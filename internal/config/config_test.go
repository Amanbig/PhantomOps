package config

import (
	"strings"
	"testing"

	"PhantomOps/internal/notify"
)

const hookURL = "https://hooks.slack.com/services/T000/B000/secretvalue"

func hookConfig() Config {
	c := Config{Webhooks: []notify.Channel{{ID: "h1", Type: notify.TypeSlack, Name: "ops", Enabled: true, URL: hookURL}}}
	c.Normalize()
	return c
}

func TestWebhookURLIsMaskedForTheUIAndRestoredOnSave(t *testing.T) {
	stored := hookConfig()
	ui := stored.Redacted()
	if ui.Webhooks[0].URL != SecretMask {
		t.Fatalf("UI must see a mask, got %q", ui.Webhooks[0].URL)
	}
	if stored.Webhooks[0].URL != hookURL {
		t.Fatal("Redacted must not modify the original")
	}
	// UI sends the mask back unchanged.
	ui.RestoreSecrets(stored)
	if ui.Webhooks[0].URL != hookURL {
		t.Errorf("masked URL not restored: %q", ui.Webhooks[0].URL)
	}
	// A mask with no matching stored channel is cleared, then rejected.
	orphan := Config{Webhooks: []notify.Channel{{ID: "nope", Type: notify.TypeSlack, Name: "x", URL: SecretMask}}}
	orphan.RestoreSecrets(stored)
	if orphan.Webhooks[0].URL != "" || orphan.Validate() == nil {
		t.Errorf("orphan mask should be cleared and invalid: %+v", orphan.Webhooks[0])
	}
}

func TestWebhookValidationAndDefaults(t *testing.T) {
	c := Config{Webhooks: []notify.Channel{{Type: notify.TypeGeneric, Name: " a ", URL: " https://example.com/h "}}}
	c.Normalize()
	w := c.Webhooks[0]
	if w.ID == "" || w.Name != "a" || w.URL != "https://example.com/h" || w.MinSeverity != "critical" {
		t.Errorf("normalize = %+v", w)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
	c.Webhooks = append(c.Webhooks, notify.Channel{ID: "2", Type: notify.TypeDiscord, Name: "A", URL: "http://discord.com/x"})
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "same name") && !strings.Contains(err.Error(), "already uses this name") {
		t.Errorf("duplicate name not caught: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("http discord URL not caught: %v", err)
	}
}
