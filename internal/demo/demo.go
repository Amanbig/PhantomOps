// Package demo provides illustrative incidents so the UI can be developed
// and demonstrated without live infrastructure.
package demo

import (
	"time"

	"PhantomOps/internal/model"
)

// Incident42 returns the illustrative "API service degradation" incident.
func Incident42() model.Incident {
	base := time.Date(2026, 11, 11, 12, 1, 0, 0, time.UTC)
	at := func(min int) time.Time { return base.Add(time.Duration(min) * time.Minute) }

	return model.Incident{
		ID:          "0042",
		Title:       "API service degradation",
		Severity:    model.SeverityCritical,
		Environment: "PRODUCTION",
		DetectedAt:  at(3),
		Automatic:   true,
		Demo:        true,
		Timeline: []model.Event{
			{
				Time: at(0), Source: model.SourceDeploy, Kind: model.KindDeployment,
				Severity: model.SeverityInfo, Subject: "api",
				Title: "Deployment detected", Detail: "New API version deployed.",
			},
			{
				Time: at(1), Source: model.SourceDocker, Kind: model.KindResource,
				Severity: model.SeverityWarning, Subject: "api",
				Title: "Memory spike", Detail: "Container memory increased from 320 MB to 890 MB.",
			},
			{
				Time: at(2), Source: model.SourceDatabase, Kind: model.KindDependency,
				Severity: model.SeverityCritical, Subject: "postgres",
				Title: "Database exhaustion", Detail: "Connection pool reached maximum capacity.",
			},
			{
				Time: at(3), Source: model.SourceProxy, Kind: model.KindDegraded,
				Severity: model.SeverityCritical, Subject: "nginx",
				Title: "Service degradation", Detail: "Reverse proxy started returning 502 errors.",
			},
		},
		Investigation: model.Investigation{
			RootCause:         "The latest deployment appears to have introduced a database connection leak.",
			Confidence:        0.87,
			RecommendedAction: "Roll back to the previous stable deployment and investigate connection lifecycle handling.",
			Action:            "rollback",
		},
	}
}
