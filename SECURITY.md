# Security policy

## Supported versions
Only the latest release receives fixes.

## Reporting a vulnerability
Please do **not** open a public issue. Use GitHub's private reporting:
<https://github.com/Amanbig/PhantomOps/security/advisories/new>

Include what you found, how to reproduce it, and the impact. Expect a first reply within a few days.

## Scope notes
PhantomOps holds credentials for your infrastructure, so these matter most:
- secrets leaking into the UI, logs, notifications or the LLM prompt
- rollback/remediation running without the typed confirmation, or missing from the audit log
- SSH commands outside the read-only allowlist, or host-key verification being skipped
- update installs that skip checksum verification
