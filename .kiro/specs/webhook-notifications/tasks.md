# Tasks – webhook notifications

- [x] 1. Add `internal/notify` with Slack, Discord and generic senders and a whitelisted message builder
- [x] 2. Unit tests with `httptest.Server` (payloads, timeout, no redirects, no URL in errors)
- [x] 3. Config: `webhooks[]` with masked URL and 0600 file mode
- [x] 4. Hook into `monitor.go` after triage filtering, non-blocking
- [x] 5. Settings UI: add/remove channel and "Send test message"
- [x] 6. Show last delivery result per channel in Settings
- [ ] 7. Docs in README and release notes (release notes at the next release)
