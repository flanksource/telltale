#!/usr/bin/env bash
set -euo pipefail
source_sha=$(git rev-parse "${SOURCE_SHA:?SOURCE_SHA is required}^{commit}")
tag=${RELEASE_TAG:?RELEASE_TAG is required}
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Expected a stable semantic version tag, received $tag" >&2
  exit 1
fi

if git show-ref --verify --quiet "refs/tags/$tag"; then
  parent=$(git show -s --format=%P "$tag")
  if [[ "$parent" != "$source_sha" ]]; then
    echo "Tag $tag already exists for another source; refusing to move it" >&2
    exit 1
  fi
  changes=$(git diff --name-only "$source_sha" "$tag")
  if [[ -z "$changes" ]]; then
    echo "Tag $tag already exists without release assets" >&2
    exit 1
  fi
  while IFS= read -r path; do
    case "$path" in
      webapp/dist/*) ;;
      *) echo "Tag $tag already exists with non-asset changes" >&2
         exit 1 ;;
    esac
  done <<< "$changes"
  git cat-file -e "$tag:webapp/dist/index.html"
  echo "Reusing $tag"
  exit 0
fi

if [[ $(git rev-parse HEAD) != "$source_sha" ]]; then
  echo "HEAD must match release source $source_sha" >&2
  exit 1
fi
if [[ -n $(git status --porcelain --untracked-files=all) ]]; then
  echo "Release requires a clean source working tree" >&2
  exit 1
fi
if [[ ! -s webapp/dist/index.html ]]; then
  echo "Missing webapp/dist/index.html; run make build before preparing the release" >&2
  exit 1
fi

git checkout --detach "$source_sha"
git add -f -- webapp/dist
git -c user.name='github-actions[bot]' -c user.email='github-actions[bot]@users.noreply.github.com' commit -m "chore(embed): build assets for $tag"
git tag "$tag"
echo "Prepared $tag on an asset-only commit above $source_sha"
