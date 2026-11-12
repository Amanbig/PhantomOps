# Design – reviewed rollbacks

## Components
- `internal/remediate` – builds a `Plan` (target, command, previous version) from an incident, and executes it for Kubernetes and Nomad. Also appends to the audit log.
- `app.go` – Wails bindings: propose plan, execute plan (requires the typed confirmation string and the enabled setting).
- `frontend/src/views/rollback.js` – review dialog showing the plan and the confirmation input.
- `internal/config` – `rollbacks_enabled` flag, default `false`.

## Flow
1. Engine marks an incident with a rollback candidate.
2. UI requests a plan; backend returns it without side effects.
3. User reads the plan, types the confirmation phrase.
4. Backend re-validates the setting and phrase, executes, writes the audit entry, returns the outcome.

## Error handling
- Missing previous version → no plan offered.
- Command failure → outcome recorded in audit log and shown; nothing retried automatically.

## Testing
- Unit tests for plan building and confirmation validation; manual check against the dev k3s / Nomad stack in `dev/`.
