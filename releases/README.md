# 📦 PhantomOps releases & changelog archive

This directory keeps the release notes of every published version of PhantomOps.
The notes for the release currently being prepared live in the root [RELEASE_NOTES.md](../RELEASE_NOTES.md);
they become the body of the GitHub Release.

### How a release is made
1. Write the notes for the new version in `RELEASE_NOTES.md` (first line: `# PhantomOps vX.Y.Z ...`).
2. Run `./scripts/release.sh vX.Y.Z`. It bumps the version in `wails.json`, copies the notes to `releases/vX.Y.Z.md` and checks that the table below has a row for the version.
3. Open a PR with those changes and merge it.
4. Tag the merge commit: `git tag vX.Y.Z && git push origin vX.Y.Z`. The release workflow builds every platform, generates `SHA256SUMS` and publishes the GitHub Release (code checks already ran on the PR).

---

### 📋 Release history

| Version | Type | Highlights | Release notes |
| :--- | :--- | :--- | :---: |
| **v0.5.1** | *Current* · Patch release | Fixes a false "host key changed" warning that blocked SSH connections to servers with several host keys; fixes Windows in-app updates that failed with a permission error (the app now closes itself after the installer starts) | [v0.5.1.md](v0.5.1.md) |
| **v0.5.0** | Minor release | Kubernetes, Nomad, Prometheus and Loki through SSH (read-only); check and trust an SSH server's host key from the app; nothing is connected by default on a fresh install; report buttons on Incidents; webhook delivery history in Activity | [v0.5.0.md](v0.5.0.md) |
| **v0.4.0** | Minor release | Docker-over-SSH monitoring and dashboards; specialist Kiro agents; headless UI screenshot helper; release Discussion announcements and discussion forms | [v0.4.0.md](v0.4.0.md) |
| **v0.3.1** | Patch release | Export incidents as Markdown reports (History → Save/Copy report); no more "Connection not found" flash on dashboards; rollback previews keep the "exact changes unknown" warning; tests for engine, rollbacks and updater | [v0.3.1.md](v0.3.1.md) |
| **v0.3.0** | Minor release | Slack, Discord and generic JSON webhook notifications for incidents (Settings → Chat & webhooks), with masked URLs, per-channel severity, test message and delivery status | [v0.3.0.md](v0.3.0.md) |
| **v0.2.2** | Patch release | Real service logos for connections; Support & community card in Settings; community files (issue forms, contributing, security, sponsor); empty chart axis fix | [v0.2.2.md](v0.2.2.md) |
| **v0.2.1** | Patch release | Fixes Kubernetes failing in the installed app (kubeconfig lookup, new kubeconfig field); actionable errors for kubeconfig and Docker socket permission problems; collapsible Settings cards | [v0.2.1.md](v0.2.1.md) |
| **v0.2.0** | Minor release | Sidebar navigation with live connection status; Overview page; dashboards for Docker, Kubernetes, Nomad, Prometheus, Loki and SSH with charts; separate Settings page; Loki no longer counts its own query logs as errors | [v0.2.0.md](v0.2.0.md) |
| **v0.1.2** | Minor release | Acknowledge, snooze & mute incidents; custom title bar in a frameless window (native traffic lights on macOS); new PhantomOps app icon on every platform with full Linux icon set; NVIDIA/Mesa rendering fix for the portable build; consistent UTC times | [v0.1.2.md](v0.1.2.md) |
| **v0.1.1** | First published release | Docker, Kubernetes, Nomad, Prometheus, Loki and SSH collectors; correlation engine with rule-based root cause; Connections UI; optional AI investigator (Ollama / OpenAI-compatible); reviewed Kubernetes & Nomad rollbacks with audit log; background monitoring, incident history and desktop notifications; verified in-app updates; fixed Linux release build; PR checks; Apache 2.0 | [v0.1.1.md](v0.1.1.md) |
| **v0.1.0** | Tag only · not published | Planned first release; the Linux packaging step failed, so no release was published. Everything in it ships in v0.1.1 | [v0.1.0.md](v0.1.0.md) |
