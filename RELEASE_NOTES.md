# PhantomOps v0.3.0 👻

PhantomOps **v0.3.0** adds **chat notifications**: send incident updates to Slack, Discord or any JSON endpoint while PhantomOps monitors in the background.

---

### ✨ New

#### Chat & webhooks
- New **Chat & webhooks** card in Settings. Add a **Slack**, **Discord** or **Generic JSON** channel, pick the minimum severity for each, and use **Send test message** to check it works.
- While background monitoring runs, new, escalated, returning and resolved incidents are posted to every enabled channel. Muted and snoozed incidents stay silent.
- Messages contain only the incident title, severity and root-cause summary. No logs, environment values or credentials. Discord messages cannot ping `@everyone`.
- Each channel shows the result of its last delivery, including the error if it failed.
- **Nothing is sent unless you add a channel.**

#### Privacy and safety
- A webhook URL is a secret: it is hidden in the app, stored in the owner-only config file, and never appears in error messages.
- Slack and Discord webhooks must use https. Redirects are not followed, each delivery times out after 10 seconds, and a failing endpoint is not retried within the same scan.

---

### 🔧 Internal
- New unit tests for the sender, the config handling of webhook URLs, and the filtering of notifications.

---

### 📦 Downloads
- **Linux**: `phantomops-linux-amd64.deb`, `phantomops-linux-amd64.rpm`, `phantomops-linux-amd64.tar.gz`
- **Windows**: `phantomops-windows-amd64-installer.exe`, `phantomops-windows-amd64.zip`
- **macOS**: `phantomops-darwin-universal.dmg`, `phantomops-darwin-universal.zip`
- `SHA256SUMS`: checksums for every file above

### 📄 License
PhantomOps is open source under the **Apache License 2.0**.
