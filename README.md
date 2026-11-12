# 👻 PhantomOps

Your infrastructure is haunted. Find out what's killing it.

PhantomOps connects to Docker, Kubernetes, Nomad, Prometheus, Loki and SSH hosts, correlates what it
sees into incident timelines, and proposes a root cause.

## Install

Download the latest build from [GitHub Releases](https://github.com/Amanbig/PhantomOps/releases/latest):

| Platform | Files |
| :--- | :--- |
| Linux | `phantomops-linux-amd64.deb`, `.rpm` or portable `.tar.gz` (needs GTK 3 and WebKitGTK 4.1) |
| Windows | `phantomops-windows-amd64-installer.exe` or portable `.zip` |
| macOS | `phantomops-darwin-universal.dmg` or `.zip` (Intel and Apple Silicon) |

Each release has a `SHA256SUMS` file (`sha256sum -c SHA256SUMS`). PhantomOps checks for new versions
(Connections → About & updates; can be turned off) and installs them in place after verifying the
checksum. Release notes for every version are kept in [`releases/`](releases/README.md).

## Connecting services

Open the **Connections** tab to add Docker/Podman, Kubernetes (k3s), Prometheus, Loki, Nomad and SSH hosts.
Each connection can be tested before saving, enabled/disabled, edited or deleted, and **Detect local
services** finds the ones running on your machine. Until you save, Docker and Kubernetes (`kubectl`) are
auto-detected so the app works out of the box.

Settings are stored in `$PHANTOM_CONFIG` or `<user config dir>/phantomops/config.json`
(`~/.config/phantomops/config.json` on Linux) with owner-only permissions, since it may hold tokens.
You can also edit it by hand; see `dev/config.example.json`.

## Try it without the GUI

```sh
# Analyze a recorded scenario (Incident #0042-style)
go run ./cmd/phantom-scan -replay dev/scenarios/incident-0042.json

# Scan live infrastructure using your config
go run ./cmd/phantom-scan

# Run a single collector and print its raw events
go run ./cmd/phantom-collect -source docker
```

`dev/` contains Docker Compose stacks and manifests (leaky and broken workloads, Prometheus/Loki,
Nomad, SSH) for exercising each collector.

## Development

```sh
wails dev -tags webkit2_41    # live development (reports version "dev")
wails build -tags webkit2_41  # local build
```

## Releasing

1. Write the notes in `RELEASE_NOTES.md` (first line `# PhantomOps vX.Y.Z ...`) and add a row to `releases/README.md`.
2. `./scripts/release.sh vX.Y.Z`: sets the version in `wails.json` and archives the notes in `releases/`.
3. Merge that in a PR, then `git tag vX.Y.Z && git push origin vX.Y.Z`.

The [release workflow](.github/workflows/release.yml) runs only for version tags. It checks formatting, vet,
tests and that all version references match the tag, builds Linux, Windows and macOS packages, generates
`SHA256SUMS` and publishes the GitHub Release with `RELEASE_NOTES.md` as its description.

## License

Copyright 2026 Amanbig. Licensed under the [Apache License, Version 2.0](LICENSE).
