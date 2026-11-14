# Contributing to PhantomOps

Thanks for helping. Bug reports, ideas, docs and code are all welcome.

## Before you start
- Search existing issues first. For bugs or features use the issue templates.
- For anything big, open an issue to talk about it before writing code.
- Security problems: see [SECURITY.md](SECURITY.md), do not open a public issue.

## Development
Needs Go 1.25+, Node 20+ and [Wails v2](https://wails.io). On Linux also GTK 3 and WebKitGTK 4.1 (development packages) and the `webkit2_41` build tag. macOS and Windows do not use that tag; see the README's Development section.

```
wails dev -tags webkit2_41      # run the app with hot reload (Linux; on macOS/Windows: wails dev)
```

Before opening a PR, run (on macOS and Windows leave out `-tags webkit2_41`):

```
gofmt -l .                            # must print nothing
go vet -tags webkit2_41 ./...
go test ./internal/...
(cd frontend && npm run build)
```

The frontend is plain JavaScript + Vite on purpose. Please do not add React, Tailwind or other frameworks.

## Pull requests
- External contributors: one feature or fix per branch (`feat/…`, `fix/…`, `chore/…`) and one PR each. Do not push to `main`. (The maintainer batches their own work into one branch and PR per release.)
- Fill in the PR template, including what you did **not** verify.
- Checks run on PRs only. Releases are built from version tags by maintainers.

## Principles
- Read-only by default. Anything that changes infrastructure is opt-in, needs typed confirmation and is written to the audit log.
- AI is optional and off by default; it may only use collected evidence.
- Secrets stay masked in the UI and out of logs and notifications.

New collector? See [`.kiro/steering/add-collector.md`](.kiro/steering/add-collector.md) for the checklist.

## License
By contributing you agree your work is licensed under the [Apache License 2.0](LICENSE).
