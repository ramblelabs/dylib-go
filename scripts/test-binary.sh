#!/usr/bin/env bash
set -euo pipefail
binary=${1:?Usage: test-binary.sh BINARY TIMEOUT}
timeout=${2:?Usage: test-binary.sh BINARY TIMEOUT}
directory=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/dylib-tests.XXXXXX")
pids=()
cleanup() {
  if (( ${#pids[@]} )); then
    for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done
    for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done
  fi
  rm -rf "$directory"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

"$binary" -test.list '^(Test|Fuzz|Example)' > "$directory/tests"
tests=()
while IFS= read -r name; do
  name=${name%$'\r'}
  case "$name" in Test*|Fuzz*|Example*) tests+=("$name") ;; esac
done < "$directory/tests"
if (( ${#tests[@]} == 0 )); then
  echo 'No tests discovered in test binary' >&2
  exit 1
fi

status=0
printf 'Root qualification: %d tests, four batches, two workers\n' "${#tests[@]}"
for ((wave = 0; wave < 4; wave += 2)); do
  pids=()
  logs=()
  for ((batch = wave; batch < wave + 2; batch++)); do
    pattern=
    count=0
    for ((index = batch; index < ${#tests[@]}; index += 4)); do
      pattern+="${pattern:+|}${tests[index]}"
      count=$((count + 1))
    done
    if (( count == 0 )); then continue; fi
    log="$directory/batch-$batch.log"
    printf 'Root qualification batch %d/4 (%d tests)\n' "$((batch + 1))" "$count" > "$log"
    "$binary" -test.count=1 -test.timeout="$timeout" \
      -test.run="^($pattern)$" -test.v >> "$log" 2>&1 &
    pids+=("$!")
    logs+=("$log")
  done
  if (( ${#pids[@]} )); then
    for pid in "${pids[@]}"; do
      if ! wait "$pid"; then status=1; fi
    done
  fi
  pids=()
  if (( ${#logs[@]} )); then
    for log in "${logs[@]}"; do cat "$log"; done
  fi
done
if (( status == 0 )); then
  printf 'Root qualification complete: %d tests\n' "${#tests[@]}"
fi
exit "$status"
