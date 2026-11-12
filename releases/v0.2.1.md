# PhantomOps v0.2.1 👻

PhantomOps **v0.2.1** is a patch release: it fixes **Kubernetes connections failing in the installed app**, makes permission problems explain themselves, and makes the **Settings** page easier to navigate.

---

### 🔧 Fixes

#### Kubernetes: "permission denied" on `/etc/rancher/k3s/k3s.yaml`
- Installed from a package and started from the desktop menu, a Kubernetes connection could fail with `Unable to read /etc/rancher/k3s/k3s.yaml … permission denied`, while running from a terminal worked. Apps started from the menu do not read your shell's startup files, so the `KUBECONFIG` variable set there was missing, and on k3s machines `kubectl` then falls back to a root-only file.
- PhantomOps now finds the kubeconfig itself, in this order: the connection's own setting, `$KUBECONFIG`, `~/.kube/config`, then `kubectl`'s default. This applies to scans, dashboards, rollbacks and "Detect local services".
- Kubernetes connections have a new **kubeconfig file** field for cases where the automatic lookup is not enough. A wrong path is reported clearly instead of falling back silently.

#### Clear errors for permission problems
- If no usable kubeconfig is found, the message now says how to create one (`~/.kube/config`) instead of showing raw `kubectl` output.
- Docker connection errors now explain themselves: no access to the Docker socket (add your user to the `docker` group), Docker not running, or a stopped daemon.

---

### ✨ Improvements

#### Collapsible Settings
- Each section of **Settings** is a collapsible card. Opening Settings shows only the top one (AI investigator) open; the others show a one-line summary of their current state (for example "Every 5 min · notifications on").
- The on/off switches stay in each card's header, so you can change them without expanding the card.
- The update badge in the title bar opens Settings straight on **About & updates**.

---

### 📦 Downloads
- **Linux**: `phantomops-linux-amd64.deb`, `phantomops-linux-amd64.rpm`, `phantomops-linux-amd64.tar.gz`
- **Windows**: `phantomops-windows-amd64-installer.exe`, `phantomops-windows-amd64.zip`
- **macOS**: `phantomops-darwin-universal.dmg`, `phantomops-darwin-universal.zip`
- `SHA256SUMS`: checksums for every file above

### 📄 License
PhantomOps is open source under the **Apache License 2.0**.
