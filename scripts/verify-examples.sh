#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
compiler=${1:?usage: verify-examples.sh go|llgo}
case "$compiler" in
  go) example=cgo ;;
  llgo) example=llgo ;;
  *) echo "unsupported compiler: $compiler" >&2; exit 1 ;;
esac
example_dir=$(mktemp -d)
trap 'rm -rf "$example_dir"' EXIT
source scripts/native-cflags.sh
binary="$example_dir/mixed"
case "$(uname -s)" in
  Darwin)
    library="$example_dir/scalars.dylib"
    "${CLANG:-clang}" -dynamiclib testdata/scalars.c testdata/arrays.c examples/nativevalue/testdata/exports.c examples/registration/testdata/exports.c -o "$library"
    ;;
  Linux)
    library="$example_dir/scalars.so"
    "${CLANG:-clang}" ${native_cflags[@]+"${native_cflags[@]}"} -shared -fPIC testdata/scalars.c testdata/arrays.c examples/nativevalue/testdata/exports.c examples/registration/testdata/exports.c -o "$library"
    ;;
  MINGW*|MSYS*)
    binary="$example_dir/mixed.exe"
    library="$example_dir/scalars.dll"
    "${CLANG:-clang}" ${native_cflags[@]+"${native_cflags[@]}"} -shared testdata/scalars.c testdata/arrays.c examples/nativevalue/testdata/exports.c examples/registration/testdata/exports.c -Wl,--export-all-symbols -o "$library"
    ;;
  *) echo 'unsupported example host' >&2; exit 1 ;;
esac
"$compiler" build -o "$binary" "./examples/$example"
result=$("$binary" "$library")
test "$result" = 42
echo "$compiler caller-defined mixed C signature: $result"

"$compiler" build -tags libffi -o "$binary" ./examples/bind
result=$("$binary" "$library")
test "$result" = 42
echo "$compiler dynamic libffi signature: $result"

"$compiler" build -tags libffi -o "$binary" ./examples/callback
result=$("$binary" "$library")
test "$result" = 42
echo "$compiler dynamic C callback with Go capture: $result"

"$compiler" build -tags libffi -o "$binary" ./examples/arrays
result=$("$binary" "$library")
test "$result" = 42
echo "$compiler dynamic C struct with array members: $result"

"$compiler" build -o "$binary" ./examples/nativevalue
result=$("$binary" "$library")
test "$result" = 42
echo "$compiler leased native record retained across C calls: $result"
echo "$compiler native storage without libffi: $result"

"$compiler" build -tags libffi -o "$binary" ./examples/registration
result=$("$binary" "$library")
test "$result" = 42
echo "$compiler owned native registration with code/value/callback leases: $result"

provider="$example_dir/dependency-provider.o"
consumer="$example_dir/dependency-consumer.o"
"${CLANG:-clang}" ${native_cflags[@]+"${native_cflags[@]}"} -c -fno-stack-protector examples/dependencies/testdata/provider.c -o "$provider"
"${CLANG:-clang}" ${native_cflags[@]+"${native_cflags[@]}"} -c -fno-stack-protector examples/dependencies/testdata/consumer.c -o "$consumer"
result=$(bash examples/run.sh "$compiler" dependencies "$provider" "$consumer")
test "$result" = 42
echo "$compiler retained cross-session function/data imports: $result"

object="$example_dir/cgodeclarations.o"
"${CLANG:-clang}" ${native_cflags[@]+"${native_cflags[@]}"} -c -fno-stack-protector examples/cgodeclarations/testdata/exports.c -o "$object"
result=$(bash examples/run.sh "$compiler" cgodeclarations "$object")
test "$result" = 42
echo "$compiler generated variadic C bridge without libffi: 42"

object="$example_dir/cgorecords.o"
"${CLANG:-clang}" ${native_cflags[@]+"${native_cflags[@]}"} -c -fno-stack-protector examples/cgorecords/testdata/exports.c -o "$object"
result=$(bash examples/run.sh "$compiler" cgorecords "$object")
test "$result" = 42
echo "$compiler generated record C bridge without libffi: 42"

if [[ "$compiler" == go && "$(go env GOOS)/$(go env GOARCH)" == windows/386 ]]; then
  go test -count=1 -run '^TestGeneratedWindows386Conventions$' ./examples/cgodeclarations
fi

if [[ "$compiler" == llgo ]]; then
  object="$example_dir/direct.o"
  "${CLANG:-clang}" ${native_cflags[@]+"${native_cflags[@]}"} -c -fno-stack-protector examples/directdeclarations/testdata/exports.c -o "$object"
  result=$(bash examples/run.sh llgo directdeclarations "$object")
  test "$result" = 42
  echo 'llgo generated direct C adapter without libffi: 42'
fi
