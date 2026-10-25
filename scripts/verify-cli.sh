#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
compiler=${1:?usage: verify-cli.sh go|llgo}
case "$compiler" in
  go|llgo) ;;
  *) echo "unsupported compiler: $compiler" >&2; exit 1 ;;
esac
cli_dir=$(mktemp -d)
trap 'rm -rf "$cli_dir"' EXIT
source scripts/native-cflags.sh
binary="$cli_dir/ddlgo"
case "$(uname -s)" in
  Darwin)
    library="$cli_dir/calls.dylib"
    "${CLANG:-clang}" -dynamiclib testdata/add.c testdata/scalars.c testdata/cli.c testdata/arrays.c testdata/variadic.c -o "$library"
    ;;
  Linux)
    library="$cli_dir/calls.so"
    "${CLANG:-clang}" ${native_cflags[@]+"${native_cflags[@]}"} -shared -fPIC testdata/add.c testdata/scalars.c testdata/cli.c testdata/arrays.c testdata/variadic.c -o "$library"
    ;;
  MINGW*|MSYS*)
    binary="$cli_dir/ddlgo.exe"
    library="$cli_dir/calls.dll"
    "${CLANG:-clang}" ${native_cflags[@]+"${native_cflags[@]}"} -shared testdata/add.c testdata/scalars.c testdata/cli.c testdata/arrays.c testdata/variadic.c -Wl,--export-all-symbols -o "$library"
    ;;
  *) echo 'unsupported CLI test host' >&2; exit 1 ;;
esac
"$compiler" build -tags libffi -o "$binary" ./cmd/ddlgo
expect() {
  local expected=$1
  shift
  local result
  result=$("$binary" call "$@")
  test "$result" = "$expected"
  echo "$compiler CLI $1: $result"
}
expect 42 'func add(int32,int32)int32' 20 22 "$library"
expect 42 'add(20:int32 22:int32)int32' "$library"
expect 42 'add(20:int32, 22:int32)int32' "$library"
expect 42 add 20 22 "$library"
expect 42 'func mixed(int32,float64,float32,uint64)float64' 10 20.5 1.5 10 "$library"
expect 42 'mixed(10:int32,20.5:float64,1.5:float32,10:uint64)float64' "$library"
expect -42 'func negate(int32)int32' 42 "$library"
expect 4294967295 'echo_u32(0xffffffff:uint32)uint32' "$library"
expect -9223372036854775808 'echo_i64(-9223372036854775808:int64)int64' "$library"
expect 18446744073709551615 'echo_u64(18446744073709551615:uint64)uint64' "$library"
expect 20.5 'echo_f32(20.5:float32)float32' "$library"
expect 0x0 'echo_ptr(nil:unsafe.Pointer)unsafe.Pointer' "$library"
expect 42 'func answer()int32' "$library"
expect 42 -abi=cdecl -symbol=add 'func placeholder(int32,int32)int32' 20 22 "$library"
expect 42 -variadic-from=1 'var_promotions(-65500:int32,-5:int8,65535:uint16,true:bool,10.5:float32,0.5:float64)float64' "$library"
expect 42 -variadic-from=2 'func var_fixed(float32,int32,float32)float64' 20.5 1 21.5 "$library"
expect 42 -variadic-from=1 'var_empty(42:int32)int32' "$library"
expect 42 -variadic-from=1 'var_pair(2:int32,{a:20,b:20}:struct{a,b int32})int32' "$library"
expect 42 -variadic-from=1 'var_pointer(0:int32,&40:*int32)int32' "$library"
expect void 'no_result()' "$library"
expect -128 'echo_i8(-128:int8)int8' "$library"
expect 255 'echo_u8(255:uint8)uint8' "$library"
expect -32768 'echo_i16(-32768:int16)int16' "$library"
expect 65535 'echo_u16(65535:uint16)uint16' "$library"
expect true 'echo_bool(true:bool)bool' "$library"
expect 42 'func sum_pair(struct{a,b int32})int32' '{a:20,b:22}' "$library"
expect 42 'sum_pair({a:20,b:22}:struct{a,b int32})int32' "$library"
expect 42 'sum_pair_ptr(&{a:20,b:22}:*struct{a,b int32})int32' "$library"
expect '{a:20,b:22}' 'echo_pair({20,22}:struct{a,b int32})struct{a,b int32}' "$library"
expect '&{a:22,b:20}' 'mutate_pair({20,22}:*struct{a,b int32})*struct{a,b int32}' "$library"
expect 42 'scalar_ptr(&40:*int32)int32' "$library"
expect 42 'sum_nested({tag:1,p:{a:20,b:20},extra:1}:struct{tag int8;p struct{a,b int32};extra float64})float64' "$library"
expect '{tag:1,p:{a:20,b:20},extra:1}' 'echo_nested({tag:1,p:{a:20,b:20},extra:1}:struct{tag int8;p struct{a,b int32};extra float64})struct{tag int8;p struct{a,b int32};extra float64}' "$library"
expect '{x:20.5,y:21.5}' 'echo_float_pair({20.5,21.5}:struct{x,y float32})struct{x,y float32}' "$library"
expect '{p:&{a:22,b:20},bonus:0}' 'echo_pair_ref({p:&{a:20,b:20}}:struct{p *struct{a,b int32};bonus int32})struct{p *struct{a,b int32};bonus int32}' "$library"
expect 42 'func sum_array_i32(struct{values [2]int32})float64' '{values:{20,22}}' "$library"
expect 42 'sum_array_ptr(&{20,22}:*[2]int32)int32' "$library"
expect '{values:{20.5,21.5}}' 'echo_array_f64({{20.5,21.5}}:struct{values [2]float64})struct{values [2]float64}' "$library"
expect '&{22,20}' 'mutate_array(&{20,22}:*[2]int32)*[2]int32' "$library"
expect '{values:{&22,&20}}' 'mutate_array_refs({{&20,&22}}:struct{values [2]*int32})struct{values [2]*int32}' "$library"
expect 42 -variadic-from=1 'var_array(2:int32,{{20,20}}:struct{values [2]int32})int32' "$library"

# Invalid C array signatures must fail before attempting to load native code.
for declaration in 'func f([2]int32)' 'func f()[2]int32' 'func f(*[0]int32)'; do
  if "$binary" call "$declaration" "$cli_dir/not-a-library" >"$cli_dir/error" 2>&1; then
    echo "accepted invalid array signature: $declaration" >&2
    exit 1
  fi
  if ! grep -qi 'array' "$cli_dir/error"; then
    cat "$cli_dir/error" >&2
    exit 1
  fi
done

# Dynamic declarations also execute raw objects/archives on each native target.
if [[ "$(uname -s)" == MINGW* || "$(uname -s)" == MSYS* ]]; then
  case "$(go env GOARCH)" in
    amd64) triple=x86_64-pc-windows-msvc ;;
    arm64) triple=aarch64-pc-windows-msvc ;;
    386) triple=i686-pc-windows-msvc ;;
    *) echo 'unsupported Windows CLI architecture' >&2; exit 1 ;;
  esac
  "${CLANG:-clang}" --target="$triple" -ffreestanding -c testdata/add.c -o "$cli_dir/add.o"
  "${CLANG:-clang}" --target="$triple" -ffreestanding -c testdata/pair.c -o "$cli_dir/pair.o"
else
  "${CLANG:-clang}" ${native_cflags[@]+"${native_cflags[@]}"} -fPIC -c testdata/add.c -o "$cli_dir/add.o"
  "${CLANG:-clang}" ${native_cflags[@]+"${native_cflags[@]}"} -fPIC -c testdata/pair.c -o "$cli_dir/pair.o"
fi
"${AR:-ar}" rcs "$cli_dir/add.a" "$cli_dir/add.o"
"${AR:-ar}" rcs "$cli_dir/pair.a" "$cli_dir/pair.o"
expect 42 'func add(int32,int32)int32' 20 22 "$cli_dir/add.o"
expect 42 'add(20:int32 22:int32)int32' "$cli_dir/add.a"
expect 42 'sum_pair({20,22}:struct{a,b int32})int32' "$cli_dir/pair.o"
expect '{a:20,b:22}' 'echo_pair({20,22}:struct{a,b int32})struct{a,b int32}' "$cli_dir/pair.a"
expect '&{a:22,b:20}' 'mutate_pair(&{20,22}:*struct{a,b int32})*struct{a,b int32}' "$cli_dir/pair.o"

# TLS is an explicit session option for raw objects, including typed calls.
tls_cflags=(-fPIC)
tls_cxx=${CLANG:-clang}
if [[ "$(uname -s)" == Darwin && -n "${DYLIB_LLVM_CLANG:-}" ]]; then
  # Match the library tests: Apple Clang 17 mixes native/emulated C++ TLS at O0.
  tls_cxx=$DYLIB_LLVM_CLANG
fi
"$tls_cxx" --version
if [[ "$(uname -s)" == MINGW* || "$(uname -s)" == MSYS* ]]; then
  tls_cflags=(--target="$triple" -ffreestanding)
fi
for mode in emulated native; do
  if [[ "$mode" == emulated ]]; then
    tls_codegen=-femulated-tls
    tls_option=EmulatedTLS
  else
    tls_codegen=-fno-emulated-tls
    tls_option=NativeTLS
  fi
  for opt in -O0 -O2; do
    tls_object="$cli_dir/$mode$opt.o"
    tls_archive="$cli_dir/$mode$opt.a"
    "${CLANG:-clang}" ${native_cflags[@]+"${native_cflags[@]}"} "${tls_cflags[@]}" "$tls_codegen" "$opt" -c testdata/cli_tls.c -o "$tls_object"
    "${AR:-ar}" rcs "$tls_archive" "$tls_object"
    if "$binary" call 'func cli_tls_add(int32,int32)int32' 20 20 "$tls_object" >"$cli_dir/error" 2>&1; then
      echo "accepted $mode TLS without an option" >&2
      exit 1
    fi
    if ! grep -Fq "Options.$tls_option" "$cli_dir/error"; then
      cat "$cli_dir/error" >&2
      exit 1
    fi
    for tls_input in "$tls_object" "$tls_archive"; do
      expect 42 "-$mode-tls" 'func cli_tls_add(int32,int32)int32' 20 20 "$tls_input"
      expect 42 "-$mode-tls" 'cli_tls_add(20:int32 20:int32)int32' "$tls_input"
      expect 42 "-$mode-tls" cli_tls_add 20 20 "$tls_input"
      expect '{a:22,b:20}' "-$mode-tls" 'cli_tls_pair({20,22}:struct{a,b int32})struct{a,b int32}' "$tls_input"
      echo "$compiler CLI TLS $mode $opt ${tls_input##*.}: PASS"
    done
    cxx_tls_cflags=("${tls_cflags[@]}")
    cxx_tls_object="$cli_dir/cxx-$mode$opt.o"
    cxx_tls_archive="$cli_dir/cxx-$mode$opt.a"
    "$tls_cxx" ${native_cflags[@]+"${native_cflags[@]}"} "${cxx_tls_cflags[@]}" "$tls_codegen" "$opt" -std=c++17 -fno-exceptions -fno-rtti -fno-stack-protector -c testdata/cli_tls.cpp -o "$cxx_tls_object"
    "${AR:-ar}" rcs "$cxx_tls_archive" "$cxx_tls_object"
    for tls_input in "$cxx_tls_object" "$cxx_tls_archive"; do
      # Its global finalizer traps unless the current thread's C++ TLS object
      # has already been destroyed exactly once during session cleanup.
      expect 42 "-$mode-tls" 'func cli_cxx_tls_add(int32,int32)int32' 20 20 "$tls_input"
      echo "$compiler CLI C++ TLS $mode $opt ${tls_input##*.}: PASS"
    done
    if [[ "$mode" == native && ( "$(uname -s)" == MINGW* || "$(uname -s)" == MSYS* ) ]]; then
      gnu_object="$cli_dir/cxx-gnu$opt.o"
      "$tls_cxx" ${native_cflags[@]+"${native_cflags[@]}"} --target="${triple%-pc-windows-msvc}-w64-windows-gnu" -ffreestanding "$tls_codegen" "$opt" -std=c++17 -fno-exceptions -fno-rtti -fno-stack-protector -c testdata/cli_tls.cpp -o "$gnu_object"
      "${AR:-ar}" rcs "$cli_dir/cxx-gnu$opt.a" "$gnu_object"
      for tls_input in "$gnu_object" "$cli_dir/cxx-gnu$opt.a"; do
        expect 42 -native-tls 'func cli_cxx_tls_add(int32,int32)int32' 20 20 "$tls_input"
        echo "$compiler CLI C++ TLS native GNU $opt ${tls_input##*.}: PASS"
      done
    fi
    if [[ "$mode" == native ]]; then
      mixed_object="$cli_dir/mixed$opt.o"
      "${CLANG:-clang}" ${native_cflags[@]+"${native_cflags[@]}"} "${tls_cflags[@]}" -femulated-tls "$opt" -Dcli_tls_add=cli_emutls_add -Dcli_tls_pair=cli_emutls_pair -c testdata/cli_tls.c -o "$mixed_object"
      for entry in cli_tls_add cli_emutls_add; do
        expect 42 -native-tls -emulated-tls "func $entry(int32,int32)int32" 20 20 "$tls_object" "$mixed_object"
      done
      for option in native emulated; do
        if "$binary" call "-$option-tls" 'func cli_tls_add(int32,int32)int32' 20 20 "$tls_object" "$mixed_object" >"$cli_dir/error" 2>&1; then
          echo "accepted mixed TLS with only -$option-tls" >&2
          exit 1
        fi
        if ! grep -q 'Options.*TLS' "$cli_dir/error"; then
          cat "$cli_dir/error" >&2
          exit 1
        fi
      done
      echo "$compiler CLI TLS mixed $opt: PASS"
    fi
  done
done

# The README runner builds the CLI and forwards arbitrary caller arguments.
# Relative inputs must stay relative to the caller, including paths with spaces.
runner="$(pwd)/examples/run.sh"
caller_dir="$cli_dir/caller with spaces"
mkdir -p "$caller_dir"
caller_library="fixture library.${library##*.}"
cp "$library" "$caller_dir/$caller_library"
result=$(
  cd "$caller_dir"
  bash "$runner" "$compiler" call "func sum_pair(struct{a,b int32})int32" "{a:20,b:22}" "./$caller_library"
)
test "$result" = 42
echo "$compiler runner struct declaration with relative library: $result"
result=$(bash "$runner" "$compiler" call "mutate_pair(&{a:20,b:22}:*struct{a,b int32})*struct{a,b int32}" "$cli_dir/pair.a")
test "$result" = '&{a:22,b:20}'
echo "$compiler runner typed struct pointer from archive: $result"
