// Package collector defines the contract every infrastructure integration
// (Docker, Kubernetes, Nomad, Prometheus, Loki, SSH) implements.
package collector

import (
	"context"
	"time"

	"PhantomOps/internal/model"
)

// Collector gathers events from one infrastructure source.
type Collector interface {
	// Name is a short identifier, e.g. "docker".
	Name() string
	// Collect returns events observed since the given time, oldest first.
	// Implementations must respect ctx cancellation.
	Collect(ctx context.Context, since time.Time) ([]model.Event, error)
}
