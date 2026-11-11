# 👻 PhantomOps

Your infrastructure is haunted. Find out what's killing it.

PhantomOps connects to Docker, Kubernetes, Nomad, Prometheus, Loki and SSH hosts, correlates what it
sees into incident timelines, and proposes a root cause.

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
wails dev     # live development
wails build   # production build
```
