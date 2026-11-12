package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ConnectionSnapshot returns the current state of one connection for its
// dashboard (read-only). It fails for disabled connections and for types
// without a dashboard.
func (a *App) ConnectionSnapshot(id string) (any, error) {
	a.mu.Lock()
	var found bool
	cfg := a.cfg
	a.mu.Unlock()
	for _, cn := range cfg.Connections {
		if cn.ID != id {
			continue
		}
		found = true
		if !cn.Enabled {
			return nil, errors.New("this connection is disabled; enable it to see its dashboard")
		}
		s, ok, err := cn.Snapshotter()
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("dashboards for %s connections are not available yet", cn.Type)
		}
		ctx, cancel := context.WithTimeout(a.context(), 25*time.Second)
		defer cancel()
		return s.Snapshot(ctx)
	}
	if !found {
		return nil, errors.New("connection not found")
	}
	return nil, nil
}
