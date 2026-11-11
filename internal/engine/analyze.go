package engine

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"PhantomOps/internal/model"
)

// finding is the output of a root-cause hypothesis.
type finding struct {
	headline       string
	cause          string
	recommendation string
	action         string // machine-readable remediation id, "" if none
	target         string
	confidence     float64
	reasons        []string
}

// maxDeployLead is how long before the first symptom a deployment is still
// considered a plausible trigger.
const maxDeployLead = 15 * time.Minute

// analyze evaluates every hypothesis and returns the most confident one.
// Order matters only for ties: earlier hypotheses win.
func analyze(occ []occurrence) finding {
	hypotheses := []func([]occurrence) finding{
		deploymentRegression,
		startFailure,
		memoryExhaustion,
		dependencyExhaustion,
		unhealthyService,
		crashLoop,
		hostPressure,
	}
	var best finding
	for _, h := range hypotheses {
		if f := h(occ); f.confidence > best.confidence {
			best = f
		}
	}
	if best.confidence == 0 {
		return fallback(occ)
	}
	if best.confidence > 0.95 {
		best.confidence = 0.95
	}
	return best
}

// --- event classification -------------------------------------------------

func text(e model.Event) string { return strings.ToLower(e.Title + " " + e.Detail) }

func hasAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func titleIn(e model.Event, titles ...string) bool {
	for _, t := range titles {
		if strings.EqualFold(e.Title, t) {
			return true
		}
	}
	return false
}

// isStrongDeploy marks changes that can be rolled back (as opposed to a mere
// container/pod start, which also happens on every restart).
func isStrongDeploy(e model.Event) bool {
	return e.Kind == model.KindDeployment &&
		(e.Source == model.SourceDeploy ||
			titleIn(e, "Deployment rollout", "Job submitted", "Deployment detected"))
}

func isSymptom(e model.Event) bool {
	return e.Severity != model.SeverityInfo
}

func isOOM(e model.Event) bool { return strings.Contains(strings.ToLower(e.Title), "oom") }

func isMemoryPressure(e model.Event) bool {
	t := strings.ToLower(e.Title)
	return isSymptom(e) && hasAny(t, "oom", "memory climbing", "high memory", "low available memory", "memory spike")
}

var depExhaustion = regexp.MustCompile(`(?i)(connection pool|too many connections|max_connections|connections? (exhaust|limit)|pool (is )?(exhaust|full))`)

func isDepExhaustion(e model.Event) bool {
	return isSymptom(e) && depExhaustion.MatchString(e.Title+" "+e.Detail)
}

func isStartFailure(e model.Event) bool {
	return isSymptom(e) && titleIn(e, "ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError",
		"CreateContainerError", "InvalidImageName", "Driver Failure", "Setup Failure", "Failed Validation", "Pod failed")
}

func isCrash(e model.Event) bool {
	return isSymptom(e) && titleIn(e, "CrashLoopBackOff", "Task gave up restarting", "Container restarted",
		"Container crashed", "Task restarting", "Container terminated", "Task exited") ||
		(isSymptom(e) && strings.HasPrefix(e.Title, "BackOff"))
}

func isUnhealthy(e model.Event) bool {
	return isSymptom(e) && (titleIn(e, "Healthcheck failing", "Allocation unhealthy") ||
		strings.HasPrefix(e.Title, "Unhealthy"))
}

// --- subject helpers ------------------------------------------------------

var stopTokens = map[string]bool{
	"default": true, "kube": true, "system": true, "phantom": true, "dev": true,
	"the": true, "svc": true, "pod": true, "task": true, "job": true,
}

func tokens(subject string) map[string]bool {
	out := map[string]bool{}
	for _, t := range regexp.MustCompile(`[^a-z0-9]+`).Split(strings.ToLower(subject), -1) {
		if len(t) >= 3 && !stopTokens[t] {
			out[t] = true
		}
	}
	return out
}

func related(a, b string) bool {
	ta, tb := tokens(a), tokens(b)
	for t := range ta {
		if tb[t] {
			return true
		}
	}
	return false
}

var podSuffix = regexp.MustCompile(`-[a-z0-9]{8,10}-[a-z0-9]{5}$`)

// shortName strips the namespace and Kubernetes pod hash suffix for display,
// e.g. "phantom-dev/leaky-api-5994c75874-cb4vm" -> "leaky-api".
func shortName(subject string) string {
	parts := strings.Split(subject, "/")
	if len(parts) > 1 {
		parts = parts[1:]
	}
	return podSuffix.ReplaceAllString(parts[0], "")
}

func firstWhere(occ []occurrence, pred func(model.Event) bool) (occurrence, bool) {
	for _, o := range occ {
		if pred(o.Event) {
			return o, true
		}
	}
	return occurrence{}, false
}

func countWhere(occ []occurrence, pred func(model.Event) bool) int {
	n := 0
	for _, o := range occ {
		if pred(o.Event) {
			n += o.Count
		}
	}
	return n
}

func clock(t time.Time) string { return t.UTC().Format("15:04:05") }

func trunc(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// --- hypotheses -----------------------------------------------------------

// deploymentRegression: a rollback-able change shortly preceded the first
// symptoms, which look like resource or dependency exhaustion.
func deploymentRegression(occ []occurrence) finding {
	first, ok := firstWhere(occ, isSymptom)
	if !ok {
		return finding{}
	}
	// Prefer a deployment of a service that shows symptoms; among equals the
	// latest one wins.
	var deploy *occurrence
	deployRelated := false
	for i := range occ {
		o := occ[i]
		lead := first.Time.Sub(o.Time)
		if !isStrongDeploy(o.Event) || lead < -time.Minute || lead > maxDeployLead {
			continue
		}
		rel := false
		for _, s := range occ {
			if isSymptom(s.Event) && related(s.Subject, o.Subject) {
				rel = true
				break
			}
		}
		// Take the candidate if it is the first, or at least as related as
		// the current pick (later deployments win ties).
		if deploy == nil || rel || !deployRelated {
			deploy, deployRelated = &occ[i], rel
		}
	}
	if deploy == nil {
		return finding{}
	}
	name := shortName(deploy.Subject)
	lead := first.Time.Sub(deploy.Time).Round(time.Second)
	if lead < 0 {
		lead = 0
	}

	mem := countWhere(occ, isMemoryPressure) > 0
	dep := countWhere(occ, isDepExhaustion) > 0
	startFail := countWhere(occ, isStartFailure) > 0
	rel := false
	for _, o := range occ {
		if isSymptom(o.Event) && related(o.Subject, deploy.Subject) {
			rel = true
		}
	}

	f := finding{
		headline: "Deployment regression suspected: " + name,
		action:   "rollback", target: deploy.Subject, confidence: 0.45,
		reasons: []string{fmt.Sprintf("%s was deployed at %s, %s before the first symptom.", deploy.Subject, clock(deploy.Time), lead)},
	}
	if mem {
		f.confidence += 0.20
		f.reasons = append(f.reasons, "Memory growth or OOM kills started after the deployment.")
	}
	if dep {
		f.confidence += 0.10
		f.reasons = append(f.reasons, "Logs report connection/dependency exhaustion.")
	}
	if rel {
		f.confidence += 0.10
		f.reasons = append(f.reasons, "Symptoms occur on the same service that was deployed.")
	} else {
		f.reasons = append(f.reasons, "No symptom names the deployed service directly (correlation by time only).")
	}
	if hasDownstream(occ) {
		f.confidence += 0.05
		f.reasons = append(f.reasons, "Downstream service degradation followed.")
	}
	if startFail && !mem && !dep {
		// Startup failures are covered by startFailure; avoid a rollback
		// recommendation for a first-ever deploy that never ran.
		return finding{}
	}

	switch {
	case mem && dep:
		f.cause = fmt.Sprintf("The latest deployment of %s appears to have introduced a resource leak: memory growth was followed by connection/dependency exhaustion.", name)
	case mem:
		f.cause = fmt.Sprintf("The latest deployment of %s appears to have introduced a memory problem: memory growth or OOM kills began %s after it.", name, lead)
	case dep:
		f.cause = fmt.Sprintf("The latest deployment of %s appears to have introduced connection/dependency exhaustion.", name)
	default:
		f.cause = fmt.Sprintf("Problems began %s after the latest deployment of %s, which is the most likely trigger.", lead, name)
	}
	f.recommendation = fmt.Sprintf("Roll back %s to the previous stable deployment and investigate resource and connection lifecycle handling.", name)
	return f
}

// hasDownstream reports whether a different service started degrading after
// a resource problem (e.g. a proxy returning 5xx after an upstream OOM).
func hasDownstream(occ []occurrence) bool {
	var resourceSubjects []string
	for _, o := range occ {
		if !isSymptom(o.Event) {
			continue
		}
		if o.Kind == model.KindResource {
			resourceSubjects = append(resourceSubjects, o.Subject)
			continue
		}
		if len(resourceSubjects) == 0 || (o.Kind != model.KindDegraded && o.Kind != model.KindDependency) {
			continue
		}
		unrelated := true
		for _, rs := range resourceSubjects {
			if related(rs, o.Subject) {
				unrelated = false
				break
			}
		}
		if unrelated {
			return true
		}
	}
	return false
}

func startFailure(occ []occurrence) finding {
	o, ok := firstWhere(occ, isStartFailure)
	if !ok {
		return finding{}
	}
	name := shortName(o.Subject)
	n := countWhere(occ, isStartFailure)
	f := finding{
		headline:   "Startup failure: " + name,
		confidence: 0.75,
		cause:      fmt.Sprintf("%s cannot start: %s", name, trunc(o.Detail, 180)),
		recommendation: "Fix the image reference, registry credentials or container configuration, then redeploy. " +
			"If an earlier version was healthy, roll back to it.",
		reasons: []string{fmt.Sprintf("%s reported at %s on %s.", o.Title, clock(o.Time), o.Subject)},
	}
	if n > 1 {
		f.confidence += 0.05
		f.reasons = append(f.reasons, fmt.Sprintf("The failure repeated (%d reports).", n))
	}
	return f
}

func memoryExhaustion(occ []occurrence) finding {
	pressure, ok := firstWhere(occ, isMemoryPressure)
	if !ok {
		return finding{}
	}
	kills := countWhere(occ, isOOM)
	climbing := countWhere(occ, func(e model.Event) bool {
		return hasAny(strings.ToLower(e.Title), "memory climbing", "memory spike")
	}) > 0

	name := shortName(pressure.Subject)
	f := finding{
		headline:       "Memory exhaustion: " + name,
		confidence:     0.40,
		recommendation: "Raise the memory limit as a stopgap and profile the service for a memory leak.",
		reasons:        []string{fmt.Sprintf("%s at %s on %s.", pressure.Title, clock(pressure.Time), pressure.Subject)},
	}
	if kills > 0 {
		f.confidence = 0.60
		f.reasons = append(f.reasons, fmt.Sprintf("%d OOM kill(s) recorded.", kills))
	}
	if kills > 1 {
		f.confidence += 0.10
	}
	if climbing {
		f.confidence += 0.10
		f.reasons = append(f.reasons, "Memory was growing steadily before the limit was hit (leak signature).")
	}
	if kills > 0 {
		f.cause = fmt.Sprintf("%s is being OOM-killed (%d time(s)): its memory usage exceeds the configured limit.", name, kills)
	} else {
		f.cause = fmt.Sprintf("%s is running low on memory and may be OOM-killed soon.", name)
	}
	return f
}

func dependencyExhaustion(occ []occurrence) finding {
	o, ok := firstWhere(occ, isDepExhaustion)
	if !ok {
		return finding{}
	}
	name := shortName(o.Subject)
	f := finding{
		headline:       "Dependency exhaustion: " + name,
		confidence:     0.50,
		cause:          fmt.Sprintf("%s reports connection/dependency exhaustion: %s", name, trunc(o.Detail, 160)),
		recommendation: "Check connection pool sizing and leaks, and the health and limits of the dependency (e.g. database max_connections).",
		reasons:        []string{fmt.Sprintf("%s at %s on %s.", o.Title, clock(o.Time), o.Subject)},
	}
	if countWhere(occ, isDepExhaustion) >= 3 {
		f.confidence += 0.05
	}
	return f
}

// unhealthyService: a running service fails its health checks. Error logs
// from the same service, if present, are added as evidence.
func unhealthyService(occ []occurrence) finding {
	o, ok := firstWhere(occ, isUnhealthy)
	if !ok {
		return finding{}
	}
	name := shortName(o.Subject)
	f := finding{
		headline:       "Unhealthy service: " + name,
		confidence:     0.55,
		cause:          fmt.Sprintf("%s is running but failing its health checks.%s", name, probeNote(o.Detail)),
		recommendation: "Run the health check by hand and read the service's recent logs; check whether a dependency it needs is reachable. Capture logs before restarting.",
		reasons:        []string{fmt.Sprintf("%s at %s on %s.", o.Title, clock(o.Time), o.Subject)},
	}
	for _, l := range occ {
		if l.Kind == model.KindLog && isSymptom(l.Event) && related(l.Subject, o.Subject) {
			f.confidence += 0.10
			f.reasons = append(f.reasons, fmt.Sprintf("The service is also logging errors: %s", trunc(l.Detail, 120)))
			break
		}
	}
	if o.Count > 1 {
		f.confidence += 0.05
	}
	return f
}

// probeNote extracts the "Last probe output: ..." part of a health event.
func probeNote(detail string) string {
	if i := strings.Index(detail, "Last probe output:"); i >= 0 {
		return " " + trunc(detail[i:], 200)
	}
	return ""
}

func crashLoop(occ []occurrence) finding {
	o, ok := firstWhere(occ, isCrash)
	if !ok {
		return finding{}
	}
	name := shortName(o.Subject)
	n := countWhere(occ, isCrash)
	f := finding{
		headline:       "Crash loop: " + name,
		confidence:     0.45,
		cause:          fmt.Sprintf("%s keeps crashing and restarting (%d related events).", name, n),
		recommendation: "Inspect the service's logs and last exit code, and check recent image or configuration changes.",
		reasons:        []string{fmt.Sprintf("%s at %s on %s.", o.Title, clock(o.Time), o.Subject)},
	}
	if n >= 3 {
		f.confidence += 0.05
	}
	return f
}

func hostPressure(occ []occurrence) finding {
	var hits []occurrence
	for _, o := range occ {
		if o.Source == model.SourceSSH && isSymptom(o.Event) {
			hits = append(hits, o)
		}
	}
	if len(hits) == 0 {
		return finding{}
	}
	var parts []string
	var reasons []string
	for _, h := range hits {
		parts = append(parts, strings.ToLower(h.Title)+" ("+trunc(strings.TrimSuffix(h.Detail, "."), 80)+")")
		reasons = append(reasons, fmt.Sprintf("%s on %s.", h.Title, h.Subject))
	}
	conf := 0.55
	if len(hits) > 1 {
		conf = 0.60
	}
	return finding{
		headline:       "Host resource pressure: " + hits[0].Subject,
		confidence:     conf,
		cause:          fmt.Sprintf("Host %s is under resource pressure: %s.", hits[0].Subject, strings.Join(parts, "; ")),
		recommendation: "Identify the heaviest processes (top/ps), free disk space or add capacity.",
		reasons:        reasons,
	}
}

func fallback(occ []occurrence) finding {
	o, ok := firstWhere(occ, isSymptom)
	if !ok {
		return finding{headline: "Unclassified activity", cause: "No anomaly detected.", confidence: 0.1}
	}
	name := shortName(o.Subject)
	return finding{
		headline:       "Anomalies on " + name,
		confidence:     0.20,
		cause:          fmt.Sprintf("The earliest anomaly was %q on %s. No known failure pattern matched.", o.Title, name),
		recommendation: "Review the timeline and the affected service's logs for the first anomaly.",
		reasons:        []string{fmt.Sprintf("%s at %s on %s.", o.Title, clock(o.Time), o.Subject)},
	}
}
