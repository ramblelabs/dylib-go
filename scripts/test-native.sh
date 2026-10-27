#!/usr/bin/env bash
set -euo pipefail

# Run the compiler matrix separately from the other native packages, then
# divide its discovered tests into bounded processes. Additional build flags
# (for example -race with Go) apply to both the suite and compiler executable.
tool=${1:?Usage: test-native.sh <go|llgo> [build flags]}
shift
case "$tool" in
  go) tags=libffi; timeout=${GO_TEST_TIMEOUT:-20m} ;;
  llgo) tags=llgo,libffi; timeout=${LLGO_TEST_TIMEOUT:-35m} ;;
  *) echo "Unsupported test tool: $tool" >&2; exit 2 ;;
esac
directory=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/dylib-compiler.XXXXXX")
trap 'rm -rf "$directory"' EXIT

bash scripts/test-packages.sh "$tool" "$tags" "$timeout" "$@"

# Compile once and enumerate the binary's real tests so new tests are included.
binary="$directory/clang.test"
"$tool" test -c -tags libffi "$@" -o "$binary" ./compiler/clang
cd compiler/clang
"$binary" -test.list '^Test' > "$directory/tests"
tests=()
while IFS= read -r name; do
  case "$name" in
    Test*) tests+=("$name") ;;
  esac
done < "$directory/tests"
if (( ${#tests[@]} == 0 )); then
  echo 'Compiler test binary listed no tests' >&2
  exit 1
fi
for ((batch = 0; batch < 4; batch++)); do
  pattern=
  count=0
  for ((index = batch; index < ${#tests[@]}; index += 4)); do
    pattern+="${pattern:+|}${tests[index]}"
    count=$((count + 1))
  done
  if (( count == 0 )); then continue; fi
  printf 'Compiler qualification batch %d/4 (%d tests)\n' "$((batch + 1))" "$count"
  "$binary" -test.count=1 -test.timeout="$timeout" \
    -test.run="^($pattern)$" -test.v
done
