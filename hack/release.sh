#!/usr/bin/env bash
set -euo pipefail
source_sha=$(git rev-parse "${SOURCE_SHA:?SOURCE_SHA is required}^{commit}")
tag=${RELEASE_TAG:?RELEASE_TAG is required}
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Expected a stable semantic version tag, received $tag" >&2
  exit 1
fi

modules=()
while IFS= read -r path; do
  case "$path" in sdk/go.mod|sdk/*/go.mod) modules+=("${path%/go.mod}/$tag") ;; esac
done < <(git ls-tree -r --name-only "$source_sha" sdk)
if [[ ${#modules[@]} == 0 ]]; then
  echo "No SDK modules found" >&2
  exit 1
fi

candidate=""
for ref in "$tag" "${modules[@]}"; do
  if git show-ref --verify --quiet "refs/tags/$ref"; then
    commit=$(git rev-parse "$ref^{commit}")
    if [[ $(git show -s --format=%P "$commit") != "$source_sha" || ( -n "$candidate" && "$candidate" != "$commit" ) ]]; then
      echo "Tag $ref already exists for another release; refusing to move it" >&2
      exit 1
    fi
    candidate=$commit
  fi
done

if [[ -n "$candidate" ]]; then
  changes=$(git diff --name-only "$source_sha" "$candidate")
  if [[ -z "$changes" ]]; then
    echo "Tag $tag already exists without release assets" >&2
    exit 1
  fi
  while IFS= read -r path; do
    case "$path" in
      webapp/dist/*|go.mod|go.sum|sdk/go.mod|sdk/go.sum|sdk/*/go.mod|sdk/*/go.sum) ;;
      *) echo "Tag $tag already exists with unexpected changes: $path" >&2; exit 1 ;;
    esac
  done <<< "$changes"
  git cat-file -e "$candidate:webapp/dist/index.html"
  for ref in "${modules[@]}" "$tag"; do
    if ! git show-ref --verify --quiet "refs/tags/$ref"; then git tag "$ref" "$candidate"; fi
  done
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
go run ./hack/modsum "$tag"
git add -- go.mod go.sum sdk
git add -f -- webapp/dist
git -c user.name='github-actions[bot]' -c user.email='github-actions[bot]@users.noreply.github.com' commit -m "chore(embed): prepare release $tag"
for ref in "${modules[@]}" "$tag"; do git tag "$ref"; done
echo "Prepared $tag and SDK tags on a detached release commit above $source_sha"
