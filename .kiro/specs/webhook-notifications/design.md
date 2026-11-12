# Design – webhook notifications

## Components
- `internal/notify` (new): `Channel` interface, `Slack`, `Discord`, `Generic` senders using `internal/httpx` with a short timeout; message builder that whitelists fields.
- `internal/config`: `notifications.channels[]` (type, name, url – masked).
- `monitor.go`: after a new incident passes triage filters, fan out to channels in a goroutine.
- `frontend/src/views/settings` area (Settings page): channel list, add/remove, test button.

## Flow
1. Monitor detects a new incident.
2. Triage check (ack/snooze/mute) → skip if suppressed.
3. Build a message from whitelisted fields only.
4. Send to each channel with timeout; record result in Activity.

## Testing
- Unit tests with `httptest.Server` for each payload shape, timeout behaviour and secret-free messages.
