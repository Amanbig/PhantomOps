# PhantomOps v0.5.0 👻

PhantomOps **v0.5.0** takes monitoring beyond the machine you're on. **Kubernetes, Nomad, Prometheus and Loki can now be read through an SSH connection** (Docker already could), you can **trust a new server's SSH key from the app**, and **a fresh install no longer connects to anything by itself**.

---

### ⚠️ Changed

#### Nothing is connected by default
- A fresh install used to switch on a "Local Docker" and a "Kubernetes (current context)" connection by itself whenever it found a Docker socket or `kubectl`. It now starts empty. Use **Detect local services** (or **Add connection**) to choose what to connect.
- **If you already saved your settings, nothing changes for you.**
- `phantom-scan` now says so when there is nothing configured, instead of quietly scanning nothing.

---

### ✨ New

#### Kubernetes, Nomad, Prometheus and Loki through SSH
- On a Kubernetes, Nomad, Prometheus or Loki connection, pick **Run through SSH connection** and choose a saved SSH connection. Everything is read-only and goes over the verified SSH host key.
- **Kubernetes** runs a fixed list of 8 read-only `kubectl` calls on that server (set **Remote kubeconfig file** if it isn't `~/.kube/config`). Anything else is refused before it leaves your computer.
- **Nomad, Prometheus and Loki** are reached through a read-only tunnel that only sends `GET` and `HEAD`. The URL is the address *as seen from that server*, for example `http://127.0.0.1:4646`. Nothing opens on your computer. The Nomad ACL token still works, inside the tunnel.
- All services on one server share a single SSH connection. Dashboards used to open a new SSH handshake on every refresh for Docker; that is fixed too.
- Clear messages for the usual problems: `kubectl` or `docker` missing from the remote PATH, a root-only k3s kubeconfig, no permission to use Docker, and an SSH server that doesn't allow port forwarding.
- **Monitoring only.** Rollbacks are never performed through SSH, and a connection set to use SSH never falls back to connecting directly.

#### Trust a new SSH server from the app
- The SSH connection form has **Check host key**. It reads the server's key without logging in (no credential is sent) and shows the fingerprint. **Trust this key** then adds it to `known_hosts`.
- Only the exact fingerprint you were shown is saved, and the action is written to the audit log first.
- A key that **changed** is never replaced by the app. You get a warning, because it can mean the connection is being intercepted, and connections to that server stay refused.
- Fixed along the way: `~/.ssh/known_hosts` (the form's own example) was not expanded, and a missing `known_hosts` file was treated as an error instead of "nothing trusted yet".

#### Incident reports on the Incidents page
- **Save report…** and **Copy report** are now on the live incident card too, not only in History.

#### Webhook delivery history
- **Activity → Webhook deliveries** lists the most recent messages sent to Slack, Discord and generic channels: when, to which channel, and whether each was delivered, failed (with the reason) or not sent after an earlier failure.
- It is kept in memory, so it starts empty each time the app starts, and it never shows webhook addresses.

---

### 🔧 Fixes
- Docker-over-SSH settings are now part of the generated app bindings (v0.4.0 left them out).

---

### 🧪 Internal
- The SSH code is now tested against an in-process SSH server (real key exchange, login, commands and port forwarding), not just fakes.
- New tests for the tunnel, the allowlists, the host-key rules and the delivery log. Each important rule was checked by deliberately breaking it to confirm a test fails.

---

### 📦 Downloads
- **Linux**: `phantomops-linux-amd64.deb`, `phantomops-linux-amd64.rpm`, `phantomops-linux-amd64.tar.gz`
- **Windows**: `phantomops-windows-amd64-installer.exe`, `phantomops-windows-amd64.zip`
- **macOS**: `phantomops-darwin-universal.dmg`, `phantomops-darwin-universal.zip`
- `SHA256SUMS`: checksums for every file above

### 📄 License
PhantomOps is open source under the **Apache License 2.0**.
