#!/usr/bin/env bash
# Prepare a release: ./scripts/release.sh v0.2.0
#
# - checks the version format and that RELEASE_NOTES.md is written for it
# - sets "info.productVersion" in wails.json
# - archives RELEASE_NOTES.md as releases/<version>.md
# - checks releases/README.md has a row for the version
#
# It never commits, tags or pushes. Afterwards: open a PR, merge it, then
#   git tag <version> && git push origin <version>
set -euo pipefail

cd "$(dirname "$0")/.."

tag="${1:-}"
if ! [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: $0 vMAJOR.MINOR.PATCH" >&2
  exit 2
fi
ver="${tag#v}"

if ! head -n 1 RELEASE_NOTES.md | grep -q "$tag"; then
  echo "RELEASE_NOTES.md must start with a heading containing $tag (e.g. '# PhantomOps $tag')." >&2
  exit 1
fi

if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  echo "Tag $tag already exists." >&2
  exit 1
fi

# wails.json: update info.productVersion without reformatting anything else.
python3 - "$ver" <<'EOF'
import json, sys
ver = sys.argv[1]
with open("wails.json") as f:
    data = json.load(f)
data.setdefault("info", {})["productVersion"] = ver
with open("wails.json", "w") as f:
    json.dump(data, f, indent=2)
    f.write("\n")
EOF

mkdir -p releases
cp RELEASE_NOTES.md "releases/$tag.md"

if ! grep -q "\*\*$tag\*\*" releases/README.md; then
  echo "Archived releases/$tag.md and set wails.json to $ver,"
  echo "but releases/README.md has no row for **$tag**. Add one (and mark it *Current*), then re-run." >&2
  exit 1
fi

echo "Prepared $tag:"
echo "  wails.json productVersion = $ver"
echo "  releases/$tag.md          = RELEASE_NOTES.md"
echo
echo "Next: commit these changes in a PR, merge it, then"
echo "  git tag $tag && git push origin $tag"
