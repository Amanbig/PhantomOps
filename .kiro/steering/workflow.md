# PhantomOps – workflow & communication

## Workflow
- **One branch per release.** Work for the next release goes on one long-lived branch (`release/next` while unnamed; rename it to `release/vX.Y.Z` with `git branch -m` once the version is chosen, before the first push). Do not open a branch and PR per feature.
- **Commit without asking.** After the checks in `tech.md` pass, commit each finished feature or fix as its own commit with a conventional message (`feat:`, `fix:`, `chore:`, `test:`). Stage specific files only. Never stage unrelated changes such as `go.mod`/`go.sum` drift. Never rewrite commits that were already pushed.
- **Ask first for anything that leaves the machine or can't be undone:** `git push` (any branch), opening or editing a PR, merging, tags, releases, Discussions posts, and force pushes. Never push to `main`.
- **One PR per release.** When the user says the release is ready: write `RELEASE_NOTES.md`, add a row in `releases/README.md`, run `./scripts/release.sh vX.Y.Z`, commit, then (after approval) push and open one PR for the whole branch. After it merges, and with approval, tag `vX.Y.Z` and push the tag. The release workflow refuses a tag that differs from `wails.json`. After publishing, it posts an announcement in Discussions (Announcements) via `scripts/announce-release.sh`; that step can fail without failing the release.
- **Exception: urgent fixes.** A fix that can't wait for the next release gets its own `fix/…` branch from `main`, its own PR and a patch release.
- CI (`checks.yml`) runs on PRs only, and a long-lived branch has no PR until the end. So run the full `tech.md` checks before every commit, and report anything not verified.
- PRs: summary, tested list, and an explicit "not verified" list. Create via `gh api repos/Amanbig/PhantomOps/pulls … -F body=@file` (`gh pr create` hits a GraphQL deprecation error).
- Choose the version by content: new capability is minor, fixes and small additions are patch. Keep the version sequence; do not skip numbers.
- If a lot of work is committed but not pushed, offer a push as a backup. Do not push until the user agrees.

## Communication style ("caveman")
- The user writes terse and casual. Reply short and direct; no filler, no long headers.
- Plain words, short sentences. Lead with the result, then what was and wasn't verified.
- Offer the next step as a one-line proposal; the user approves with "yeah lets do it".
