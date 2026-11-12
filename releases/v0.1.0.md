# PhantomOps v0.1.0 👻

**Your infrastructure is haunted. Find out what's killing it.**

The first public release of PhantomOps: a desktop app that connects to your Docker, Kubernetes, Nomad, Prometheus, Loki and SSH hosts, correlates what it sees into incident timelines, explains the most likely root cause, and can roll back a bad deployment after you review and approve it.

---

### ✨ Highlights

#### 🔌 Connect your infrastructure
- **Six integrations**: Docker / Podman (containers, restarts, OOM kills, memory, healthchecks, error logs), Kubernetes incl. k3s (pods, events, rollouts, memory vs limits), Nomad (jobs, allocations, task failures, deployments), Prometheus (PromQL rules: memory growth, targets down, firing alerts, 5xx rates), Loki (error lines per service) and SSH hosts (load, memory, disk, failed units, kernel OOM, open ports; read-only allowlisted commands, host keys verified).
- **Connections tab**: add, test, edit, enable/disable and delete connections; **Detect local services** finds Docker, kubectl contexts and local Prometheus / Loki / Nomad.
- Settings are stored with owner-only permissions; secrets are masked in the UI.

#### 🧠 Correlation & root cause
- Events from all sources are grouped into incidents by time and service, and ordered into a timeline.
- Rule-based diagnosis with confidence and evidence: deployment regression, startup failure, memory exhaustion, dependency exhaustion, unhealthy service, crash loop and host pressure.

#### 🤖 Optional AI investigator
- Ollama (local) or any OpenAI-compatible API can double-check the diagnosis. Answers must cite real timeline events and may only name services present in your data; anything else is rejected.
- Off by default. A banner shows whether incident data stays local or would be sent to a remote endpoint.

#### 🛠 Reviewed rollbacks
- Roll a Kubernetes Deployment or Nomad service job back to its previous revision from the incident.
- Off by default. Every rollback shows a preview of exactly what will change, requires typing the workload name, uses a single-use approval that expires after 2 minutes, re-checks the target just before acting, and is refused for first-ever deploys, identical revisions and system namespaces.
- **Activity** tab and append-only audit log: the intent is recorded before anything changes.

#### ⏱ Monitoring, history & notifications
- Optional background scans every N minutes while the app is open.
- **History** tab: incidents are kept across scans and restarts, with first/last seen, duration and status. An incident resolves after two clean scans; partial scans never resolve anything.
- Desktop notifications for new, escalated, returning and resolved incidents (critical only by default).

#### 🔄 In-app updates
- PhantomOps checks GitHub Releases for new versions (can be turned off) and installs them from the app. Every download is verified against the release's `SHA256SUMS` before it is installed. Development builds are never replaced.

---

### 📦 Downloads
- **Linux**: `phantomops-linux-amd64.deb` (Debian, Ubuntu, Mint, Pop!_OS), `phantomops-linux-amd64.rpm` (Fedora, RHEL, openSUSE), `phantomops-linux-amd64.tar.gz` (portable)
- **Windows**: `phantomops-windows-amd64-installer.exe` (installer), `phantomops-windows-amd64.zip` (portable)
- **macOS**: `phantomops-darwin-universal.dmg`, `phantomops-darwin-universal.zip` (Intel & Apple Silicon)
- `SHA256SUMS`: checksums for every file above

### 📄 License
PhantomOps is open source under the **Apache License 2.0**.

### ⚠ Known limitations
- Linux and Windows builds are not code-signed; macOS builds are not notarized, so Gatekeeper will warn on first launch.
- Monitoring runs only while the app is open.
- Kubernetes rollbacks revert the pod template only (not ConfigMaps or Secrets).
