# PhantomOps v0.5.1 👻

PhantomOps **v0.5.1** is a patch release. It fixes a **false "host key changed" warning** that stopped SSH connections to many ordinary servers, and **in-app updates on Windows** that failed with a permission error.

---

### 🔧 Fixes

#### Windows: updating from the installer failed with a permission error
- If you installed PhantomOps with the Windows installer and used the in-app update, the download finished but the installer never started. There was no Yes/No permission prompt, only an error saying it needs elevated privileges. The installer installs into Program Files, so it needs administrator rights, and the updater started it in a way that cannot ask for them.
- It now starts the installer the way Windows expects, so the usual permission prompt appears. If you answer No, or this account may not install programs, PhantomOps says that the installer was not started, instead of showing a Windows error code.
- **Known limitation:** the installer does not close a running PhantomOps. If it reports that it cannot write `PhantomOps.exe`, close PhantomOps and choose Retry.

#### SSH: a false "the host key CHANGED" warning
- A server has several host keys (ed25519, ECDSA and RSA), and the client and server agree on one during the handshake. If your `known_hosts` file held only the ed25519 key, which is what the normal `ssh` command stores, PhantomOps could be shown the server's ECDSA key instead. It then reported that the key had **changed** and might be intercepted, and refused to connect, although nothing had changed and `ssh` accepted the same file.
- It now behaves like OpenSSH. For a server it does not know yet, it offers the ed25519 key first, so the key you compare is the one you expect. For a server it knows, it asks for a key type your `known_hosts` already has.
- A key of the **same type that really differs** is still reported as changed and still refused, and PhantomOps never accepts a key type it has no entry for. If a server stops offering the key type you trusted, the message now says so.
- The host-key panel now names the key file to compare for the key it shows (ed25519, ecdsa or rsa), where it used to name the ed25519 file every time.
- This also affected the SSH host check from earlier versions, with a plainer message.

---

### 🧪 Internal
- The test SSH server can now offer several host key types, like a real server. New tests cover each known key type, an unknown server, a changed key while other types are offered, and the number of handshakes needed.
- I tested v0.5.0 against a real OpenSSH server, a real Prometheus and a real Loki before this release.
- New tests for how the updater starts the installer and how each Windows result is reported. The Windows call itself was not run on Windows for this release.

---

### 📦 Downloads
- **Linux**: `phantomops-linux-amd64.deb`, `phantomops-linux-amd64.rpm`, `phantomops-linux-amd64.tar.gz`
- **Windows**: `phantomops-windows-amd64-installer.exe`, `phantomops-windows-amd64.zip`
- **macOS**: `phantomops-darwin-universal.dmg`, `phantomops-darwin-universal.zip`
- `SHA256SUMS`: checksums for every file above

### 📄 License
PhantomOps is open source under the **Apache License 2.0**.
