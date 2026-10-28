# Original-header C bridges

`compiler/cbridge` generates a separately compiled C facade for records whose
calling ABI cannot safely be reconstructed in Go or llgo. The facade includes
the original header and initializes the producer's actual C types from ordinary
transport storage, then copies result fields back. Its call uses `__typeof__(&original_function)`,
so the native compiler supplies the original parameter/result lowering, hidden
result pointers and concrete variadic promotions.

For example, an unnamed bitfield before a floating field can change the SysV
AMD64 register classes without becoming a named member. An explicit padding
member can change those classes again. This bridge never recreates that native
record: both the producer and its caller are compiled with the same C compiler.

## Generate and load

1. Parse the original header with `compiler/clang.Parse`, an explicit function
   list, `NominalRecords: true` and matching preprocessing/target flags. Nominal
   records distinguish otherwise identical logical field lists.
2. Select each original `Function.Name` and a distinct `Function.Symbol` for
   its facade export. Expand concrete variadic scalar tails with
   `Declaration.WithTail` before generation. Record tails require explicit
   matching `Signature.Args` and `Signature.ArgTypes` metadata.
3. Call `cbridge.Generate(header, declarations, functions)` and save `CSource`.
   Compile it separately with the **producer's compiler, target, headers,
   defines, includes and ABI-affecting flags**. Both Clang and GCC are tested.
   Clang's declaration model supplies the logical fields; the facade compiler
   supplies the native record layout and calling ABI.
4. Load the producer and facade as objects, archives or shared libraries.
   Select both `Binding.NativeSymbol` and `Binding.Symbol` as link roots for
   lazy archives. Generated code may need native `memcpy`/`memset`; supply the
   matching runtime symbols just as for other C objects.
5. Use `Binding.Call` with an `Invoker` that retains the facade image and calls
   its exact C signature. Retain the original target independently throughout
   the call. The tested [example](../examples/headerbridge/call.go) does both;
   [cgo](../examples/headerbridge/invoke_cgo.go) and
   [direct llgo](../examples/headerbridge/invoke_llgo.go) implementations avoid
   libffi. They are exercised by the native package tests on CI.

The independent `CHeader` exposes only
`int32_t facade(void *target, void *output, const void *input)`. Consumers can
also parse it with `compiler/clang` and use its existing cgo/direct llgo source
generators. Attributed records never cross this three-pointer interface by value.
Do not compile the native facade inside an llgo C preamble when the producer is
GCC: that would replace the required matching native compiler with LLVM.

## Storage and lifetime

`Binding.Signature` returns the original logical signature. `Input` and `Output`
return independent descriptors for natural C transport storage. `Call` validates
values, checks the host target and actual facade layout, allocates owned native
storage, copies arguments, invokes synchronously, reads the result and releases
all storage. Neither cgo nor libffi is required to generate source; execution
requires cgo, with libffi optional.

Every call first invokes the facade with a null target. An input `uint64_t`
contains the exact metadata capacity; output receives a version marker, word
count, and recursively reachable carrier sizes, alignments, member offsets and
array strides. `CheckLayout` compares these with `abi.LayoutOf` before publishing
the call's input/output pointers. A wrong header, compiler flag or storage model
that changes this carrier layout returns `ErrLayout` before invoking the target.
The check validates transport storage; it does not prove that the separately
loaded original function still matches the header used to build the facade.

The native status is zero for success, one for null input/output, and two for
a metadata capacity mismatch. A nonzero normal-call status wraps `ErrStatus`;
an invoker error is preserved. Layout queries are bounded to 64 KiB, generation
to 256 exports, 64 arguments per function and 1 MiB of C source.

Native object/function-pointer fields remain borrowed opaque addresses. Keep
their allocations, callback leases and producer images alive. Pointer arguments
and results retain their original native representation; the bridge does not
turn pointers to logical transport records into pointers to packed native
records. `abi.AddressOf` temporaries, including nested ones, are rejected.
Native callees must not retain the facade's temporary input/output storage.

## Qualified scope

| Area | Coverage |
| --- | --- |
| Host execution | Go: Linux/Windows amd64, arm64 and 386; macOS amd64 and arm64. llgo: the six 64-bit hosts |
| Inputs | Separate objects, archives and complete shared libraries; `-O0` and `-O2` |
| Producers | Native Clang on every host; native GCC additionally on Linux and macOS |
| Values | All supported fixed-width integers, Boolean, float/double, native pointers, nested records and multidimensional fixed arrays; const scalar/record/array/bitfield/pointer subobjects use native C17 initialization |
| Native record ABI | Unnamed nonzero/zero-width bitfields, empty argument lanes, floating prefix/tail gaps, packed/aligned records, large results, exhausted registers and concrete variadic values |
| Compiler packing | Ordinary defaults and `-fpack-struct=1`; natural carriers explicitly restore their own packing scope |
| Safety | Corrupted layout headers/sizes/alignments/offsets, invoker/native status failures, concurrent queries and retired facade images |

Native C control callers are compiled from the original prototypes with the
same producer. The compiler tests separately inspect all eight target triples;
cross-target compilation supplements native execution.

Arguments use designated initialization, including const subobjects and native
callback-pointer fields. Array initializers use private element/block macros that
explicitly capture outer array indices and are undefined after each argument.
This keeps a 64 KiB byte array within the existing generated-source bound;
the original ABI storage and descriptor limits still apply. Native tests check
const nested values, variadic records, exhausted registers and array block tails
at `-O0`/`-O2`, plus the full 64 KiB array at `-O0`, through Clang/GCC objects,
archives and shared libraries. Large Windows
array fixtures disable compiler stack-argument probes; those tests qualify the
initializer and storage bound separately from CRT-specific stack-probe helpers.

Function-pointer parameters/results need a separate adapter; native pointer
fields are supported. The original Clang parser's limits still apply, including
unions without an explicit view, flexible arrays and unsupported scalar types.
This package supplies neither C++ object/exception handling nor Go callbacks for
arbitrary native record conventions. Other calling conventions need their own
execution qualification even though the original C prototype is preserved.
