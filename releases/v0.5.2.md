# PhantomOps v0.5.2 👻

PhantomOps **v0.5.2** is a patch release. It ships with a **patched Go toolchain** (the v0.5.1 builds used Go 1.25.0, which has known security fixes available), fixes **incidents that wrongly merged unrelated services**, and makes **Docker over SSH about four times faster**.

---

### 🔒 Security
- Release builds now use the latest Go 1.26 patch release. A scan of the v0.5.1 build found 38 known issues in Go's standard library and 2 in `golang.org/x/crypto` (two denial-of-service bugs in its SSH code). The scan of this build reports none.
- Updated `golang.org/x/crypto` to v0.57.0 (this needs Go 1.26, which is why `go.mod` now says `go 1.26.0`).
- Pull requests now run a **known-vulnerabilities check** (`govulncheck`), so this cannot go unnoticed again.

### 🔧 Fixes

#### Services that share a name prefix were merged into one incident
- Services from the same Compose project (for example `shop-db`, `shop-api`, `shop-ok`) could end up in a single incident, and error log lines from a healthy service could be attached to a broken one. Two events are now related only when one subject's name is part of the other's, such as `api` and `api-7d9f-xk2`.
- The recorded scenario in `dev/scenarios` produces the same output as before.
- Known limit: a service whose only symptom is error logs still attaches to the most recent unrelated failure, not to its own incident.

### ⚡ Speed
- **Docker over SSH** now inspects up to 6 containers at the same time. On a real host with 7 containers a scan went from **9.2 s to 2.2 s**, with the same 12 events as a direct connection.

### 🧪 Internal
- Removed unused code found by `staticcheck`.
- Tested against real services before this release: Docker, a real k3s cluster (collector, dashboard, rollback plan and execute, protected-namespace and single-revision refusals), Docker and Kubernetes through a real SSH server (event lists identical to direct connections), Nomad (collector, scan, rollback plan and execute, and through SSH, where the event count matched a direct connection) and the `phantom-scan` and `phantom-collect` tools.
- **Not tested:** the Windows installer flow, macOS and Windows packages, and passphrase-protected SSH keys.

---

### 📦 Downloads
- **Linux**: `phantomops-linux-amd64.deb`, `phantomops-linux-amd64.rpm`, `phantomops-linux-amd64.tar.gz`
- **Windows**: `phantomops-windows-amd64-installer.exe`, `phantomops-windows-amd64.zip`
- **macOS**: `phantomops-darwin-universal.dmg`, `phantomops-darwin-universal.zip`
- `SHA256SUMS`: checksums for every file above

### 📄 License
PhantomOps is open source under the **Apache License 2.0**.
