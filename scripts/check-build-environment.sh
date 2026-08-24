#!/bin/sh
set -eu

[ "$#" -eq 1 ] && [ -n "$1" ] || { echo 'usage: check-build-environment.sh <target>' >&2; exit 78; }
target=$1
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
set -- $("$root/scripts/resolve-target.sh" "$target")
target_os=$1
target_arch=$2

go_expected=$(awk '$1 == "go" { value="go" $2; count++ } END { if (count == 1) print value; else exit 1 }' "$root/go.mod" 2>/dev/null || true)
go_actual=$(go env GOVERSION 2>/dev/null || true)
go_host_os=$(go env GOHOSTOS 2>/dev/null || true)
go_host_arch=$(go env GOHOSTARCH 2>/dev/null || true)

required_os=$go_host_os
required_arch=$go_host_arch
if [ "$(uname -s)" = Darwin ] && [ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
  required_os=darwin
  required_arch=arm64
fi

if [ -z "$go_expected" ] || [ "$go_actual" != "$go_expected" ] || \
   [ "$target_os" != "$required_os" ] || [ "$target_arch" != "$required_arch" ] || \
   [ "$go_host_os" != "$required_os" ] || [ "$go_host_arch" != "$required_arch" ]; then
  printf 'TOOLCHAIN_MISMATCH: target=%s targetRuntime=%s/%s expectedGo=%s requiredRuntime=%s/%s actualGo=%s actualRuntime=%s/%s\n' \
    "$target" "$target_os" "$target_arch" "${go_expected:-missing}" "${required_os:-unknown}" "${required_arch:-unknown}" \
    "${go_actual:-missing}" "${go_host_os:-unknown}" "${go_host_arch:-unknown}" >&2
  exit 78
fi

printf 'BUILD_ENVIRONMENT_READY target=%s go=%s runtime=%s/%s\n' "$target" "$go_actual" "$go_host_os" "$go_host_arch"
