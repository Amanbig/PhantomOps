# 📦 PhantomOps releases & changelog archive

This directory keeps the release notes of every published version of PhantomOps.
The notes for the release currently being prepared live in the root [RELEASE_NOTES.md](../RELEASE_NOTES.md);
they become the body of the GitHub Release.

### How a release is made
1. Write the notes for the new version in `RELEASE_NOTES.md` (first line: `# PhantomOps vX.Y.Z ...`).
2. Run `./scripts/release.sh vX.Y.Z`. It bumps the version in `wails.json`, copies the notes to `releases/vX.Y.Z.md` and checks that the table below has a row for the version.
3. Open a PR with those changes and merge it.
4. Tag the merge commit: `git tag vX.Y.Z && git push origin vX.Y.Z`. The release workflow checks that the versions match, builds every platform, generates `SHA256SUMS` and publishes the GitHub Release.

---

### 📋 Release history

| Version | Type | Highlights | Release notes |
| :--- | :--- | :--- | :---: |
| **v0.1.0** | *Current* · Initial public release | Docker, Kubernetes, Nomad, Prometheus, Loki and SSH collectors; correlation engine with rule-based root cause; Connections UI; optional AI investigator (Ollama / OpenAI-compatible); reviewed Kubernetes & Nomad rollbacks with audit log; background monitoring, incident history and desktop notifications; verified in-app updates | [v0.1.0.md](v0.1.0.md) |
