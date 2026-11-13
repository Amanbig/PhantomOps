#!/usr/bin/env bash
# Post a release announcement in GitHub Discussions.
#
#   ./scripts/announce-release.sh v0.3.1            # create the discussion
#   ./scripts/announce-release.sh v0.3.1 --dry-run  # look everything up, create nothing
#
# Used by the release workflow after the release is published. It needs the
# `gh` CLI with a token that may write discussions (GH_TOKEN) and Discussions
# enabled on the repository. It is safe to run by hand.
#
# Environment (all optional):
#   GITHUB_REPOSITORY      owner/name (default: Amanbig/PhantomOps)
#   DISCUSSION_CATEGORY    category slug (default: announcements)
set -euo pipefail

tag="${1:-}"
dry="${2:-}"
if ! [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { [ -n "$dry" ] && [ "$dry" != "--dry-run" ]; }; then
  echo "usage: $0 vMAJOR.MINOR.PATCH [--dry-run]" >&2
  exit 2
fi

cd "$(dirname "$0")/.."
repo="${GITHUB_REPOSITORY:-Amanbig/PhantomOps}"
owner="${repo%%/*}"
name="${repo#*/}"
slug="${DISCUSSION_CATEGORY:-announcements}"

# The archived notes for this version; fall back to the working copy (the
# release workflow checks out the tagged commit, where both are identical).
notes="releases/$tag.md"
[ -f "$notes" ] || notes="RELEASE_NOTES.md"
if ! head -n 1 "$notes" | grep -q "$tag"; then
  echo "$notes does not start with a heading for $tag; refusing to announce the wrong notes." >&2
  exit 1
fi

lookup="$(gh api graphql -f query='
  query($owner:String!, $name:String!) {
    repository(owner:$owner, name:$name) {
      id
      hasDiscussionsEnabled
      discussionCategories(first:25) { nodes { id slug } }
    }
  }' -f owner="$owner" -f name="$name")"

if [ "$(jq -r '.data.repository.hasDiscussionsEnabled' <<<"$lookup")" != "true" ]; then
  echo "Discussions are not enabled on $repo (Settings -> General -> Features)." >&2
  exit 1
fi
repo_id="$(jq -r '.data.repository.id' <<<"$lookup")"
cat_id="$(jq -r --arg s "$slug" '.data.repository.discussionCategories.nodes[] | select(.slug==$s) | .id' <<<"$lookup")"
if [ -z "$cat_id" ]; then
  echo "No discussion category with slug \"$slug\" on $repo." >&2
  exit 1
fi

title="PhantomOps $tag is out"
body="$(mktemp)"
trap 'rm -f "$body"' EXIT
{
  echo "**PhantomOps $tag** is published: https://github.com/$repo/releases/tag/$tag"
  echo
  # Notes without their own top heading, and bounded (discussion limit is 65536 chars).
  tail -n +2 "$notes" | head -c 55000
  echo
  echo
  echo '---'
  echo 'Questions, problems or ideas about this release? Reply here. Bugs are best reported as [issues](https://github.com/'"$repo"'/issues/new/choose).'
} >"$body"

if [ "$dry" = "--dry-run" ]; then
  echo "DRY RUN: would post to $repo in \"$slug\" (category $cat_id)"
  echo "Title: $title"
  echo "Body: $(wc -c <"$body") bytes, from $notes"
  exit 0
fi

url="$(gh api graphql \
  -f query='mutation($r:ID!, $c:ID!, $t:String!, $b:String!) {
    createDiscussion(input:{repositoryId:$r, categoryId:$c, title:$t, body:$b}) { discussion { url } }
  }' -f r="$repo_id" -f c="$cat_id" -f t="$title" -F b=@"$body" --jq '.data.createDiscussion.discussion.url')"
echo "Announced: $url"
