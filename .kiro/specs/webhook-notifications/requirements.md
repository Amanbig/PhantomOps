# Requirements – webhook notifications (Slack/Discord/generic)

## Introduction
PhantomOps notifies on new incidents through desktop notifications. Operators also want incidents pushed to chat or a generic webhook, without leaking secrets.

## Requirements

### 1. Opt-in channels
**User story:** As an operator, I want to add webhook channels myself so nothing is sent unless I configure it.
- WHEN no channel is configured THEN nothing SHALL be sent over the network.
- WHEN a channel is added THEN the UI SHALL support Slack, Discord and a generic JSON webhook.

### 2. Content
- WHEN a new incident opens THEN the message SHALL include title, severity, affected target and root cause summary.
- Messages SHALL NOT include secrets, tokens, env values or raw log lines.

### 3. Respect triage
- WHEN an incident is acknowledged, snoozed or muted THEN no webhook SHALL be sent for it.

### 4. Secrets
- Webhook URLs are secrets: masked in the UI, stored in config with 0600.

### 5. Reliability
- Sending SHALL time out and never block monitoring; failures are shown in Activity and not retried endlessly.
- A "Send test message" button SHALL exist per channel.
