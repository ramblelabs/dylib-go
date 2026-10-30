#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
tool=${DLLTOOL:-dlltool}
"$tool" --version | head -n 1 | grep -q 'GNU'
temp=$(mktemp -d)
trap 'rm -rf "$temp"' EXIT
export SOURCE_DATE_EPOCH=0
targets=(amd64 386)
suffix=
case "${1:-}" in
  '') ;;
  compat) targets=(386); suffix=_compat ;;
  *) echo "usage: $0 [compat]" >&2; exit 1 ;;
esac
for target in "${targets[@]}"; do
  case "$target" in
    amd64) triple=x86_64; machine=i386:x86-64 ;;
    386) triple=i686; machine=i386 ;;
  esac
  assembler="$temp/$target-as"
  if [[ -n "$suffix" ]]; then
    assembler=${DLLTOOL_AS:-i686-w64-mingw32-as}
  else
    printf '#!/bin/sh\nexec clang --target=%s-w64-windows-gnu -c "$@"\n' "$triple" > "$assembler"
    chmod +x "$assembler"
  fi
  args=(--deterministic-libraries -m "$machine" -S "$assembler" -d provider.def
    --output-delaylib "windows_${target}${suffix}.a" -t "$temp/$target-")
  if [[ -n "$suffix" ]]; then args+=(--compat-implib); fi
  "$tool" "${args[@]}"
  test -s "windows_${target}${suffix}.a"
done
