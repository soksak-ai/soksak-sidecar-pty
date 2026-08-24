#!/bin/sh
set -eu

[ "$#" -eq 1 ] && [ -n "$1" ] || { echo 'usage: resolve-target.sh <target>' >&2; exit 2; }
target=$1
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
grep -F '"target": "'"$target"'"' "$root/release/targets.json" >/dev/null || {
  echo "unsupported release target: $target" >&2
  exit 2
}

case "$target" in
  aarch64-apple-darwin) printf 'darwin arm64 none\n' ;;
  x86_64-apple-darwin) printf 'darwin amd64 none\n' ;;
  aarch64-unknown-linux-gnu) printf 'linux arm64 none\n' ;;
  x86_64-unknown-linux-gnu) printf 'linux amd64 none\n' ;;
  x86_64-pc-windows-msvc) printf 'windows amd64 .exe\n' ;;
  *) echo "release target has no Go mapping: $target" >&2; exit 2 ;;
esac
