// Package report renders an incident as a Markdown postmortem.
//
// Reports are meant to be shared, so they are built from a fixed set of
// fields, raw log excerpts are left out, and all free text goes through
// Redact. Redaction is best effort: review a report before publishing it.
package report

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"PhantomOps/internal/history"
	"PhantomOps/internal/model"
	"PhantomOps/internal/remediate"
)

// Options carries values the report cannot derive from the incident.
type Options struct {
	Now     time.Time
	Version string // PhantomOps version, e.g. "v0.3.0" or "dev"
}

const (
	maxDetail = 200
	maxText   = 600
)

var redactions = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(-----END [A-Z ]*PRIVATE KEY-----|$)`), "[redacted private key]"},
	{regexp.MustCompile(`https://hooks\.slack\.com/\S+`), "[redacted webhook]"},
	{regexp.MustCompile(`https://(?:discord(?:app)?\.com)/api/webhooks/\S+`), "[redacted webhook]"},
	{regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)(?:bearer|basic|token)\s+\S+`), "${1}[redacted]"},
	{regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9._~+/=-]{8,}`), "${1} [redacted]"},
	{regexp.MustCompile(`(://)[^/\s:@]+:[^/\s@]+@`), "${1}[redacted]@"},
	{regexp.MustCompile(`(?i)\b((?:[a-z0-9_-]*)(?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)[a-z0-9_-]*)\b(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;&]+)`), "${1}${2}[redacted]"},
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), "[redacted key]"},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`), "[redacted token]"},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}\b`), "[redacted token]"},
}

// Redact removes credentials that commonly leak into text: private keys,
// webhook URLs, bearer and basic auth, passwords in URLs, key=value secrets
// and well-known token formats.
func Redact(s string) string {
	for _, r := range redactions {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// Markdown renders rec and the audit entries recorded for it.
func Markdown(rec history.Record, audit []remediate.Entry, opt Options) string {
	inc := rec.Latest
	inv := inc.Investigation
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w("# Incident report: %s", text(rec.Title, 200))
	w("")
	w("| | |")
	w("| :--- | :--- |")
	w("| Severity | %s |", cell(string(rec.Severity)))
	w("| Status | %s |", cell(rec.Status))
	if inc.Environment != "" {
		w("| Environment | %s |", cell(inc.Environment))
	}
	w("| First seen | %s |", utc(rec.FirstSeen))
	if rec.Status == history.StatusOpen {
		w("| Last seen | %s |", utc(rec.LastSeen))
		w("| Open for | %s |", duration(opt.Now.Sub(rec.FirstSeen)))
	} else {
		w("| Resolved | %s |", utc(rec.LastSeen))
		w("| Lasted | %s |", duration(rec.LastSeen.Sub(rec.FirstSeen)))
	}
	w("| Seen in scans | %d |", rec.Scans)
	if rec.Reopened > 0 {
		w("| Came back | %d× |", rec.Reopened)
	}
	if t := triage(rec, opt.Now); t != "" {
		w("| Triage | %s |", cell(t))
	}

	w("")
	w("## Likely root cause")
	w("")
	w("%s", text(orDash(inv.RootCause), maxText))
	if inv.Confidence > 0 {
		w("")
		w("Confidence: %d%%. Determined by: %s.", int(inv.Confidence*100+0.5), analysedBy(inv))
	}
	if inv.Source == "ai" && inv.RuleBasedRootCause != "" {
		w("")
		w("Rule-based hypothesis: %s", text(inv.RuleBasedRootCause, maxText))
	}
	if len(inv.Reasoning) > 0 {
		w("")
		w("## Evidence")
		w("")
		for _, r := range inv.Reasoning {
			w("- %s", text(r, maxText))
		}
	}
	if inv.RecommendedAction != "" {
		w("")
		w("## Recommended action")
		w("")
		w("%s", text(inv.RecommendedAction, maxText))
	}

	w("")
	w("## Timeline (UTC)")
	w("")
	if len(inc.Timeline) == 0 {
		w("_No events were recorded._")
	} else {
		w("| Time | Source | Severity | Event | Subject |")
		w("| :--- | :--- | :--- | :--- | :--- |")
		omitted := false
		for _, e := range inc.Timeline {
			what := text(e.Title, 200)
			if e.Kind == model.KindLog {
				omitted = omitted || e.Detail != ""
			} else if d := text(e.Detail, maxDetail); d != "" {
				what += " — " + d
			}
			subject := e.Subject
			if e.Origin != "" {
				subject += " (" + e.Origin + ")"
			}
			w("| %s | %s | %s | %s | %s |", e.Time.UTC().Format("2006-01-02 15:04:05"), cell(string(e.Source)),
				cell(string(e.Severity)), cell(what), cell(text(subject, 120)))
		}
		if omitted {
			w("")
			w("_Log excerpts are left out of reports on purpose._")
		}
	}

	var mine []remediate.Entry
	for _, e := range audit {
		if e.IncidentID == rec.ID {
			mine = append(mine, e)
		}
	}
	w("")
	w("## Actions taken")
	w("")
	if len(mine) == 0 {
		w("_PhantomOps made no changes for this incident._")
	} else {
		w("| Time | User | Step | Action | Target | Result |")
		w("| :--- | :--- | :--- | :--- | :--- | :--- |")
		for _, e := range mine {
			res := "ok"
			if !e.OK {
				res = "failed"
			}
			if e.Phase == "approved" {
				res = "approved"
			}
			if e.Message != "" {
				res += ": " + text(e.Message, 200)
			}
			w("| %s | %s | %s | %s | %s | %s |", e.Time.UTC().Format("2006-01-02 15:04:05"), cell(e.User), cell(e.Phase),
				cell(e.Action), cell(text(e.Target, 120)), cell(res))
		}
	}

	w("")
	w("---")
	ver := opt.Version
	if ver == "" {
		ver = "dev"
	}
	w("Generated by PhantomOps %s on %s. Secrets are removed on a best-effort basis; review before sharing.",
		ver, opt.Now.UTC().Format("2006-01-02 15:04 UTC"))
	return b.String()
}

func triage(rec history.Record, now time.Time) string {
	var parts []string
	if rec.AckBy != "" {
		s := "acknowledged by " + rec.AckBy
		if rec.AckAt != nil {
			s += " at " + utc(*rec.AckAt)
		}
		parts = append(parts, s)
	}
	if rec.SnoozedUntil != nil && rec.SnoozedUntil.After(now) {
		parts = append(parts, "snoozed until "+utc(*rec.SnoozedUntil))
	}
	if rec.Muted {
		parts = append(parts, "muted")
	}
	return strings.Join(parts, "; ")
}

func analysedBy(inv model.Investigation) string {
	if inv.Source == "ai" {
		return "AI-assisted analysis (" + text(inv.Model, 80) + ") checked against collected evidence"
	}
	return "rule-based analysis"
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func utc(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

func duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f h", d.Hours())
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

// text makes free text safe for a report: one line, redacted, bounded.
func text(s string, max int) string {
	s = strings.Join(strings.Fields(Redact(s)), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "…"
	}
	return s
}

// cell escapes text for a Markdown table cell.
func cell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.ReplaceAll(s, "|", `\|`)
}
