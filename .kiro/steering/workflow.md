# PhantomOps – workflow & communication

## Workflow
- One feature per branch (`feat/…`, `fix/…`, `chore/…`), one PR per branch. Never push to `main`.
- CI (`checks.yml`) runs on PRs only. Tags build and publish.
- PRs: summary, tested list, and an explicit "not verified" list. Create via `gh api repos/Amanbig/PhantomOps/pulls … -F body=@file` (`gh pr create` hits a GraphQL deprecation error).
- Only commit when asked. Stage specific files.
- Release: write `RELEASE_NOTES.md`, add a row in `releases/README.md`, run `./scripts/release.sh vX.Y.Z`, PR, merge, then tag `vX.Y.Z` and push the tag. The release workflow refuses a tag that differs from `wails.json`. After publishing, it also posts an announcement in Discussions (Announcements) via `scripts/announce-release.sh`; that step can fail without failing the release.

## Communication style ("caveman")
- The user writes terse and casual. Reply short and direct; no filler, no long headers.
- Plain words, short sentences. Lead with the result, then what was and wasn't verified.
- Offer the next step as a one-line proposal; the user approves with "yeah lets do it".
