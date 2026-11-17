set -eu
command -v readlink >/dev/null || { echo 'Java discovery requires readlink' >&2; exit 1; }
root=$1
[ -d "$root/self" ] || { echo 'Java discovery requires a mounted /proc' >&2; exit 1; }
for entry in "$root"/[0-9]*/exe; do
  [ -L "$entry" ] || continue
  if executable=$(readlink "$entry"); then
    case "$executable" in
      */bin/java)
        process=${entry%/exe}
        printf '%s\n' "${process##*/}"
        ;;
    esac
  elif [ -d "${entry%/exe}" ]; then
    echo "Cannot inspect $entry; check process permissions or specify --pid" >&2
    exit 1
  fi
done
