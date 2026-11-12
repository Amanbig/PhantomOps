package main

import (
	"errors"
	"time"

	"PhantomOps/internal/history"
)

// Triage actions on incidents. They only change PhantomOps' own records
// (acknowledgements, snoozes, mute rules); nothing in the infrastructure.

var errNoHistory = errors.New("incident history is unavailable")

// refreshTriage re-applies triage state to the last scan result so the UI can
// re-render it without scanning again.
func (a *App) refreshTriage() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.history.Annotate(a.last.Incidents)
}

func (a *App) triage(fn func(*history.Store) error) error {
	if a.history == nil {
		return errNoHistory
	}
	if err := fn(a.history); err != nil {
		return err
	}
	a.refreshTriage()
	return nil
}

// AcknowledgeIncident marks an incident as being handled by the current user.
func (a *App) AcknowledgeIncident(id string) error {
	return a.triage(func(h *history.Store) error { return h.Acknowledge(id, currentUser(), true) })
}

// UnacknowledgeIncident clears the acknowledgement.
func (a *App) UnacknowledgeIncident(id string) error {
	return a.triage(func(h *history.Store) error { return h.Acknowledge(id, "", false) })
}

// SnoozeIncident hides an incident and silences its notifications for the
// given number of minutes (1 minute to 7 days).
func (a *App) SnoozeIncident(id string, minutes int) error {
	if minutes < 1 {
		return errors.New("snooze for at least one minute")
	}
	return a.triage(func(h *history.Store) error { return h.Snooze(id, time.Duration(minutes)*time.Minute) })
}

// UnsnoozeIncident ends a snooze early.
func (a *App) UnsnoozeIncident(id string) error {
	return a.triage(func(h *history.Store) error { return h.Snooze(id, 0) })
}

// MuteIncident hides and silences every incident with the same title in the
// same environment until it is unmuted.
func (a *App) MuteIncident(id string) (history.MuteRule, error) {
	var rule history.MuteRule
	err := a.triage(func(h *history.Store) error {
		var err error
		rule, err = h.Mute(id, currentUser())
		return err
	})
	return rule, err
}

// UnmuteIncident removes a mute rule by its key.
func (a *App) UnmuteIncident(key string) error {
	return a.triage(func(h *history.Store) error { return h.Unmute(key) })
}

// MuteRules lists the active mute rules.
func (a *App) MuteRules() []history.MuteRule {
	if a.history == nil {
		return []history.MuteRule{}
	}
	return a.history.MuteRules()
}
