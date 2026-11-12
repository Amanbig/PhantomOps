# PhantomOps v0.1.2 👻

PhantomOps **v0.1.2** adds **incident triage** (acknowledge, snooze and mute), a **custom title bar** in a frameless window, and a **new PhantomOps app icon** across every platform. It is also the first release delivered through the **in-app updater**: if you are on v0.1.1, open *Connections → About & updates* to install it.

---

### ✨ What's new

#### 🔕 Acknowledge, snooze and mute incidents
- **Acknowledge** an incident to show someone is on it ("✓ Acknowledged by you · 5 min ago", with Undo). Acknowledged incidents still notify you if they escalate.
- **Snooze** for 1, 4 or 24 hours: the incident is hidden and silent until the snooze ends, then returns on its own. *Show now* ends it early.
- **Mute** hides and silences every incident with the same title in the same environment until you unmute it. Muted incidents are listed in *History → Muted*, where you can unmute them.
- Snoozed and muted incidents are kept in a collapsed "hidden" list on the Incidents tab, so nothing disappears silently.
- If a resolved incident comes back, its acknowledgement and snooze are cleared: it is a new occurrence nobody has looked at yet.

#### 🪟 Custom title bar
- PhantomOps now draws its own title bar: logo, name, version and update badge on the left, the section tabs in the middle, and minimise / maximise / close on the right. Drag the bar to move the window; double-click it to maximise.
- Windows keeps its shadow, rounded corners and snap layouts. macOS keeps its native traffic-light buttons.

#### 👻 New app icon
- A PhantomOps ghost replaces the default Wails logo in the window, task bar, Windows executable, macOS app and Linux menus.
- Linux packages install the icon at every standard size (16 to 512 px), with a simplified design at small sizes so it stays legible.

---

### 🔧 Fixes & improvements
- Fixed blank or failing rendering on some NVIDIA/Mesa Linux setups ("KMS: DRM_IOCTL_MODE_CREATE_DUMB failed: Permission denied") for the portable `.tar.gz` build; the `.deb`/`.rpm` already included the workaround.
- All times in the app are now shown in UTC, consistently.
- The incident history file is upgraded automatically; history from v0.1.1 is kept.
- The bundled Wails JavaScript runtime matches the Wails version the app is built with.

---

### 📦 Downloads
- **Linux**: `phantomops-linux-amd64.deb`, `phantomops-linux-amd64.rpm`, `phantomops-linux-amd64.tar.gz`
- **Windows**: `phantomops-windows-amd64-installer.exe`, `phantomops-windows-amd64.zip`
- **macOS**: `phantomops-darwin-universal.dmg`, `phantomops-darwin-universal.zip`
- `SHA256SUMS`: checksums for every file above

### 📄 License
PhantomOps is open source under the **Apache License 2.0**.
