#!/usr/bin/env bash
set -euo pipefail

source_sha=$(git rev-parse "${SOURCE_SHA:?SOURCE_SHA is required}^{commit}")
version_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'

greater_or_equal() {
  local first=${1#v} second=${2#v} a b c x y z
  IFS=. read -r a b c <<< "$first"
  IFS=. read -r x y z <<< "$second"
  (( 10#$a > 10#$x || (10#$a == 10#$x && 10#$b > 10#$y) || (10#$a == 10#$x && 10#$b == 10#$y && 10#$c >= 10#$z) ))
}

reuse=""
highest=""
while IFS= read -r ref; do
  case "$ref" in
    v*) version=$ref ;;
    sdk/*) version=${ref##*/} ;;
    *) continue ;;
  esac
  if [[ ! "$version" =~ $version_pattern ]]; then continue; fi
  parent=$(git show -s --format=%P "$ref")
  if [[ "$parent" == "$source_sha" ]] && { [[ -z "$reuse" ]] || greater_or_equal "$version" "$reuse"; }; then
    reuse=$version
  fi
  if [[ -z "$highest" ]] || greater_or_equal "$version" "$highest"; then
    highest=$version
  fi
done < <(git tag --list 'v*' 'sdk/*')

if [[ -n "$reuse" ]]; then
  echo "$reuse"
elif [[ -n "$highest" && "$highest" =~ $version_pattern ]]; then
  echo "v${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.$((10#${BASH_REMATCH[3]} + 1))"
else
  echo "v0.0.1"
fi
