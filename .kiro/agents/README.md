# Project agents

These agents are small role prompts. Shared project facts stay in `.kiro/steering/`; do not copy them into every agent.

## Use

```sh
kiro-cli agent list
kiro-cli chat --agent platform-expert
kiro-cli chat --agent ui-designer
kiro-cli agent validate --path .kiro/agents/ui-designer.md
```

A main agent can delegate to a specialist when the task matches its description. Start with the narrowest agent that fits:

- `platform-expert`: infrastructure paths, remote services, dashboards, rollbacks
- `collector-dev`: collectors and connection wiring
- `ui-designer`: vanilla JS/CSS and screenshots
- `test-engineer`: tests and safety regressions
- `security-reviewer`: read-only review
- `release-manager`: release preparation and announcement workflow

Specialists commit verified work on the release branch without asking. They still need your approval to push, open a PR, merge, tag, publish, post a Discussion or change infrastructure.
