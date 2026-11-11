package main

import (
	"context"
	"fmt"

	"PhantomOps/internal/demo"
	"PhantomOps/internal/model"
)

// App is the Wails-bound backend. Exported methods are callable from the UI.
type App struct {
	ctx context.Context
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// ListIncidents returns all known incidents, newest first.
// For now this only returns the illustrative demo incident.
func (a *App) ListIncidents() []model.Incident {
	return []model.Incident{demo.Incident42()}
}

// GetIncident returns a single incident by ID.
func (a *App) GetIncident(id string) (model.Incident, error) {
	for _, inc := range a.ListIncidents() {
		if inc.ID == id {
			return inc, nil
		}
	}
	return model.Incident{}, fmt.Errorf("incident %q not found", id)
}
