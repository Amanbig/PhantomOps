package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"PhantomOps/internal/config"
	"PhantomOps/internal/engine"
	"PhantomOps/internal/model"
)

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
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
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
	a.last = res
	a.mu.Unlock()
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
