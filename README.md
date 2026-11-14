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
services** finds the ones running on your machine.

For services on another Linux host, add and save its SSH connection first, then add the service and select
**Run through SSH connection**. Everything goes over the verified SSH host key, is read-only, and shares one
SSH connection per server:

- **Docker and Kubernetes** run a fixed allowlist of `docker` / `kubectl` commands on that host (never
  arbitrary commands). The remote user needs `docker` or `kubectl` on its `PATH` (non-interactive SSH does
  not read `~/.bashrc`), permission to use Docker, and for Kubernetes a readable kubeconfig (set **Remote
  kubeconfig file** if it is not `~/.kube/config`).
- **Nomad, Prometheus and Loki** are reached through a read-only tunnel (only `GET` and `HEAD`). The URL is
  the address *as seen from that server*, for example `http://127.0.0.1:4646`. Nothing opens on your
  computer. This needs port forwarding allowed on the SSH server (`AllowTcpForwarding`, on by default),
  and the URL can name anything that server can reach, so use a URL you trust.

This is monitoring only: rollbacks are never performed through SSH, and a connection that is set to use SSH
never falls back to connecting directly.

**Trusting a new server.** SSH connections verify the server against `known_hosts` and refuse unknown hosts.
In the SSH connection form, **Check host key** reads the server's key without logging in (no credential is
sent) and shows its fingerprint. Compare it with one you got from a trusted source, for example
`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` on the server, then choose **Trust this key** to add it to
`known_hosts` (the action is written to the audit log). A key that *changed* is never replaced by the app: it
shows a warning, because it can mean the connection is being intercepted. Nothing is connected by default: a fresh install
reads no Docker socket or cluster until you add a connection or run **Detect local services** and add what it finds.

Settings are stored in `$PHANTOM_CONFIG` or `<user config dir>/phantomops/config.json`
(`~/.config/phantomops/config.json` on Linux) with owner-only permissions, since it may hold tokens.
You can also edit it by hand; see `dev/config.example.json`.

## Chat notifications

Optional. In **Settings → Chat & webhooks** add a Slack, Discord or generic JSON webhook. While background
monitoring runs, new, escalated, returning and resolved incidents are posted there (muted and snoozed ones are
not). Messages only contain the title, severity and root-cause summary. The webhook URL is stored in the
owner-only config file and masked in the UI. **Activity → Webhook deliveries** lists the most recent messages
and whether each arrived (including the ones skipped after a failure); it is kept in memory and holds no URLs.

## Incident reports

On **Incidents** (live) and **History**, **Save report…** writes a Markdown postmortem of an incident and
**Copy report** puts it on the clipboard: summary, root cause and confidence, evidence, a UTC timeline, and any
rollback actions taken. Log excerpts are left out and common secrets (keys, tokens, passwords, webhook URLs)
are removed, on a best-effort basis, so read a report before you share it.

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
python3 scripts/icons.py      # regenerate all icons after editing build/appicon*.svg
```

## Releasing

1. Write the notes in `RELEASE_NOTES.md` (first line `# PhantomOps vX.Y.Z ...`) and add a row to `releases/README.md`.
2. `./scripts/release.sh vX.Y.Z`: sets the version in `wails.json` and archives the notes in `releases/`.
3. Merge that in a PR, then `git tag vX.Y.Z && git push origin vX.Y.Z`.

[Checks](.github/workflows/checks.yml) (gofmt, vet, tests, frontend and app build, release metadata) run on
pull requests. The [release workflow](.github/workflows/release.yml) runs only for version tags and just builds
and publishes: Linux, Windows and macOS packages, `SHA256SUMS`, and a GitHub Release with `RELEASE_NOTES.md` as
its description. It refuses a tag that does not match the version in `wails.json`.

## Community

- [Contributing](CONTRIBUTING.md) · [Code of Conduct](CODE_OF_CONDUCT.md) · [Security policy](SECURITY.md)
- Questions, ideas, or want to show your setup? Start a [discussion](https://github.com/Amanbig/PhantomOps/discussions).
- Found a bug? [Open an issue](https://github.com/Amanbig/PhantomOps/issues/new/choose).
- PhantomOps is free. If it helps you, you can [sponsor the project](https://github.com/sponsors/Amanbig).

## License

Copyright 2026 Amanbig. Licensed under the [Apache License, Version 2.0](LICENSE).

### Trademarks
The Docker, Kubernetes, Nomad and Prometheus logos shown in the app (from [Simple Icons](https://github.com/simple-icons/simple-icons), CC0) and the
names Loki, Grafana and SSH belong to their respective owners and are used only to identify the services PhantomOps connects
to. PhantomOps is not affiliated with or endorsed by them. Details: [`frontend/src/assets/connections/README.md`](frontend/src/assets/connections/README.md).
