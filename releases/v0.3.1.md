# PhantomOps v0.3.1 👻

PhantomOps **v0.3.1** is a small patch release: you can **export an incident as a Markdown report**, dashboards no longer flash "Connection not found" while loading, and rollback previews now warn you when they cannot show the exact changes.

---

### ✨ New

#### Incident reports
- In **History**, open an incident and use **Save report…** or **Copy report** to get a Markdown postmortem: summary, root cause and confidence, evidence, recommended action, a UTC timeline, and any rollback actions taken.
- Reports leave out log excerpts and remove common secrets (private keys, webhook URLs, passwords and tokens). This is best effort, so read a report before you share it.

---

### 🔧 Fixes
- **Dashboards:** opening a connection's dashboard showed "Connection not found" for a moment while loading. It now shows a spinner, and only says "not found" if the connection is really gone. If the settings cannot be read, it says so instead.
- **Rollback previews:** when PhantomOps could not compare the current and previous versions, the warning that the exact changes were unknown was dropped. It is shown now.
- Scans with nothing to report now return empty lists instead of nothing.

---

### 🧪 Internal
- Added tests for the correlation engine, rollbacks, and the in-app updater (including refusing tampered downloads).

---

### 📦 Downloads
- **Linux**: `phantomops-linux-amd64.deb`, `phantomops-linux-amd64.rpm`, `phantomops-linux-amd64.tar.gz`
- **Windows**: `phantomops-windows-amd64-installer.exe`, `phantomops-windows-amd64.zip`
- **macOS**: `phantomops-darwin-universal.dmg`, `phantomops-darwin-universal.zip`
- `SHA256SUMS`: checksums for every file above

### 📄 License
PhantomOps is open source under the **Apache License 2.0**.
