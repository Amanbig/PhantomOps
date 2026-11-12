# PhantomOps – layout

- `internal/{model,engine,config,llm,remediate,history,kubeconfig,updater,version,httpx}` – core logic.
- `internal/collector/{docker,kubernetes,nomad,prometheus,loki,ssh}` – one package per source, implementing the `Collector` interface; optional `collector.Snapshotter` powers dashboards.
- `cmd/phantom-collect`, `cmd/phantom-scan` – CLIs.
- Root: `app.go monitor.go triage.go dashboards.go updates.go main.go` – Wails bindings.
- `frontend/src/` – `main.js titlebar.js sidebar.js charts.js logos.js util.js`, `views/*.js`, `assets/connections/*` (service logos).
- `build/linux/` packaging (nfpm, desktop file, icons); `scripts/release.sh`, `scripts/icons.py`.
- `dev/` – docker-compose and manifests for local test infra.
- `.github/workflows/checks.yml` (PRs only) and `release.yml` (tags only; builds and publishes).
- `releases/` – archived release notes; `RELEASE_NOTES.md` – the next release.
