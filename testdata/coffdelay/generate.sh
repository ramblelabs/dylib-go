#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
tool=${DLLTOOL:-dlltool}
"$tool" --version | head -n 1 | grep -q 'GNU'
temp=$(mktemp -d)
trap 'rm -rf "$temp"' EXIT
export SOURCE_DATE_EPOCH=0
for target in amd64 386; do
  case "$target" in
    amd64) triple=x86_64; machine=i386:x86-64 ;;
    386) triple=i686; machine=i386 ;;
  esac
  assembler="$temp/$target-as"
  printf '#!/bin/sh\nexec clang --target=%s-w64-windows-gnu -c "$@"\n' "$triple" > "$assembler"
  chmod +x "$assembler"
  "$tool" --deterministic-libraries -m "$machine" -S "$assembler" -d provider.def \
    --output-delaylib "windows_$target.a" -t "$temp/$target-"
  test -s "windows_$target.a"
done
