package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"PhantomOps/internal/engine"
	"PhantomOps/internal/history"
	"PhantomOps/internal/model"
)

// UI events emitted by the monitor.
const (
	scanEvent       = "scan:update" // a background scan finished; payload engine.Result
	notifyOpenEvent = "notify:open" // a notification was clicked; payload incident ID
)

// maxNotificationsPerScan bounds notification bursts; the rest are summarised.
const maxNotificationsPerScan = 3

// MonitorStatus is shown in the UI.
type MonitorStatus struct {
	Enabled         bool      `json:"enabled"`
	IntervalMinutes int       `json:"intervalMinutes"`
	LastScan        time.Time `json:"lastScan"`
	NextScan        time.Time `json:"nextScan"`
	Scanning        bool      `json:"scanning"`
	Notify          bool      `json:"notify"`
	// NotificationsError explains why desktop notifications are unavailable.
	NotificationsError string `json:"notificationsError"`
	HistoryError       string `json:"historyError"`
}

// initNotifications connects to the desktop notification service and routes
// clicks back to the incident.
func (a *App) initNotifications() {
	if err := runtime.InitializeNotifications(a.ctx); err != nil {
		a.notifyErr = "desktop notifications unavailable: " + err.Error()
		return
	}
	a.notify = func(id, title, body string, data map[string]any) error {
		return runtime.SendNotification(a.ctx, runtime.NotificationOptions{ID: id, Title: title, Body: body, Data: data})
	}
	runtime.OnNotificationResponse(a.ctx, func(r runtime.NotificationResult) {
		if r.Error != nil {
			return
		}
		runtime.WindowUnminimise(a.ctx)
		runtime.WindowShow(a.ctx)
		if id, ok := r.Response.UserInfo["incidentId"].(string); ok && id != "" {
			runtime.EventsEmit(a.ctx, notifyOpenEvent, id)
		}
	})
}

// wakeMonitor makes the monitor loop re-read its settings now.
func (a *App) wakeMonitor() {
	select {
	case a.monitorWake <- struct{}{}:
	default:
	}
}

// monitorLoop runs background scans while monitoring is enabled. It re-reads
// the interval whenever settings change.
func (a *App) monitorLoop() {
	for {
		a.mu.Lock()
		mon := a.cfg.Monitoring
		a.mu.Unlock()

		if !mon.Enabled {
			a.setNext(time.Time{})
			select {
			case <-a.ctx.Done():
				return
			case <-a.monitorWake:
				continue
			}
		}

		next := time.Now().Add(mon.Interval())
		a.setNext(next)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-a.ctx.Done():
			timer.Stop()
			return
		case <-a.monitorWake:
			timer.Stop() // settings changed: recompute the schedule
			continue
		case <-timer.C:
		}
		res := a.runScan(true)
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, scanEvent, res)
		}
	}
}

func (a *App) setNext(t time.Time) {
	a.mu.Lock()
	a.nextScan = t
	a.mu.Unlock()
}

// runScan performs one scan, updates history and (for background scans)
// sends notifications. Scans never overlap.
func (a *App) runScan(background bool) engine.Result {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()

	a.mu.Lock()
	eng, notes, mon := a.eng, a.notes, a.cfg.Monitoring
	a.scanning = true
	a.mu.Unlock()

	res := eng.Scan(a.context())
	res.Warnings = append(res.Warnings, notes...)

	var changes []history.Change
	if a.history != nil {
		// A scan with failed sources is partial: it must not resolve incidents
		// that are simply invisible right now.
		incs, ch, err := a.history.Observe(res.Incidents, len(res.Errors) == 0)
		res.Incidents, changes = incs, ch
		if err != nil {
			res.Warnings = append(res.Warnings, "incident history: "+err.Error())
		}
	}

	a.mu.Lock()
	a.attachAI(&res)
	a.last = res
	a.lastScan, a.scanning = res.ScannedAt, false
	a.mu.Unlock()

	if background && mon.Notify {
		a.sendNotifications(changes, mon.NotifyMinSeverity)
	}
	return res
}

func severityAtLeast(s model.Severity, min string) bool {
	if min == "warning" {
		return s == model.SeverityWarning || s == model.SeverityCritical
	}
	return s == model.SeverityCritical
}

func (a *App) sendNotifications(changes []history.Change, minSeverity string) {
	if a.notify == nil {
		return
	}
	type note struct{ id, title, body string }
	var notes []note
	for _, c := range changes {
		r := c.Record
		if !severityAtLeast(r.Severity, minSeverity) {
			continue
		}
		// Muted and snoozed incidents stay silent. (Acknowledged ones still
		// notify: an escalation matters even when someone is on it.)
		if a.history != nil && a.history.Suppressed(r) {
			continue
		}
		cause := r.Latest.Investigation.RootCause
		if len(cause) > 180 {
			cause = cause[:180] + "…"
		}
		var title string
		switch c.Kind {
		case "new":
			title = fmt.Sprintf("👻 %s: %s", strings.ToUpper(string(r.Severity)), r.Title)
		case "reopened":
			title = "👻 Back again: " + r.Title
		case "escalated":
			title = fmt.Sprintf("👻 Now %s: %s", strings.ToUpper(string(r.Severity)), r.Title)
		case "resolved":
			title, cause = "✅ Resolved: "+r.Title, "No longer detected after "+humanDuration(r.LastSeen.Sub(r.FirstSeen))+"."
		default:
			continue
		}
		notes = append(notes, note{r.ID, title, cause})
	}
	if len(notes) > maxNotificationsPerScan {
		extra := len(notes) - (maxNotificationsPerScan - 1)
		notes = append(notes[:maxNotificationsPerScan-1], note{"summary",
			fmt.Sprintf("👻 %d more incident updates", extra), "Open PhantomOps to see them."})
	}
	for _, n := range notes {
		data := map[string]any{"incidentId": n.id}
		if n.id == "summary" {
			data = nil
		}
		_ = a.notify("phantomops-"+n.id+"-"+fmt.Sprint(time.Now().UnixNano()), n.title, n.body, data)
	}
}

func humanDuration(d time.Duration) string {
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

// --- bound methods --------------------------------------------------------

// LastResult returns the most recent scan result (zero ScannedAt if none),
// so the UI can show it without scanning again.
func (a *App) LastResult() engine.Result {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last
}

// GetMonitorStatus reports the background monitor's state.
func (a *App) GetMonitorStatus() MonitorStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	m := a.cfg.Monitoring
	return MonitorStatus{
		Enabled: m.Enabled, IntervalMinutes: int(m.Interval().Minutes()), LastScan: a.lastScan, NextScan: a.nextScan,
		Scanning: a.scanning, Notify: m.Notify, NotificationsError: a.notifyErr, HistoryError: a.historyErr,
	}
}

// SendTestNotification shows a sample notification.
func (a *App) SendTestNotification() error {
	if a.notify == nil {
		if a.notifyErr != "" {
			return fmt.Errorf("%s", a.notifyErr)
		}
		return fmt.Errorf("desktop notifications are not available")
	}
	return a.notify(fmt.Sprintf("phantomops-test-%d", time.Now().UnixNano()), "👻 PhantomOps notifications work",
		"You will be notified like this when a new incident is detected.", nil)
}

// GetHistory returns tracked incidents, open first.
func (a *App) GetHistory() []history.Record {
	if a.history == nil {
		return []history.Record{}
	}
	return a.history.List()
}

// ClearResolvedHistory deletes resolved incidents from the history.
func (a *App) ClearResolvedHistory() error {
	if a.history == nil {
		return nil
	}
	return a.history.ClearResolved()
}
