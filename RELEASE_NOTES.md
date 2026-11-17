# PhantomOps v0.5.3 👻

PhantomOps **v0.5.3** is a patch release with screen and layout fixes. **Settings is now grouped into sections**, the app **no longer says "all clear" when connections failed to scan**, and several layout problems are fixed.

---

### ✨ Improvements

#### Settings is easier to find your way around
- Cards are grouped under **Scanning** (Analysis, Monitoring), **Alerts** (Chat & webhooks), **Advanced** (AI investigator, Remediation) and **App** (About & updates, Support). Four buttons at the top jump to each group.
- Analysis, the most common settings, now opens first. AI used to be first, although it is off by default.
- A card now shows **"Not saved yet"** once you change something in it. This includes "Allow rollbacks", which does nothing until you press Save in that card.
- Saving one card used to throw away unsaved changes in the other cards. They are now kept.
- Card titles use line icons that match the sidebar instead of emoji.

### 🔧 Fixes

#### "All clear" shown when connections failed
- If a scan could not read a connection (for example Docker permission denied, or Kubernetes refusing connections), Overview still showed a green check, "Nothing needs attention" and "all clear", and Incidents said "Suspiciously quiet… Phantom is watching". They now say that no incidents were found but some connections could not be read, so the result may be incomplete.

#### Layout
- **Narrow window:** the "+" button to add a connection disappeared when the sidebar shrank to icons. It stays now.
- **Short window:** the lower sidebar entries were squashed on top of "Settings". The connection list now scrolls by itself and Settings stays at the bottom.
- **Docker dashboard:** image names and ports were broken in the middle of a word (`demo/ap i:1.4`, `8080->8 0/tcp`). They now stay on one line.
- **Keyboard:** a "Skip to content" link is the first Tab stop, so you do not have to tab through the whole sidebar.

---

### 🧪 Internal
- Checked with a headless browser against a stand-in backend: Settings in wide and narrow windows, the unsaved-changes marker, the jump buttons, empty and failed-scan screens, the skip link, and a basic accessibility check (button names, input labels, image alt text, focus rings).
- **Not tested:** the real app on Windows and macOS, and real screen-reader use. No backend (Go) code changed in this release.

---

### 📦 Downloads
- **Linux**: `phantomops-linux-amd64.deb`, `phantomops-linux-amd64.rpm`, `phantomops-linux-amd64.tar.gz`
- **Windows**: `phantomops-windows-amd64-installer.exe`, `phantomops-windows-amd64.zip`
- **macOS**: `phantomops-darwin-universal.dmg`, `phantomops-darwin-universal.zip`
- `SHA256SUMS`: checksums for every file above

### 📄 License
PhantomOps is open source under the **Apache License 2.0**.
