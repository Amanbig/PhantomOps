# PhantomOps v0.5.5 👻

PhantomOps **v0.5.5** is a patch release with one important fix: on a **fresh install, none of the "Add" buttons worked**.

---

### 🔧 Fixes

#### Could not add a connection on a fresh install
- When nothing was connected yet (the default since v0.5.0), clicking **Add connection**, **Add** or **Add all** (under **Detect local services**) did nothing. There was no error on screen, so it looked like the buttons were dead. **Test connection** still worked, which made it more confusing.
- Cause: with no connections, the app's backend sent an empty list as `null`, and the screen failed when it tried to add to it. The backend now sends an empty list, and the screen also copes with a missing list.
- If you already had at least one connection, you were not affected. If you hit this, update and add your connection again. Nothing else is needed.

---

### 🧪 Internal
- New test: the settings sent to the screen never contain `null` for the connections, chat channels or ignore lists. It failed before the fix and passes now.
- Checked in a headless browser with an empty settings file: Add connection (Docker and Kubernetes), Add, and Add all each save the connection.
- **Not tested:** the fix in the installed app on Windows and macOS. The screen-side check is a script and is not part of the automated tests.

---

### 📦 Downloads
- **Linux**: `phantomops-linux-amd64.deb`, `phantomops-linux-amd64.rpm`, `phantomops-linux-amd64.tar.gz`
- **Windows**: `phantomops-windows-amd64-installer.exe`, `phantomops-windows-amd64.zip`
- **macOS**: `phantomops-darwin-universal.dmg`, `phantomops-darwin-universal.zip`
- `SHA256SUMS`: checksums for every file above

### 📄 License
PhantomOps is open source under the **Apache License 2.0**.
