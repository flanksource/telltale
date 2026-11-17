set -eu
home=$1
if [ ! -e "$home" ]; then
  printf '__TELLTALE_PRESENT__=false\n'
  exit 0
fi
printf '__TELLTALE_PRESENT__=true\n'
[ -x "$home/bin/java" ] || { echo "Java home $home has no executable bin/java" >&2; exit 1; }
cat "$home/release"
printf '\n__TELLTALE_TOOLS__='
if [ -r "$home/lib/tools.jar" ]; then
  printf 'true\n'
else
  printf 'false\n'
fi
printf '__TELLTALE_ATTACH_MODULE__='
if [ -r "$home/jmods/jdk.attach.jmod" ]; then
  printf 'true\n'
else
  printf 'false\n'
fi
