package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"PhantomOps/internal/history"
	"PhantomOps/internal/report"
	"PhantomOps/internal/version"
)

// IncidentReport renders an incident from the history as a Markdown
// postmortem (timeline, root cause, triage and any rollback audit entries).
// Log excerpts are left out and secrets are redacted.
func (a *App) IncidentReport(id string) (string, error) {
	md, _, err := a.buildReport(id)
	return md, err
}

// SaveIncidentReport asks where to save the report and writes it. It returns
// the saved path, or "" if the user cancelled the dialog.
func (a *App) SaveIncidentReport(id string) (string, error) {
	md, rec, err := a.buildReport(id)
	if err != nil {
		return "", err
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Save incident report",
		DefaultFilename: reportFilename(rec.Title, time.Now()),
		Filters:         []runtime.FileFilter{{DisplayName: "Markdown (*.md)", Pattern: "*.md"}},
	})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}
	if err := os.WriteFile(path, []byte(md), 0o600); err != nil {
		return "", fmt.Errorf("could not save the report: %w", err)
	}
	return path, nil
}

func (a *App) buildReport(id string) (string, history.Record, error) {
	if a.history == nil {
		return "", history.Record{}, fmt.Errorf("incident history is not available")
	}
	var rec history.Record
	found := false
	for _, r := range a.history.List() {
		if r.ID == id {
			rec, found = r, true
			break
		}
	}
	if !found {
		return "", rec, fmt.Errorf("incident %q is not in the history (it may have been cleared)", id)
	}
	// A missing or unreadable audit log must not block the report.
	entries, _ := a.audit.Recent(500)
	return report.Markdown(rec, entries, report.Options{Now: time.Now(), Version: version.Version}), rec, nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// reportFilename builds a safe default file name, e.g.
// phantomops-incident-api-crash-loop-2026-03-01.md
func reportFilename(title string, now time.Time) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" {
		slug = "report"
	}
	return fmt.Sprintf("phantomops-incident-%s-%s.md", slug, now.UTC().Format("2006-01-02"))
}
