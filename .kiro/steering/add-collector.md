# Adding a collector

Each source lives in `internal/collector/<name>/` and implements `collector.Collector` (`internal/collector/collector.go`):
- `Name() string`
- `Collect(ctx, since) ([]model.Event, error)` – oldest first, must respect ctx cancellation.
- Optionally `Snapshotter.Snapshot(ctx) (any, error)` for the per-connection dashboard. Read-only.

## Checklist
1. Package with `client.go` (talks to the source), `<name>.go` (Collector), `snapshot.go` (optional). Use `internal/httpx` for HTTP.
2. Read-only only. No mutating calls. SSH-style sources must use an allowlist.
3. Add the type to `internal/config` and register it where collectors are built (see `app.go`).
4. Frontend: logo in `frontend/src/assets/connections/` + `logos.js`; entry in `TYPES` in `views/connections.js`; a dashboard in `views/dashboard.js` if it has a Snapshot.
5. Mask any secret field in the UI; config stays 0600.
6. Add a unit test next to the package (see `docker/client_test.go`) and, if useful, a service in `dev/`.
7. Run the verify commands in `tech.md`.
