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
	// Origin is the name of the connection that produced the event. It is
	// stamped by the config layer; empty for replayed or synthetic events.
	Origin string `json:"origin"`
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
	// TargetSource is the platform owning Target ("kubernetes" or "nomad") and
	// TargetConnection the connection that can act on it.
	TargetSource     string `json:"targetSource"`
	TargetConnection string `json:"targetConnection"`
	// Reasoning lists the evidence behind the conclusion, in plain language.
	Reasoning []string `json:"reasoning"`

	// Source is "rules" (deterministic engine) or "ai" (LLM-refined).
	Source string `json:"source"`
	// Model is the LLM that produced an "ai" investigation.
	Model string `json:"model"`
	// AIStatus is "" (AI off), "pending" (analysis running) or "failed".
	AIStatus string `json:"aiStatus"`
	// AINote explains an AI failure or a safety decision (e.g. a dropped action).
	AINote string `json:"aiNote"`
	// CitedEvents are 1-based timeline positions the AI cited as evidence.
	CitedEvents []int `json:"citedEvents"`
	// RuleBasedRootCause preserves the engine's hypothesis when AI replaced it.
	RuleBasedRootCause string `json:"ruleBasedRootCause"`
}

// Incident groups correlated events into a timeline.
type Incident struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	Severity      Severity      `json:"severity"`
	Environment   string        `json:"environment"`
	DetectedAt    time.Time     `json:"detectedAt"`
	Automatic     bool          `json:"automatic"`
	Timeline      []Event       `json:"timeline"`
	Investigation Investigation `json:"investigation"`
}
