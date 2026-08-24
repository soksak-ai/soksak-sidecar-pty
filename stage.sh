#!/bin/sh
set -eu

dist=${1:-dist}
target=${2:-}
build_root=${3:-target}
[ -n "$target" ] || { echo 'usage: stage.sh <out> <target> [build-root]' >&2; exit 2; }

repository=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
name=soksak-sidecar-pty
set -- $("$repository/scripts/resolve-target.sh" "$target")
extension=$3
[ "$extension" != none ] || extension=
output="$build_root/$target/release/$name$extension"
[ -f "$output" ] || { echo "release binary is missing: $output" >&2; exit 1; }

mkdir -p "$dist"
temporary="$dist/.$name.tmp.$$"
trap 'rm -f "$temporary" "$dist/.sidecar.json.tmp.$$"' EXIT HUP INT TERM
cp "$output" "$temporary"
chmod +x "$temporary"
staged="$name$extension"
if [ -e "$dist/$staged" ]; then
  cmp -s "$temporary" "$dist/$staged" || { echo "staged binary conflicts with current build: $dist/$staged" >&2; exit 1; }
  rm -f "$temporary"
else
  mv "$temporary" "$dist/$staged"
fi

manifest="$dist/.sidecar.json.tmp.$$"
sed "s#\"process\": \"dist/$name\"#\"process\": \"dist/$staged\"#" "$repository/sidecar.json" > "$manifest"
if [ -e "$dist/sidecar.json" ]; then
  cmp -s "$manifest" "$dist/sidecar.json" || { echo "staged manifest conflicts with source: $dist/sidecar.json" >&2; exit 1; }
  rm -f "$manifest"
else
  mv "$manifest" "$dist/sidecar.json"
fi
printf 'PTY_STAGED target=%s output=%s\n' "$target" "$dist/$staged"
