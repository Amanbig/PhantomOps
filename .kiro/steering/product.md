# PhantomOps – product

PhantomOps is "the ghost hunter for infrastructure": a desktop app (Wails: Go backend + vanilla JS/Vite frontend) that acts as an AI-assisted infrastructure detective.

It connects to Docker, Kubernetes/k3s, Nomad, Prometheus, Loki and SSH hosts, correlates events into **incidents** (timeline, root cause), offers **reviewed rollbacks**, and has monitoring, history, notifications, triage (ack/snooze/mute), per-connection dashboards and an Overview.

## Principles (do not break)
- **Read-only by default.** Collectors only read. SSH uses an allowlist of read-only commands and `known_hosts` verification.
- **Rollbacks are opt-in**: off by default, require typed confirmation, and every action is written to the audit log.
- **AI is optional and off by default.** Root cause is rule-based first; the LLM (OpenAI-compatible / Ollama) is grounded only in collected evidence.
- **Secrets are masked** in the UI and config files are written with 0600.
- **No demo/fake incidents** in the product.
- Be honest in reports: say what was verified and what was not.

## License
Apache-2.0. Third-party logos are trademarks of their owners (see README "Trademarks").
