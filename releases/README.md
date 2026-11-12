# 📦 PhantomOps releases & changelog archive

This directory keeps the release notes of every published version of PhantomOps.
The notes for the release currently being prepared live in the root [RELEASE_NOTES.md](../RELEASE_NOTES.md);
they become the body of the GitHub Release.

### How a release is made
1. Write the notes for the new version in `RELEASE_NOTES.md` (first line: `# PhantomOps vX.Y.Z ...`).
2. Run `./scripts/release.sh vX.Y.Z`. It bumps the version in `wails.json`, copies the notes to `releases/vX.Y.Z.md` and checks that the table below has a row for the version.
3. Open a PR with those changes and merge it.
4. Tag the merge commit: `git tag vX.Y.Z && git push origin vX.Y.Z`. The release workflow builds every platform, generates `SHA256SUMS` and publishes the GitHub Release (code checks already ran on the PR).

---

### 📋 Release history

| Version | Type | Highlights | Release notes |
| :--- | :--- | :--- | :---: |
| **v0.1.1** | *Current* · First published release | Docker, Kubernetes, Nomad, Prometheus, Loki and SSH collectors; correlation engine with rule-based root cause; Connections UI; optional AI investigator (Ollama / OpenAI-compatible); reviewed Kubernetes & Nomad rollbacks with audit log; background monitoring, incident history and desktop notifications; verified in-app updates; fixed Linux release build; PR checks; Apache 2.0 | [v0.1.1.md](v0.1.1.md) |
| **v0.1.0** | Tag only · not published | Planned first release; the Linux packaging step failed, so no release was published. Everything in it ships in v0.1.1 | [v0.1.0.md](v0.1.0.md) |
