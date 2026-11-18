# PhantomOps v0.5.4 👻

PhantomOps **v0.5.4** is a patch release. SSH connections now work with **passphrase-protected private keys**, and **Docker over SSH no longer misses error logs** that containers write to stderr.

---

### ✨ New

#### Passphrase-protected SSH keys
- An SSH connection has a new **Key passphrase** field. Before this, an encrypted key failed with a vague message, and the only way out was ssh-agent.
- Works with ed25519, ECDSA and RSA keys, in both the new OpenSSH format and the older PEM format.
- The passphrase is stored in the config file, which is readable only by you (0600), the same way Nomad tokens and AI keys are. It is shown as `********` in the app, and saving a connection without retyping it keeps the stored one. If you would rather not store it, leave the key path empty and add the key to ssh-agent.
- Clear errors: "the key is protected by a passphrase" when it is missing, and "the passphrase for key ... is wrong" when it does not fit. Errors never include the passphrase.
- Every connection type that runs through SSH (Docker, Kubernetes, Nomad, Prometheus, Loki) uses the same login, so they work with an encrypted key too.
- The `phantom-collect` tool has a matching `-ssh-passphrase` option. It defaults to the `PHANTOM_SSH_PASSPHRASE` environment variable, so the secret does not have to appear on the command line.

### 🔧 Fixes

#### Docker over SSH missed error logs written to stderr
- Docker over SSH only read what a container wrote to **stdout**. Most programs log errors to **stderr**, so their "N error log line(s)" signal never showed up through SSH. Direct Docker connections were not affected.
- Both streams are read now. On a real host, Docker through SSH and a direct connection report the same events.

---

### 🧪 Internal
- New tests for the parallel Docker collection added in v0.5.2: the limit of 6 at a time, event order, one broken container not hiding the others, and cancellation. New tests for stderr logs and for encrypted keys (right, wrong and missing passphrase, a plain key with a spare passphrase, masking and restoring in the config).
- Tested against real services before this release:
  - A real OpenSSH server with ed25519, RSA (both formats) and ECDSA keys, each with the right, a wrong and no passphrase, and with ssh-agent.
  - Docker, Kubernetes, Nomad, Prometheus and Loki through one SSH host with an encrypted key. The event counts matched a direct connection for each (Docker after the stderr fix), and every dashboard snapshot loaded.
- **Not tested:** the Settings form with a passphrase key in the real app window (only headless), Docker over SSH with TTY containers, the real app on Windows and macOS.

---

### 📦 Downloads
- **Linux**: `phantomops-linux-amd64.deb`, `phantomops-linux-amd64.rpm`, `phantomops-linux-amd64.tar.gz`
- **Windows**: `phantomops-windows-amd64-installer.exe`, `phantomops-windows-amd64.zip`
- **macOS**: `phantomops-darwin-universal.dmg`, `phantomops-darwin-universal.zip`
- `SHA256SUMS`: checksums for every file above

### 📄 License
PhantomOps is open source under the **Apache License 2.0**.
