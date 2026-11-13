package main

import (
	"context"
	"errors"
	"fmt"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"PhantomOps/internal/config"
	"PhantomOps/internal/engine"
	"PhantomOps/internal/history"
	"PhantomOps/internal/llm"
	"PhantomOps/internal/model"
	"PhantomOps/internal/notify"
	"PhantomOps/internal/remediate"
	"PhantomOps/internal/updater"
)

// aiEvent is the name of the UI event emitted when an AI analysis finishes.
const aiEvent = "ai:update"

// maxAIIncidents bounds how many incidents are sent to the model per scan.
const maxAIIncidents = 5

// AIUpdate is the payload of the ai:update event.
type AIUpdate struct {
	ID            string              `json:"id"`
	Investigation model.Investigation `json:"investigation"`
}

// AITestResult is the outcome of testing the AI endpoint.
type AITestResult struct {
	OK      bool     `json:"ok"`
	Message string   `json:"message"`
	Models  []string `json:"models"`
	// Remote is true when the endpoint is outside this machine/private
	// network, i.e. incident data would be sent over the internet.
	Remote bool `json:"remote"`
}

// Settings is what the UI shows on the Connections screen.
type Settings struct {
	Config config.Config `json:"config"` // secrets are masked
	Path   string        `json:"path"`
	// Saved is false when the config file does not exist yet and the
	// connections shown are auto-detected defaults.
	Saved bool     `json:"saved"`
	Notes []string `json:"notes"`
}

// TestResult is the outcome of testing a connection.
type TestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// App is the Wails-bound backend. Exported methods are callable from the UI.
type App struct {
	ctx context.Context

	mu    sync.Mutex
	cfg   config.Config
	saved bool
	eng   *engine.Engine
	notes []string // problems building collectors / reading config
	last  engine.Result

	ai       *llm.Investigator
	aiGen    int                            // bumped when settings change; discards stale results
	aiCache  map[string]model.Investigation // incident fingerprint -> AI result
	aiFlight map[string]bool                // fingerprints being analysed
	aiQueue  chan struct{}                  // serialises model calls (local models are slow)

	approvals *remediate.Approvals
	audit     *remediate.AuditLog
	remMu     sync.Mutex // one remediation at a time

	history     *history.Store
	historyErr  string
	scanMu      sync.Mutex // scans never overlap
	scanning    bool
	lastScan    time.Time
	nextScan    time.Time
	monitorWake chan struct{}
	notify      func(id, title, body string, data map[string]any) error
	notifyErr   string
	hooks       *notify.Sender
	hookStatus  map[string]WebhookStatus // last delivery result per webhook ID

	updater       *updater.Updater
	pendingUpdate *updater.Info
}

// RollbackPreview is what the user reviews before approving a rollback.
type RollbackPreview struct {
	Token      string         `json:"token"` // empty when the plan is not feasible
	IncidentID string         `json:"incidentId"`
	Plan       remediate.Plan `json:"plan"`
	// ConfirmText is what the user must type to approve (the workload name).
	ConfirmText string `json:"confirmText"`
	ExpiresIn   int    `json:"expiresIn"` // seconds
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		aiCache: map[string]model.Investigation{}, aiFlight: map[string]bool{}, aiQueue: make(chan struct{}, 1),
		approvals:   remediate.NewApprovals(),
		monitorWake: make(chan struct{}, 1),
		hooks:       notify.NewSender(),
		audit:       remediate.NewAuditLog(filepath.Join(filepath.Dir(config.Path()), "audit.log")),
	}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.reload()
	h, err := history.Open(filepath.Join(filepath.Dir(config.Path()), "history.json"))
	if err != nil {
		a.historyErr = err.Error()
	}
	a.history = h
	a.initNotifications()
	go a.monitorLoop()
	a.initUpdater()
}

// reload re-reads the config file and rebuilds the engine.
func (a *App) reload() {
	cfg, saved, err := config.Load(config.Path())
	var notes []string
	if err != nil {
		notes = append(notes, "config: "+err.Error())
	}
	a.apply(cfg, saved, notes)
}

func (a *App) apply(cfg config.Config, saved bool, notes []string) {
	eng, more := cfg.Engine()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg, a.saved, a.eng, a.notes = cfg, saved, eng, append(notes, more...)
	a.ai = cfg.Investigator()
	a.aiGen++
	a.aiCache = map[string]model.Investigation{}
	a.aiFlight = map[string]bool{}
}

func (a *App) context() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// GetSettings returns the current settings with secrets masked.
func (a *App) GetSettings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Settings{Config: a.cfg.Redacted(), Path: config.Path(), Saved: a.saved, Notes: a.notes}
}

// SaveSettings validates and persists the settings, then rebuilds the engine.
// Masked secrets are replaced by the stored values.
func (a *App) SaveSettings(cfg config.Config) error {
	a.mu.Lock()
	old := a.cfg
	a.mu.Unlock()

	cfg.RestoreSecrets(old)
	cfg.Normalize()
	if err := config.Save(config.Path(), cfg); err != nil {
		return err
	}
	a.apply(cfg, true, nil)
	a.wakeMonitor()
	return nil
}

// TestConnection checks that a connection works without saving it.
func (a *App) TestConnection(conn config.Connection) TestResult {
	// A masked token means "unchanged": use the stored one.
	probe := config.Config{Connections: []config.Connection{conn}}
	a.mu.Lock()
	old := a.cfg
	probe.RestoreSecrets(old)
	full := a.cfg
	a.mu.Unlock()
	probe.Normalize()
	conn = probe.Connections[0]
	if conn.ID != "" {
		replaced := false
		for i := range full.Connections {
			if full.Connections[i].ID == conn.ID {
				full.Connections[i] = conn
				replaced = true
				break
			}
		}
		if !replaced {
			full.Connections = append(full.Connections, conn)
		}
	}

	col, err := full.CollectorFor(conn)
	if err != nil {
		return TestResult{Message: err.Error()}
	}
	ctx, cancel := context.WithTimeout(a.context(), 20*time.Second)
	defer cancel()
	start := time.Now()
	events, err := col.Collect(ctx, time.Now().Add(-30*time.Minute))
	if err != nil {
		return TestResult{Message: err.Error()}
	}
	return TestResult{OK: true, Message: fmt.Sprintf("Connected in %d ms · %d recent event(s)",
		time.Since(start).Milliseconds(), len(events))}
}

// DetectServices finds services on this machine that can be connected.
func (a *App) DetectServices() []config.Connection {
	ctx, cancel := context.WithTimeout(a.context(), 8*time.Second)
	defer cancel()
	return config.Discover(ctx)
}

// Scan runs every enabled connection, correlates the events and returns the
// detected incidents with stable IDs from the incident history. Failures are
// reported per connection in Result.Errors.
func (a *App) Scan() engine.Result {
	return a.runScan(false)
}

// fingerprint identifies an incident's current evidence so AI results are
// reused until the timeline changes.
func fingerprint(inc model.Incident) string {
	last := ""
	if n := len(inc.Timeline); n > 0 {
		last = inc.Timeline[n-1].Time.UTC().Format(time.RFC3339Nano)
	}
	return inc.ID + "|" + strconv.Itoa(len(inc.Timeline)) + "|" + last
}

// attachAI applies cached AI results to the incidents and starts analysis for
// the rest in the background. Callers must hold a.mu.
func (a *App) attachAI(res *engine.Result) {
	if a.ai == nil {
		return
	}
	for i := range res.Incidents {
		if i >= maxAIIncidents {
			break
		}
		inc := &res.Incidents[i]
		key := fingerprint(*inc)
		if cached, ok := a.aiCache[key]; ok {
			inc.Investigation = cached
			continue
		}
		base := *inc // rule-based version, sent to the model
		inc.Investigation.AIStatus = "pending"
		if !a.aiFlight[key] {
			a.aiFlight[key] = true
			go a.analyze(base, key, a.ai, a.aiGen)
		}
	}
}

// analyze runs one AI refinement and publishes the outcome to the UI.
func (a *App) analyze(base model.Incident, key string, ai *llm.Investigator, gen int) {
	a.aiQueue <- struct{}{} // one model call at a time
	defer func() { <-a.aiQueue }()

	ctx, cancel := context.WithTimeout(a.context(), llm.Timeout)
	defer cancel()
	inv, err := ai.Refine(ctx, base)

	a.mu.Lock()
	defer a.mu.Unlock()
	if gen != a.aiGen {
		return // settings changed while we were waiting; drop the result
	}
	delete(a.aiFlight, key)
	if err != nil {
		inv = base.Investigation
		inv.AIStatus = "failed"
		inv.AINote = "AI analysis unavailable: " + err.Error()
	} else {
		a.aiCache[key] = inv
	}
	for i := range a.last.Incidents {
		if a.last.Incidents[i].ID == base.ID {
			a.last.Incidents[i].Investigation = inv
		}
	}
	if a.history != nil {
		_ = a.history.UpdateInvestigation(base.ID, inv)
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, aiEvent, AIUpdate{ID: base.ID, Investigation: inv})
	}
}

// TestAI checks the AI endpoint and lists the models it offers.
func (a *App) TestAI(cfg config.AIConfig) AITestResult {
	a.mu.Lock()
	if cfg.APIKey == config.SecretMask {
		cfg.APIKey = a.cfg.AI.APIKey
	}
	a.mu.Unlock()

	probe := cfg
	probe.Enabled = true
	if err := probe.Validate(); err != nil {
		return AITestResult{Message: err.Error()}
	}
	res := AITestResult{Remote: !config.IsLocalEndpoint(cfg.BaseURL)}
	ctx, cancel := context.WithTimeout(a.context(), 15*time.Second)
	defer cancel()
	models, err := llm.NewClient(cfg.BaseURL, cfg.Model, cfg.APIKey).Models(ctx)
	if err != nil {
		res.Message = err.Error()
		return res
	}
	res.Models = models
	found := len(models) == 0 // some servers don't list models; don't block them
	for _, m := range models {
		if m == cfg.Model || m == cfg.Model+":latest" {
			found = true
		}
	}
	if !found {
		res.Message = fmt.Sprintf("Connected, but model %q is not available on this server (%d model(s) offered).", cfg.Model, len(models))
		return res
	}
	res.OK = true
	res.Message = fmt.Sprintf("Connected · model %q is available", cfg.Model)
	return res
}

// GetIncident returns an incident from the last scan by ID.
func (a *App) GetIncident(id string) (model.Incident, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, inc := range a.last.Incidents {
		if inc.ID == id {
			return inc, nil
		}
	}
	return model.Incident{}, fmt.Errorf("incident %q not found", id)
}

// --- remediation -----------------------------------------------------------

// executorFor builds the executor that can act on the incident's target.
// Callers must hold a.mu.
func (a *App) executorFor(inv model.Investigation) (remediate.Executor, error) {
	if inv.TargetConnection == "" {
		return nil, errors.New("PhantomOps cannot tell which connection owns this deployment (the incident was not produced by a live scan)")
	}
	conn, ok := a.cfg.FindConnection(inv.TargetConnection)
	if !ok {
		return nil, fmt.Errorf("connection %q is not available or is disabled", inv.TargetConnection)
	}
	switch {
	case conn.Type == config.TypeKubernetes && inv.TargetSource == "kubernetes":
		return remediate.NewKube(conn.Name, conn.Context, conn.Kubeconfig), nil
	case conn.Type == config.TypeNomad && inv.TargetSource == "nomad":
		return remediate.NewNomad(conn.Name, conn.URL, conn.Token), nil
	}
	return nil, fmt.Errorf("connection %q (%s) cannot perform a %s rollback", conn.Name, conn.Type, inv.TargetSource)
}

func targetName(target string) string {
	if i := strings.LastIndex(target, "/"); i >= 0 {
		return target[i+1:]
	}
	return target
}

// PreviewRollback inspects the incident's rollback target and returns exactly
// what an approval would do. It changes nothing.
func (a *App) PreviewRollback(incidentID string) (RollbackPreview, error) {
	a.mu.Lock()
	enabled := a.cfg.Remediation.Enabled
	var inv model.Investigation
	found := false
	for _, inc := range a.last.Incidents {
		if inc.ID == incidentID {
			inv, found = inc.Investigation, true
		}
	}
	var ex remediate.Executor
	var err error
	if found && enabled {
		ex, err = a.executorFor(inv)
	}
	a.mu.Unlock()

	switch {
	case !enabled:
		return RollbackPreview{}, errors.New("remediation is turned off; enable it under Connections → Remediation")
	case !found:
		return RollbackPreview{}, fmt.Errorf("incident %q not found; run a new scan", incidentID)
	case inv.Action != "rollback" || inv.Target == "":
		return RollbackPreview{}, errors.New("this incident has no rollback action")
	case err != nil:
		return RollbackPreview{}, err
	}

	ctx, cancel := context.WithTimeout(a.context(), 45*time.Second)
	defer cancel()
	plan, err := ex.Plan(ctx, inv.Target)
	if err != nil {
		return RollbackPreview{}, err
	}
	pv := RollbackPreview{IncidentID: incidentID, Plan: plan, ConfirmText: targetName(inv.Target)}
	if plan.Feasible {
		pv.Token = a.approvals.Issue(remediate.Pending{Plan: plan, IncidentID: incidentID, Executor: ex})
		pv.ExpiresIn = int(remediate.ApprovalTTL.Seconds())
	}
	return pv, nil
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "unknown"
}

// ApproveRollback performs a previously previewed rollback. The caller must
// present the single-use token from PreviewRollback and type the workload
// name. The intent is written to the audit log before anything is changed;
// if that fails, nothing is changed.
func (a *App) ApproveRollback(token, typed string) (remediate.Result, error) {
	a.mu.Lock()
	enabled := a.cfg.Remediation.Enabled
	a.mu.Unlock()
	if !enabled {
		return remediate.Result{}, errors.New("remediation is turned off")
	}
	pending, err := a.approvals.Peek(token)
	if err != nil {
		return remediate.Result{}, err
	}
	if strings.TrimSpace(typed) != targetName(pending.Plan.Target) {
		return remediate.Result{}, fmt.Errorf("confirmation text does not match; type %q to approve", targetName(pending.Plan.Target))
	}
	if !a.remMu.TryLock() {
		return remediate.Result{}, errors.New("another remediation is already in progress")
	}
	defer a.remMu.Unlock()
	if _, err := a.approvals.Redeem(token); err != nil {
		return remediate.Result{}, err
	}

	pl := pending.Plan
	entry := remediate.Entry{User: currentUser(), IncidentID: pending.IncidentID, Action: "rollback",
		Platform: pl.Platform, Connection: pl.Connection, Target: pl.Target, Command: pl.Command}

	intent := entry
	intent.Phase, intent.OK, intent.Message = "approved", true, fmt.Sprintf("%s → %s", pl.Current, pl.Previous)
	if err := a.audit.Append(intent); err != nil {
		return remediate.Result{}, fmt.Errorf("refusing to act: the audit log is not writable (%s): %w", a.audit.Path(), err)
	}

	ctx, cancel := context.WithTimeout(a.context(), 4*time.Minute)
	defer cancel()
	res, execErr := pending.Executor.Execute(ctx, pl)

	outcome := entry
	outcome.Phase = "result"
	switch {
	case execErr != nil:
		outcome.OK, outcome.Message = false, execErr.Error()
	default:
		outcome.OK, outcome.Message = res.Applied && res.Healthy, res.Message
	}
	if err := a.audit.Append(outcome); err != nil && execErr == nil {
		res.Message += " (warning: could not write the result to the audit log: " + err.Error() + ")"
	}
	return res, execErr
}

// AuditLog returns the most recent audit entries, newest first.
func (a *App) AuditLog(limit int) ([]remediate.Entry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return a.audit.Recent(limit)
}
