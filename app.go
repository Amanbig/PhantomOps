package main

import (
	"context"
	"fmt"
	"sync"

	"PhantomOps/internal/config"
	"PhantomOps/internal/demo"
	"PhantomOps/internal/engine"
	"PhantomOps/internal/model"
)

// App is the Wails-bound backend. Exported methods are callable from the UI.
type App struct {
	ctx context.Context

	mu    sync.Mutex
	eng   *engine.Engine
	notes []string // config problems to show in the UI
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

// reload (re)reads the config file and rebuilds the engine.
func (a *App) reload() {
	cfg, err := config.Load(config.Path())
	var notes []string
	if err != nil {
		notes = append(notes, "config: "+err.Error())
	}
	eng, more := cfg.Engine()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.eng, a.notes = eng, append(notes, more...)
}

// Scan runs every configured collector, correlates the events and returns the
// detected incidents. Collector failures are reported in Result.Errors.
func (a *App) Scan() engine.Result {
	a.mu.Lock()
	eng, notes := a.eng, a.notes
	a.mu.Unlock()

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	res := eng.Scan(ctx)
	res.Warnings = append(res.Warnings, notes...)

	a.mu.Lock()
	a.last = res
	a.mu.Unlock()
	return res
}

// DemoIncident returns the illustrative Incident #0042.
func (a *App) DemoIncident() model.Incident {
	return demo.Incident42()
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
