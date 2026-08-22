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
  GOOS="$target_os" GOARCH="$target_arch" go -C "$repository" build -o "$output" .
else
  output="$build_dir/release/$name"
  mkdir -p "$(dirname "$output")"
  go -C "$repository" build -o "$output" .
fi

mkdir -p "$dist"
temporary="$dist/.$name.tmp.$$"
cp "$output" "$temporary"
chmod +x "$temporary"
staged="$name$extension"
mv -f "$temporary" "$dist/$staged"
cat > "$dist/sidecar.json" <<EOF
{
  "id": "soksak-sidecar-pty",
  "version": "0.0.2",
  "interface": {
    "id": "soksak-spec-sidecar-pty",
    "version": "0.0.1"
  },
  "process": "dist/$staged"
}
EOF
printf 'staged: %s\n' "$dist/$staged"
