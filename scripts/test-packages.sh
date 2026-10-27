#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
tool=${1:?Usage: test-packages.sh TOOL TAGS TIMEOUT [build flags]}
tags=${2:?Usage: test-packages.sh TOOL TAGS TIMEOUT [build flags]}
timeout=${3:?Usage: test-packages.sh TOOL TAGS TIMEOUT [build flags]}
shift 3
directory=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/dylib-packages.XXXXXX")
trap 'rm -rf "$directory"' EXIT

root=$(go list -tags "$tags" .)
go list "$@" -tags "$tags" ./... > "$directory/packages"
packages=()
while IFS= read -r package; do
  if [[ "$package" != "$root" && "$package" != "$root/compiler/clang" ]]; then
    packages+=("$package")
  fi
done < "$directory/packages"
"$tool" test -count=1 -timeout="$timeout" -v -tags "$tags" "$@" "${packages[@]}"

# Keep the growing root suite below per-process timeouts. Separate processes
# isolate native registries; each discovered test runs once in its original mode.
binary="$directory/root.test"
if [[ "$(go env GOOS)" == windows ]]; then binary+=.exe; fi
"$tool" test -c -tags "$tags" "$@" -o "$binary" .
bash scripts/test-binary.sh "$binary" "$timeout"
