#!/bin/sh
set -eu

dist=${1:-dist}
name=soksak-sidecar-pty
target=${2:-}
extension=
case "$target" in *windows*) extension=.exe ;; esac

if [ -n "$target" ]; then
  output="target/$target/release/$name$extension"
  GOOS="$(case "$target" in *windows*) echo windows;; *linux*) echo linux;; *darwin*) echo darwin;; *) echo "unsupported target: $target" >&2; exit 1;; esac)" \
    GOARCH="$(case "$target" in aarch64-*|arm64-*) echo arm64;; x86_64-*) echo amd64;; *) echo "unsupported target: $target" >&2; exit 1;; esac)" \
    go build -o "$output" .
else
  output="target/release/$name"
  mkdir -p "$(dirname "$output")"
  go build -o "$output" .
fi

mkdir -p "$dist"
temporary="$dist/.$name.tmp.$$"
cp "$output" "$temporary"
chmod +x "$temporary"
mv -f "$temporary" "$dist/$name$extension"
printf 'staged: %s\n' "$dist/$name$extension"
