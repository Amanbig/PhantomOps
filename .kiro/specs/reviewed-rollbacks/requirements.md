# Requirements – reviewed rollbacks

## Introduction
When PhantomOps finds the likely cause of an incident (e.g. a bad Kubernetes deployment or Nomad job version), it can offer a rollback. It must never act without explicit, informed human approval.

## Requirements

### 1. Opt-in
**User story:** As an operator, I want rollbacks disabled by default so nothing changes my infrastructure unexpectedly.
- WHEN the app is first installed THEN rollbacks SHALL be disabled.
- WHEN rollbacks are disabled THEN no rollback action SHALL be offered or executable.

### 2. Review before action
**User story:** As an operator, I want to see exactly what will run before approving.
- WHEN a rollback is proposed THEN the UI SHALL show the target, the exact command/change and the previous version.
- WHEN the user approves THEN they SHALL type a confirmation phrase before execution.

### 3. Audit
**User story:** As an operator, I want a record of every action.
- WHEN a rollback is executed, rejected or fails THEN an entry SHALL be appended to `audit.log` with time, target and outcome.

### 4. Supported targets
- Kubernetes (deployment rollout undo) and Nomad (job revert) SHALL be supported.
