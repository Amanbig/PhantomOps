# Tasks – webhook notifications

- [ ] 1. Add `internal/notify` with Slack, Discord and generic senders and a whitelisted message builder
- [ ] 2. Unit tests with `httptest.Server` (payloads, timeout, no secrets in message)
- [ ] 3. Config: `notifications.channels[]` with masked URL and 0600 file mode
- [ ] 4. Hook into `monitor.go` after triage filtering, non-blocking
- [ ] 5. Settings UI: add/remove channel and "Send test message"
- [ ] 6. Show send failures in Activity
- [ ] 7. Docs in README and release notes
