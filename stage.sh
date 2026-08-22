#!/bin/sh
set -eu

repository=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
dist=${1:-dist}
name=soksak-sidecar-pty
target=${2:-}
build_dir=${SOKSAK_BUILD_DIR:-target}
extension=
case "$target" in *windows*) extension=.exe ;; esac

if [ -n "$target" ]; then
  output="$build_dir/$target/release/$name$extension"
  case "$target" in
    *windows*) target_os=windows ;;
    *linux*) target_os=linux ;;
    *darwin*) target_os=darwin ;;
    *) echo "unsupported target: $target" >&2; exit 1 ;;
  esac
  case "$target" in
    aarch64-*|arm64-*) target_arch=arm64 ;;
    x86_64-*) target_arch=amd64 ;;
    *) echo "unsupported target: $target" >&2; exit 1 ;;
  esac
  mkdir -p "$(dirname "$output")"
  GOOS="$target_os" GOARCH="$target_arch" go build -o "$output" "$repository"
else
  output="$build_dir/release/$name"
  mkdir -p "$(dirname "$output")"
  go build -o "$output" "$repository"
fi

mkdir -p "$dist"
temporary="$dist/.$name.tmp.$$"
cp "$output" "$temporary"
chmod +x "$temporary"
mv -f "$temporary" "$dist/$name"
printf 'staged: %s\n' "$dist/$name"
