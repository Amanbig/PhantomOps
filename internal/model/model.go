// Package model defines the core domain types shared by collectors,
// the correlation engine, the LLM investigator and the UI.
package model

import "time"

// Severity of an incident or event.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Source identifies which integration produced an event.
type Source string

const (
	SourceDocker     Source = "docker"
	SourceKubernetes Source = "kubernetes"
	SourceNomad      Source = "nomad"
	SourcePrometheus Source = "prometheus"
	SourceLoki       Source = "loki"
	SourceSSH        Source = "ssh"
	SourceDeploy     Source = "deploy"
	SourceProxy      Source = "proxy"
	SourceDatabase   Source = "database"
)

// Kind is a coarse classification used by the correlation engine.
type Kind string

const (
	KindDeployment Kind = "deployment"
	KindResource   Kind = "resource"
	KindDependency Kind = "dependency"
	KindDegraded   Kind = "degradation"
	KindLog        Kind = "log"
	KindOther      Kind = "other"
)

// Event is a single observation from the infrastructure.
type Event struct {
	Time     time.Time `json:"time"`
	Source   Source    `json:"source"`
	Kind     Kind      `json:"kind"`
	Severity Severity  `json:"severity"`
	Title    string    `json:"title"`
	Detail   string    `json:"detail"`
	Subject  string    `json:"subject"` // e.g. container, pod or job name
}

// Investigation is the "Phantom's investigation" section of an incident.
type Investigation struct {
	RootCause         string  `json:"rootCause"`
	Confidence        float64 `json:"confidence"` // 0..1
	RecommendedAction string  `json:"recommendedAction"`
	// Action is a machine-readable remediation id (e.g. "rollback"),
	// empty when nothing can be automated.
	Action string `json:"action"`
	// Target is what the action applies to (e.g. "default/api" for a rollback).
	Target string `json:"target"`
	// Reasoning lists the evidence behind the conclusion, in plain language.
	Reasoning []string `json:"reasoning"`
}

// Incident groups correlated events into a timeline.
type Incident struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	Severity      Severity      `json:"severity"`
	Environment   string        `json:"environment"`
	DetectedAt    time.Time     `json:"detectedAt"`
	Automatic     bool          `json:"automatic"`
	Demo          bool          `json:"demo"` // true for illustrative data
	Timeline      []Event       `json:"timeline"`
	Investigation Investigation `json:"investigation"`
}
