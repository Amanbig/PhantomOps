# PhantomOps – tech & commands

- Go module `PhantomOps`, go 1.25.0. Wails desktop app; build tag `webkit2_41` on Linux.
- Frontend: **vanilla JS + Vite**. Do NOT introduce React, shadcn or Tailwind.
- Config dir `~/.config/phantomops/` (`config.json`, `history.json`, `audit.log`); `PHANTOM_CONFIG` env overrides.

## Verify before every commit
```
gofmt -l .
go vet -tags webkit2_41 ./...
go test ./internal/...
(cd frontend && npm run build)
go build -tags webkit2_41 -o /tmp/pb .
```

## Gotchas
- `wails generate module` changes exec bits in `frontend/wailsjs/runtime/*` and creates `frontend/package.json.md5`: revert with `git checkout -- frontend/wailsjs/runtime` and `rm -f frontend/package.json.md5` before committing.
- Never use broad `pkill -f`; stop test processes by exact PID.
- Test UI with headless Playwright (stand-in backend stubs); do not open windows on the user's screen.
- Kubeconfig resolution order (`internal/kubeconfig`): connection `kubeconfig` field → `$KUBECONFIG` → `~/.kube/config` → default.
- Firefox `img.naturalWidth` is 0 for SVGs without fixed size; use `img.decode()` in tests.
