# PhantomOps v0.2.0 👻

PhantomOps **v0.2.0** turns PhantomOps from an incident list into a full monitoring console: a **sidebar** with live connection status, a **dashboard for every connected service** (Docker, Kubernetes, Nomad, Prometheus, Loki and SSH), and a new **Overview** that shows what is wrong and where at a glance.

---

### ✨ What's new

#### 🧭 Sidebar navigation
- Navigation moves from the title bar to a sidebar: **Overview**, **Incidents** (with a live count of open incidents, red when any is critical), **History** and **Activity**.
- Every connection is listed with a **live status dot**: green healthy, red failing (the error is in the tooltip), hollow disabled. Add a connection with **+**.
- **Settings** has its own page: AI investigator, monitoring, remediation, analysis and updates. The Connections page now only manages connections.
- On narrow windows the sidebar shrinks to icons.

#### 📊 Overview
- Open incidents by severity, connections healthy, incidents resolved in the last 24 hours with the average time to resolve, and triage counts.
- **Needs attention**: the open incidents, critical first, one click away.
- **Incidents, last 24 h**: new incidents per hour, by severity.
- A card per connection with its health and open-incident count, and the **noisiest services** of the day.

#### 🖥 A dashboard for every connection
Click a connection in the sidebar to see its live state (read-only), its open incidents, and a fix-it path when it is failing.
- **Docker**: running, unhealthy and failed containers; per-container CPU, memory against its limit, health, restarts, exit codes and ports; filter, search and sort.
- **Kubernetes**: node readiness with CPU and memory use (via metrics-server), deployment readiness, problem pods with the reason (CrashLoopBackOff, ImagePullBackOff, OOMKilled, …) and the last hour's warnings.
- **Nomad**: nodes, jobs with running / failed / queued allocations, failing or restarting allocations with their last event, failed deployments.
- **Prometheus**: scrape targets and their errors, firing and pending alerts, and charts for targets up, firing alerts and ingestion rate.
- **Loki**: log volume and error lines per service, error rate, an errors-per-service chart and the latest error lines.
- **SSH hosts**: OS, uptime, load against CPU count, memory, swap, disks, failed systemd units, expected ports and the busiest processes (fixed, read-only commands only).

---

### 🔧 Fixes & improvements
- **Loki no longer counts PhantomOps' own queries as errors.** Loki logs every query it runs, and those queries contain words like "error", so they were being reported as error lines. Both incidents and the dashboard now ignore them.
- Docker containers without a memory limit are no longer shown as using a percentage of the host's RAM.
- Connection cards show health from the last scan without needing a manual test.

---

### 📦 Downloads
- **Linux**: `phantomops-linux-amd64.deb`, `phantomops-linux-amd64.rpm`, `phantomops-linux-amd64.tar.gz`
- **Windows**: `phantomops-windows-amd64-installer.exe`, `phantomops-windows-amd64.zip`
- **macOS**: `phantomops-darwin-universal.dmg`, `phantomops-darwin-universal.zip`
- `SHA256SUMS`: checksums for every file above

### 📄 License
PhantomOps is open source under the **Apache License 2.0**.
