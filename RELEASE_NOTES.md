# PhantomOps v0.5.1 👻

PhantomOps **v0.5.1** is a patch release. It fixes a **false "host key changed" warning** that stopped SSH connections to many ordinary servers, and **in-app updates on Windows** that failed with a permission error.

---

### 🔧 Fixes

#### Windows: updating from the installer failed with a permission error
- If you installed PhantomOps with the Windows installer and used the in-app update, the download finished but the installer never started. There was no Yes/No permission prompt, only an error saying it needs elevated privileges. The installer installs into Program Files, so it needs administrator rights, and the updater started it in a way that cannot ask for them.
- It now starts the installer the way Windows expects, so the usual permission prompt appears. If you answer No, or this account may not install programs, PhantomOps says that the installer was not started, instead of showing a Windows error code. PhantomOps stays open in that case.
- Once the installer has started, PhantomOps **closes itself after a few seconds**, because Windows will not let an installer replace a program that is running. When the installer finishes, start PhantomOps again from the Start menu or the desktop shortcut. (The "Restart now" button is not shown in this case, since it would reopen the old version.)

---

### 🧪 Internal
- The test SSH server can now offer several host key types, like a real server. New tests cover each known key type, an unknown server, a changed key while other types are offered, and the number of handshakes needed.
- I tested v0.5.0 against a real OpenSSH server, a real Prometheus and a real Loki before this release.
- New tests for how the updater starts the installer, how each Windows result is reported, and when PhantomOps closes itself (only after the installer started, and never for other update types). The Windows launch itself was not run on Windows for this release.

---

### 📦 Downloads
- **Linux**: `phantomops-linux-amd64.deb`, `phantomops-linux-amd64.rpm`, `phantomops-linux-amd64.tar.gz`
- **Windows**: `phantomops-windows-amd64-installer.exe`, `phantomops-windows-amd64.zip`
- **macOS**: `phantomops-darwin-universal.dmg`, `phantomops-darwin-universal.zip`
- `SHA256SUMS`: checksums for every file above

### 📄 License
PhantomOps is open source under the **Apache License 2.0**.
