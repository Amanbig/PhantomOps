package main

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"PhantomOps/internal/config"
	"PhantomOps/internal/engine"
	"PhantomOps/internal/llm"
	"PhantomOps/internal/model"
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
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{aiCache: map[string]model.Investigation{}, aiFlight: map[string]bool{}, aiQueue: make(chan struct{}, 1)}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.reload()
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
	return nil
}

// TestConnection checks that a connection works without saving it.
func (a *App) TestConnection(conn config.Connection) TestResult {
	// A masked token means "unchanged": use the stored one.
	probe := config.Config{Connections: []config.Connection{conn}}
	a.mu.Lock()
	probe.RestoreSecrets(a.cfg)
	a.mu.Unlock()
	probe.Normalize()
	conn = probe.Connections[0]

	col, err := conn.Collector()
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
// detected incidents. Failures are reported per connection in Result.Errors.
func (a *App) Scan() engine.Result {
	a.mu.Lock()
	eng, notes := a.eng, a.notes
	a.mu.Unlock()

	res := eng.Scan(a.context())
	res.Warnings = append(res.Warnings, notes...)

	a.mu.Lock()
	a.attachAI(&res)
	a.last = res
	a.mu.Unlock()
	return res
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
