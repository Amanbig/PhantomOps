# PhantomOps v0.4.0 👻

PhantomOps **v0.4.0** adds Docker-over-SSH, so a Docker daemon on another Linux host can be monitored without manually creating a tunnel.

---

### ✨ New

#### Docker-over-SSH
- Add and save an SSH connection first, then select it from a Docker connection's **Run through SSH connection** field.
- PhantomOps runs fixed, read-only Docker CLI commands on the remote host: `info`, `ps`, `inspect`, `stats` and `logs`.
- The existing SSH key or agent authentication and `known_hosts` verification are reused.
- Docker collection and Docker dashboards work through the remote connection.
- An optional remote Docker socket path can be set; the default is `/var/run/docker.sock`.
- Container IDs are validated and commands are shell-quoted. No Docker mutation commands are generated.

#### Project tooling
- Added six workspace specialist agents under `.kiro/agents/` for platform work, collectors, UI, tests, security review and releases.
- Added `dev/ui/shot.py`, a headless screenshot helper with a stand-in Wails backend.
- Releases can announce themselves in GitHub Discussions, and Ideas/Q&A discussion forms are included.

---

### 🔒 Safety
- Docker-over-SSH requires a saved SSH connection and keeps host-key verification enabled by default.
- The remote path uses a fixed read-only command set. It does not accept arbitrary commands from the UI.
- Real SSH and remote Docker testing is still environment-dependent. The release was tested with fake command runners and headless UI fixtures.

---

### 📦 Downloads
- **Linux**: `phantomops-linux-amd64.deb`, `phantomops-linux-amd64.rpm`, `phantomops-linux-amd64.tar.gz`
- **Windows**: `phantomops-windows-amd64-installer.exe`, `phantomops-windows-amd64.zip`
- **macOS**: `phantomops-darwin-universal.dmg`, `phantomops-darwin-universal.zip`
- `SHA256SUMS`: checksums for every file above

### 📄 License
PhantomOps is open source under the **Apache License 2.0**.
