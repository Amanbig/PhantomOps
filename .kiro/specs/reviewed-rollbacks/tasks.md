# Tasks – reviewed rollbacks

- [x] 1. Add `rollbacks_enabled` config flag (default off)
- [x] 2. Build rollback plans for Kubernetes deployments and Nomad jobs in `internal/remediate`
- [x] 3. Append every executed/rejected/failed action to `audit.log`
- [x] 4. Wails bindings to propose and execute a plan with typed confirmation
- [x] 5. Review dialog in `frontend/src/views/rollback.js`
- [ ] 6. Committed unit tests for plan building and confirmation validation
