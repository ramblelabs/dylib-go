# dylib-go

[![Go](https://github.com/ramblelabs/dylib-go/actions/workflows/go.yml/badge.svg)](https://github.com/ramblelabs/dylib-go/actions/workflows/go.yml)
[![llgo](https://github.com/ramblelabs/dylib-go/actions/workflows/llgo.yml/badge.svg)](https://github.com/ramblelabs/dylib-go/actions/workflows/llgo.yml)
[![README](https://github.com/ramblelabs/dylib-go/actions/workflows/readme.yml/badge.svg)](https://github.com/ramblelabs/dylib-go/actions/workflows/readme.yml)

A Go runtime object loader: load `.o/.obj` files, link required members of `.a/.lib` archives, resolve symbols and relocations, and execute native code in the current process. Shared libraries (`.so/.dylib/.dll`) use the host operating system's loader.

The project reimplements the general loading and linking mechanisms of early [DDL](https://github.com/Marenz/ddl). It uses Go's `debug/elf`, `debug/macho`, and `debug/pe`, with no LLVM C++, ORC, or JITLink dependency. D ABI, ModuleInfo, and D runtime compatibility are outside its scope.

This is an experimental implementation with native execution tests. See the [platform and language matrix](docs/support.md) for its supported subset and limitations. On macOS amd64/arm64, [eligible weak atoms](docs/macho.md#coalesced-weak-atoms) in ordinary code/data/BSS and `S_COALESCED` sections support bounded public alias groups, including alternate entries sharing a public storage boundary. They retain independent name selection, preserve field/function-entry addresses through GOT cells and pointers, and prune associated DWARF/compact unwind metadata when weak code loses selection.

## Platform, architecture, and format support

The execution columns describe native loading and calls on the listed host. Inspection only reads metadata and does not execute code.

| Platform | Architecture | Object execution | Archive execution | Shared library execution | CI coverage |
| --- | --- | --- | --- | --- | --- |
| Linux | amd64 | ELF64 little-endian RELA `.o` | GNU/BSD ar and GNU thin `.a` | ELF `.so` | Go and llgo native calls |
| Linux | arm64 | ELF64 little-endian RELA `.o` | GNU/BSD ar and GNU thin `.a` | ELF `.so` | Go and llgo native calls |
| macOS | amd64 | Mach-O 64 / universal `.o` | GNU/BSD ar, GNU thin / universal `.a` | Mach-O / universal `.dylib` | Go and llgo native calls |
| macOS | arm64 | Mach-O 64 / universal `.o` | GNU/BSD ar, GNU thin / universal `.a` | Mach-O / universal `.dylib` | Go and llgo native calls |
| Windows | amd64 | AMD64 COFF `.obj` / `.o` | COFF/GNU ar and GNU thin `.lib` / `.a` | PE `.dll` | Go and llgo native calls |
| Windows | arm64 | ARM64 COFF `.obj` / `.o` | COFF/GNU ar and GNU thin `.lib` / `.a` | PE `.dll` | Go and llgo native calls |
| Linux | 386 | ELF32 little-endian REL/RELA `.o` | GNU/BSD ar and GNU thin `.a` | ELF32 `.so` | Native 32-bit Go calls on amd64 runners |
| Windows | 386 | i386 COFF `.obj` / `.o` | COFF/GNU ar and GNU thin `.lib` / `.a` | PE32 `.dll` | Native 32-bit Go calls under WoW64 |

The six amd64/arm64 targets test scalar and struct calls, C callbacks, and C-created callback threads with both Go and llgo; Linux/Windows 386 use native 32-bit Go processes. The current setup-llgo installer accepts only amd64/arm64, so no llgo 386 execution is claimed. Ordinary Go execution requires cgo; metadata inspection does not. Raw objects and archives support the implemented relocation subset, ELF COMDAT, COFF COMDAT selections 1–7, COFF weak aliases, and [bounded Mach-O coalesced weak atoms](docs/macho.md#coalesced-weak-atoms). Modern initialization/termination tables, legacy ELF `.ctors/.dtors`, GNU ELF split CRT `.init/.fini` fragments, COFF integer initializers, and session-owned C/C++ exit registrations are supported; Clang `-femulated-tls` is available with `Options{EmulatedTLS: true}`; the documented C++ thread-exit registrations and Clang MSVC emulated TLS initialization are supported; native Mach-O TLV on macOS amd64/arm64 and ELF dynamic TLS on Linux amd64/arm64/386 (including x86 GNU2 TLSDESC and AArch64 TLSGD/TLSLD and TLSDESC address forms), with documented C++ wrappers and thread-exit registrations, are available with `Options{NativeTLS: true}`. Native COFF TLS on Windows amd64/arm64/386 uses a Go-generated minimal OS TLS carrier with the same `NativeTLS` option; C values and Clang GNU/MSVC ABI nonthrowing C++ namespace/function-local initialization and thread-exit registration are supported. Other native TLS models remain unsupported. See [TLS ownership](docs/tls.md). Optional Windows/macOS amd64/arm64 and Linux amd64/arm64/386 C frame registration and stack traversal use `Options{RegisterUnwind: true}`; Linux supports the tested native C++ DWARF exception subset and macOS supports DWARF/compact unwind; see [unwind scope](docs/unwind.md). See [COFF selection rules](docs/coff.md) and [object lifecycle](docs/lifecycle.md) for limits. Shared libraries use the host OS loader. Inputs and call signatures must match the host architecture and OS ABI; this table does not imply cross-CPU or cross-OS execution.

Metadata inspection uses pure Go and can read ELF, Mach-O (including universal containers), COFF/PE (including bigobj and short/GNU long import libraries), and ordinary/GNU thin ar files independently of the file's CPU architecture. Windows short and standard GNU long import libraries resolve selected symbols from their DLLs; verified single-DLL templates support multiple entries and can coexist with [retained ordinary sections and COMDAT groups](docs/coff.md#gnu-long-import-libraries). `Options.LibraryPaths` configures dependency directories. See [archive containers and paths](docs/archives.md) for thin references and snapshots. See [universal Mach-O selection](docs/universal.md) for baseline CPU slices and shared-library limits. Nonstandard raw import tables, OMF, D `.ddl`, Go gc `.a`, and LLVM IR/bitcode are not directly supported library inputs. See the [detailed format limits and language matrix](docs/support.md) for other platforms and unsupported features.

## Installation

Add the module with `go get github.com/ramblelabs/dylib-go`. The package name is `dylib`. Use Go 1.23+ and a C compiler for native execution, or build the host with llgo. Pure Go metadata inspection works with `CGO_ENABLED=0`.

Known native signatures can use caller-defined cgo or llgo adapters. For dynamic signatures, install libffi development files and pkg-config and build with `-tags libffi`. The library API does not depend on the test CLI. Clang produces the CI fixtures, but is not needed to load already compiled inputs at runtime.

## Public API

- `Inspect(path)`: read format, target, symbols, and archive metadata without executing code.
- `Load(path)` / `Define(name, address)`: add inputs or native symbols supplied by the host.
- `DefineSymbol(name, symbol)`: import another session's function/data symbol and retain its code through consumer cleanup.
- `Link(roots...)`: select archive roots, extract dependencies, and apply relocations.
- `Lookup(name)`: obtain an unmanaged native address; callers manage its lifetime.
- `Resolve(name)`: resolve once and return a session-owned, untyped `Symbol`.
- `Symbol.WithAddress(callback)`: use an address while preventing concurrent `Close`.
- `WithInitializingAddress(name, callback)`: temporarily use staged raw symbols or prior library/provider symbols from an initialization callback; see [initialization and lifetime rules](docs/lifecycle.md#initialization-callbacks-and-concurrent-close).
- `Symbol.Acquire()` / `Function.Acquire()`: retain code and bound plans until unregister/join has finished, including during session retirement.
- `Bind(name, abi.Signature)` / `Function.Call`: bind and invoke an explicit dynamic signature with libffi.
- `abi.NewCallback(signature, handler)`: create a native C entry for a Go closure; acquire a lease before publishing its address.
- `NewRegistration(resources, stop)`: group code, native-value and callback leases; run unregister/join before releasing them, retaining resources on cleanup failure.
- `Close()`: free the object image and release system library references; subsequent symbol access and bound calls return `ErrClosed`.

Mach-O and i386 COFF C names omit one leading linker underscore. Mach-O supports external `N_INDR` aliases, eager indirect pointer binding, and regenerated amd64/arm64 symbol stubs, including short and padded entries; see [Mach-O symbol rules](docs/macho.md). Windows vectorcall and i386 stdcall/fastcall decorations remain; vectorcall retains identifier-leading underscores. Use the matching ABI adapter; vectorcall requires a generated C bridge. C++ names still require their exact mangled linkage name.

ELF amd64/i386 size relocations use the selected object definition's size, including merged common storage. Address-only host symbols cannot supply size metadata; see [ELF size rules](docs/elf.md).

## Library usage with typed adapters

`Resolve` returns an untyped symbol handle. `WithAddress` keeps the owning session alive while a caller-defined adapter uses its address. This complete [cgo example](examples/cgo/main.go) calls the C signature `double(int32_t,double,float,uint64_t)` and prints 42:

<!-- embedme examples/cgo/main.go -->

```go
//go:build cgo && (darwin || linux || windows)

// This example implements a caller-defined signature with an ordinary cgo
// adapter. The loader itself does not know the signature.
package main

/*
#include <stdint.h>
static double call_mixed(uintptr_t address, int32_t a, double b, float c, uint64_t d) {
    return ((double (*)(int32_t, double, float, uint64_t))address)(a, b, c, d);
}
*/
import "C"

import (
	"fmt"
	"os"

	dylib "github.com/ramblelabs/dylib-go"
)

func run(path string) error {
	s := dylib.New(dylib.Options{})
	defer s.Close()
	if err := s.Load(path); err != nil {
		return err
	}
	symbol, err := s.Resolve("mixed")
	if err != nil {
		return err
	}
	var result float64
	err = symbol.WithAddress(func(address uintptr) error {
		result = float64(C.call_mixed(C.uintptr_t(address), 10, 20.5, 1.5, 10))
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Println(result)
	return s.Close()
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: mixed-cgo OBJECT_OR_LIBRARY")
		os.Exit(1)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

```

The adapter supplies the actual types and calling convention; symbol names do not reveal function prototypes. With llgo, the [same call](examples/llgo/main.go) uses a caller-defined `//llgo:type C` function-pointer type:

<!-- embedme examples/llgo/main.go -->

```go
//go:build llgo && cgo && (darwin || linux || windows)

// This example uses a caller-defined C signature without the libffi backend.
package main

import (
	"fmt"
	"os"
	"unsafe"

	dylib "github.com/ramblelabs/dylib-go"
)

// llgo emits the indirect call using the native C calling convention.
//
//llgo:type C
type mixedFunc func(int32, float64, float32, uint64) float64

func run(path string) error {
	s := dylib.New(dylib.Options{})
	defer s.Close()
	if err := s.Load(path); err != nil {
		return err
	}
	symbol, err := s.Resolve("mixed")
	if err != nil {
		return err
	}
	var result float64
	err = symbol.WithAddress(func(address uintptr) error {
		function := *(*mixedFunc)(unsafe.Pointer(&address))
		result = function(10, 20.5, 1.5, 10)
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Println(result)
	return s.Close()
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: mixed-llgo OBJECT_OR_LIBRARY")
		os.Exit(1)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

```

Neither typed adapter enables the optional dynamic ABI backend. CI compiles and runs these programs against a real C shared library with Go on eight targets and llgo on the six amd64/arm64 targets. Run `bash examples/run.sh go library` or `bash examples/run.sh llgo library` to reproduce the typed and dynamic examples; libffi development files are required for the dynamic example.

`Load` may be called repeatedly before the first `Link`, `Lookup`, `Resolve`, or `Bind`. Add all dependencies before linking. A failed link can be retried after adding dependencies. A successful link seals the session; create a new session for another plugin set.

Use `DefineSymbol` to import resolved functions or data from another session with
automatic code retention. Close consumers before providers, or close them
concurrently; existing consumers can still use a retiring provider. See
[retained dependencies](docs/dependencies.md) and the
[CI-executed library example](examples/dependencies/main.go).

The optional [`module`](module) package loads versioned dependency manifests.
`module.Discover` selects exact-version providers from catalogs in explicit
directories before loading. `module.DiscoverCompatible` selects a consistent graph
from caller-declared acceptable versions in preference order, with bounded
backtracking. `module.DiscoverVersionRanges` expands caller-declared SemVer
intervals into that same graph search, preferring newer matching versions and
excluding prereleases by default. These APIs do not infer binary ABI versions or
signatures. See the [executed discovery examples](examples/modules/manifest.go).
The module loader validates provider versions and declared exports, loads selected dependency
graphs, retains imported code/data, and closes consumers before providers. See
the [executed manifest](examples/modules/manifest.go) and [module guide](docs/modules.md).
`module.Manager` supports [replacement with explicit state migration](docs/replacement.md),
retaining old scopes and code leases while new calls use the replacement.

## Dynamic signatures

`Bind(name, abi.Signature)` handles caller-supplied signatures through the optional libffi backend. Build with `-tags libffi` and install the libffi development files and pkg-config. This complete [dynamic example](examples/bind/main.go) is compiled and executed in CI, returning 42 with Go on all eight native library targets and llgo on the six amd64/arm64 targets:

<!-- embedme examples/bind/main.go -->

```go
//go:build libffi && cgo && (darwin || linux || windows)

// This example supplies a dynamic scalar signature to the libffi backend.
package main

import (
	"fmt"
	"math"
	"os"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
)

func run(path string) error {
	s := dylib.New(dylib.Options{})
	defer s.Close()
	if err := s.Load(path); err != nil {
		return err
	}
	f, err := s.Bind("mixed", abi.Signature{
		Result: abi.F64,
		Args:   []abi.Type{abi.I32, abi.F64, abi.F32, abi.U64},
	})
	if err != nil {
		return err
	}
	v, err := f.Call(abi.Int32(10), abi.Float64(20.5), abi.Float32(1.5), abi.Uint64(10))
	if err != nil {
		return err
	}
	fmt.Println(math.Float64frombits(v.Bits))
	return s.Close()
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: mixed-bind OBJECT_OR_LIBRARY")
		os.Exit(1)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

```

Supported scalar types are signed/unsigned 8/16/32/64-bit integers, C `bool`, `f32/f64`, and pointers. Ordinary C structs support arguments, results, nesting, pointer fields, and fixed-length array members through `abi.TypeDesc`. Use `abi.StructValue` for fields, `abi.ArrayValue` for array elements, `abi.AddressOf` for temporary native copies, and `abi.Ptr` for caller-managed native addresses. Temporary mutations are copied back; native code must not retain those pointers. Layout and register classification come from libffi. Calls allow `void` results and allocate argument storage for the concrete signature length. Native ABI limits and available memory still bound call size. The dynamic backend does not support unions, packed/aligned structs, bitfields, C++ `this` adjustment, or Swift calling conventions. Explicit packed/aligned records and bitfields can use the generated C bridges described below; union member and whole-representation views use `compiler/cunion`.

Arrays use `TypeDesc{Type: abi.Array, Len: N, Elem: &element}` and may appear inside structs or behind pointers, including multidimensional arrays and arrays of structs or pointers. C array parameters decay to pointers; bare array arguments/results are rejected. The [array library example](examples/arrays/main.go) constructs an array member and calls a C function; `examples/run.sh go library` and `examples/run.sh llgo library` execute it in CI. See [array types and limits](docs/arrays.md).

Use `abi.NewNativeValue(description, initial)` when C must retain scalar, record
or array storage across calls. Acquire a lease, pass its address with `abi.Ptr`,
unregister/join native users, then release the lease and close the owner. Reads
and writes use logical values; pointer fields remain caller-owned addresses.
This optional libffi path owns storage separately from code images. See
[native value ownership](docs/native-values.md) and the
[tested library example](examples/nativevalue/main.go).

For a long-lived registration, `NewRegistration` acquires independent leases on
its declared symbols/functions, native values and callbacks before addresses are
published through `WithLeases`. Its stop function unregisters and joins native
users while all leases remain alive; failed cleanup retains them for a later
`Close` retry. Resource owners remain independent. See
[registration ownership](docs/registrations.md) and the
[tested C registration example](examples/registration/main.go), executed by
`examples/run.sh go library` and `examples/run.sh llgo library`.

`abi.LayoutOf(description, abi.CDecl)` returns the current native backend's storage size, alignment and direct member offsets as independent Go data. It uses the same type construction as calls and callbacks; pointer queries describe the address itself. Compare these values with the producer's compiler layout before using generated records. See [native layout queries](docs/layout.md).

For a variadic call shape, set `Signature.Variadic = true` and `FixedArgs` to the fixed-prefix count. `Args` includes every concrete tail argument. The backend promotes tail `float32` to `float64` and small integers / C bool to `int32`; fixed arguments retain their declared types. Prepare another binding for a different tail shape. `Convention` accepts `abi.Default` / `abi.CDecl` on supported hosts and `abi.StdCall` / `abi.FastCall` / `abi.ThisCall` for fixed calls on Windows 386; unsupported host/convention combinations fail during preparation.

`ThisCall` takes an explicit receiver pointer as `Args[0]`; dynamic calls and callbacks support scalar/pointer/void results and ordinary struct arguments. Struct results require `compiler/clang.Header.CgoSource` with the exact producer target so the receiver and hidden result pointer use the correct native positions. See the [tested generated thiscall example](examples/thiscall/add_windows_386.go).

`abi.Win64` selects fixed Microsoft x64 calls on amd64. `abi.SysV64` selects fixed System V AMD64 calls; dynamic calls/callbacks require Linux or macOS, while generated `CgoSource` bridges also support it on Windows. Both preserve the producer's C data model and require exact signatures. They do not provide another operating system's runtime. The [generated dual-ABI example](examples/foreignabi/call_amd64.go) calls both conventions without the optional dynamic-call backend.

`Bind` indexes session-owned call plans by validated logical signatures. Equal signatures share a plan across symbols. Distinct logical signatures can share a native CIF, layouts and bounded buffer pools while preserving their validation and result metadata. This includes renamed record fields, different pointee descriptions, default/cdecl conventions and equally promoted variadic tails. Each active call has separate storage. Temporary pointers are valid only for their call; cleared backing memory may be reused later. Declared temporary pointee layouts are prepared lazily. `Session.Close` releases all plans. For independently managed native addresses, use `abi.Prepare(signature)`, optional `CallPlan.Share(signature)`, `CallPlan.Call(address, values...)`, and explicit `CallPlan.Close`; plans do not own target code. See [shared call resources](docs/shared-plans.md) for the comparison rules and lifetime contract.

See [dynamic call benchmarks](docs/performance.md) for reproducible scalar, struct, and pointer measurements comparing bindings, prepared plans, and one-shot calls.

`abi/signature.Parse` converts a Go-style declaration into a symbol name and `abi.Signature`, ready for `Bind`; `ParseCall` also parses typed values for dynamic tests. The parser uses Go's standard `go/parser` and does not infer prototypes from symbols.

Run `go test -tags libffi ./...` or `llgo test -tags libffi ./...` to exercise the dynamic backend.

Ordinary Go's default build does not require libffi. The llgo compiler's own runtime dependencies, including its GC and libffi, are separate from this optional backend.

On Linux with llgo v1.0.6, set `export PKG_CONFIG_ALLOW_SYSTEM_CFLAGS=1` before enabling libffi. This avoids that version's newline-only pkg-config CFLAGS parsing bug ([upstream issue #2749](https://github.com/xgo-dev/llgo/issues/2749)). The qualified CI compiler includes the upstream fix; ordinary Go does not require this workaround.

## Compiling LLVM IR and bitcode

The optional [`compiler/llvm`](compiler/llvm) package compiles `.ll` or `.bc`
input through an installed `llc` and returns an owned native object. The module's
target and data layout are preserved; the input version must be compatible with
the LLVM reader. The core loader keeps its Go implementation and does not depend
on LLVM. See [compiler scope and target coverage](docs/llvm.md).

This [example](examples/llvm/main.go) loads the object snapshot, removes compiler
files, and calls a C export through the library. CI runs this source with Go and
llgo. Run `examples/run.sh <go|llgo> llvm <module.ll|module.bc>` for a module
exporting `add` with this signature.

<!-- embedme examples/llvm/main.go -->

```go
//go:build libffi && cgo && (linux || darwin || windows)

// This example compiles a module exporting the C function add(int32, int32).
package main

import (
	"context"
	"fmt"
	"os"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/llvm"
)

func call(input string) (abi.Value, error) {
	object, err := llvm.Compile(context.Background(), input, llvm.Options{Compiler: os.Getenv("DYLIB_LLC")})
	if err != nil {
		return abi.Value{}, err
	}
	defer object.Close()
	session := dylib.New(dylib.Options{LibraryPaths: object.SourceDirectories})
	defer session.Close()
	if err := session.Load(object.Path); err != nil {
		return abi.Value{}, err
	}
	if err := object.Close(); err != nil {
		return abi.Value{}, err
	}
	if err := session.Link(); err != nil {
		return abi.Value{}, err
	}
	fn, err := session.Bind("add", abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32, abi.I32}})
	if err != nil {
		return abi.Value{}, err
	}
	return fn.Call(abi.Int32(20), abi.Int32(22))
}

func main() {
	if len(os.Args) != 2 {
		panic("usage: llvm <module.ll|module.bc>")
	}
	value, err := call(os.Args[1])
	if err != nil {
		panic(err)
	}
	fmt.Println(int32(value.Bits))
}

```

`compiler/llvm.CompileArchive` converts ordinary GNU/COFF/BSD and GNU thin/proxy archives containing
bitcode, native objects, or both into a native archive. Members retain their order
and names; `Link` selects dependencies and runs only selected initializers.
The [CI-tested archive example](examples/llvmarchive/call.go) requests an explicit
root and closes compiler storage before execution. Run
`examples/run.sh <go|llgo> llvmarchive <library.a>`.

<!-- embedme examples/llvmarchive/call.go -->

```go
//go:build libffi && cgo && (linux || darwin || windows)

package main

import (
	"context"
	"os"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/llvm"
)

func call(input string) (abi.Value, error) {
	archive, err := llvm.CompileArchive(context.Background(), input, llvm.Options{Compiler: os.Getenv("DYLIB_LLC")})
	if err != nil {
		return abi.Value{}, err
	}
	defer archive.Close()
	session := dylib.New(dylib.Options{LibraryPaths: archive.SourceDirectories})
	defer session.Close()
	if err := session.Load(archive.Path); err != nil {
		return abi.Value{}, err
	}
	if err := archive.Close(); err != nil {
		return abi.Value{}, err
	}
	if err := session.Link("add"); err != nil {
		return abi.Value{}, err
	}
	fn, err := session.Bind("add", abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32, abi.I32}})
	if err != nil {
		return abi.Value{}, err
	}
	return fn.Call(abi.Int32(20), abi.Int32(22))
}

```

External thin members are snapshotted. `Archive.SourceDirectories` exposes their
original dependency directories for explicit `dylib.Options.LibraryPaths`.
`compiler/llvm.CompileModules` explicitly merges IR/bitcode modules into one native
object after validating their target triples and data layouts. The
[CI-tested library example](examples/llvmmodules/main.go) calls a function whose
implementation spans two modules. Run `examples/run.sh <go|llgo> llvmmodules <module>...`.
See [container and ownership limits](docs/llvm.md).

## C header declarations

`compiler/clang.Parse` extracts explicitly requested C prototypes using an optional
external Clang. Integer widths and plain `char` signedness come from compiler
probes; `Header.GoSource` emits ordinary Go declarations for dynamic `Session.Bind`.
The [CI-tested example](examples/declarations/main.go) uses generated, target-qualified
declarations and checks them with `ForHost` before passing a generated struct
callback to C. Run
`examples/run.sh <go|llgo> declarations <library>`.

Generation covers supported scalars, typedefs, ordinary structs, fixed array
members, typed struct pointers, function-pointer parameter/result and record-field prototypes,
variadic prefixes, C cdecl, and Windows 386 stdcall/fastcall/thiscall. `ForHost` checks compiler record sizes, alignments and offsets
against the actual backend before calls, including callback records.
`LookupFunctionPointer` supplies signatures for `abi.NewCallback` or returned native
addresses, including nested prototypes and multiple pointer layers. Explicitly
packed/aligned records and ordinary members, plus integral/Boolean bitfields, use generated C bridges, including
unnamed and zero-width allocation boundaries. Named and typedef enums use
compiler-confirmed integer or Boolean types, including packed/short enums and
fixed underlying types accepted by Clang. Clang global `-fpack-struct` defaults
and local pragma overrides use compiled C bridges; the
[tested packing example](examples/globalpacking/call.go) covers values, bitfields,
native pointers, factories and variadic calls. Native union signatures use the `compiler/cunion` facades below.
See [types, targets and limits](docs/clang.md).

Records containing callbacks use opaque native address fields or fixed arrays
of addresses, with independent
prototype metadata. `Header.ForHostRecord` and `Record.LookupFunctionPointer`
validate and inspect these entries. The [tested record example](examples/recordcallbacks/call.go)
passes and returns callback records, including multidimensional callback arrays, with cgo and direct
llgo bindings. Retained Go callback fields use `abi.NewCallback` and `Registration`
to keep entries alive until native code clears them.

| Generator | Host compiler | Native calls | libffi required |
| --- | --- | --- | --- |
| `Header.GoSource` | Go / llgo | Dynamic `Session.Bind`, including supported records and variadic shapes | Yes |
| `Header.CgoSource` | Go / llgo | Typed scalars, ordinary/pragma-packed/explicitly packed or aligned records and bitfields, native addresses and concrete cdecl variadic shapes; fixed Windows 386 stdcall/fastcall/thiscall | No |
| `Header.LLGoSource` | llgo | Typed cdecl scalars, ordinary records, concrete variadic shapes and fixed/variadic native function pointers | No |

`Header.CgoSource(packageName, bindingType)` generates typed methods usable with
Go on all eight native targets and llgo on the six 64-bit targets. Expand variadic
declarations with `Declaration.WithTail` before generation; the C compiler applies
default argument promotions. Native function-pointer parameters/results remain
caller-owned addresses, with their C prototypes preserved inside the bridge.
Higher-order prototypes retain nested argument/result conventions; pointer slots
are borrowed native addresses, with explicit caller-managed storage lifetimes.
Fixed stdcall/fastcall/thiscall exports and native function-pointer prototypes are also
supported with Go on Windows 386. Ordinary record values support nested records,
fixed arrays and native address fields. Fields are copied into independent C
values; public Go structs need not share C padding or alignment. Constructors
verify C sizes, alignments and offsets against Clang before resolving symbols.
Set `clang.Options.NominalRecords` to distinguish C declarations with identical
fields but different native layouts. Optional `abi.TypeDesc.RecordID` metadata
preserves their identities across nested values, pointers and generated bindings.
See the [tested example](examples/nominalrecords/call.go).

Explicit `__attribute__((packed))` records and bitfields, including nested arrays, use ordinary
transport records across Go/C and field copies inside C. These native values
never cross that boundary directly. The [tested packed example](examples/packedrecords/call.go)
also covers ordinary `#pragma pack` records, nested packing levels, floating
arrays, record/member alignment beyond the packing cap, nested aligned values,
native factories/function-pointer fields, struct pointers and variadic record
calls with Go and llgo. The [over-aligned bitfield example](examples/overalignedbits/call.go)
also covers allocation units, zero-width boundaries and nested records whose
alignment exceeds the packing cap; see the
[pragma packing scope](docs/clang.md#pragma-packing). Dynamic libffi binding and direct `LLGoSource` reject these layouts.
For bitfields, Clang emits constants whose byte masks are read with Go's object
parsers; generated C bridges verify both masks and layout before binding. Public
Go fields hold ordinary scalar values. The [tested bitfield example](examples/bitfieldrecords/call.go)
covers signed/Boolean fields, cross-byte widths, unnamed/zero-width boundaries,
leading padding, numeric/omitted unnamed alignment, floating fields, pragma
packing and packed combinations, including nested bitfield arrays and variadic calls.
Small GNU AMD64 records with only trailing unnamed values use implicit padding
to preserve Clang/GCC register classes and stack arguments; see the
[tested tail padding example](examples/tailpadding/call.go) and its
[qualification limits](docs/clang.md#pragma-packing).
Interior gaps with original unnamed bitfields can also use implicit prefixes
when each affected eight-byte chunk already has a named integer class; see the
[tested prefix cases](examples/overalignedbits/prefix_test.go).
GNU bridges also preserve compiler-measured packed root alignment when GCC's
pragma would otherwise raise it; see the
[tested packed root cases](examples/overalignedbits/root_test.go).
Small GNU AMD64 records also use observed unnamed-field byte masks to reject
floating or empty register lanes without a proven named integer class. Nested
integer fields and arrays retain their working C bridges; see the
[tested controls](examples/overalignedbits/classes_test.go).
On Darwin ARM64, plain nonzero unnamed bitfields use compiler-measured byte
padding in the private C bridge, preserving Clang/GCC layout and value calls.
The [tested example](examples/plainpadding/call_darwin_arm64.go) includes native
pointers, nested arrays, factories, variadic records and exhausted registers.
Named and unnamed member-level `packed` bitfields use validated C bridges,
including small value returns, packed/aligned named and unnamed members, ignored
MSVC alignment requests and mixed zero-width boundaries; see the
[packing limits](docs/clang.md#explicitly-packed-c-records).
Compiler-evaluated record/member alignment also uses C transport records,
including omitted GNU `aligned` arguments and `_Alignas(type)` members. The
[tested aligned example](examples/alignedrecords/call.go) covers packed/bitfield
combinations, nested arrays, floating aggregates, native storage alignment,
factories and variadic calls. Go records carry logical fields; pointers to these
attributed types require producer-owned native storage. Aligned scalar, enum and
native-pointer typedefs preserve raised/lowered alignment through C bridges,
including array leaves and Windows compiler storage differences. GNU AMD64
records require compiler-probed Clang 19-compatible lowering; see the
[tested typedef example](examples/typedefalignment/call.go). Whole-array typedef
alignment also preserves each alias layer, including nested scalar, record and
native-address arrays, with packing/member alignment handled separately.
Named scalar/enum typedef bitfields also preserve raised/lowered alignment,
shared allocation units and Linux ARM64 argument alignment through C bridges;
see the [tested typedef bitfield example](examples/typedefbitfields/call.go).
Unnamed and zero-width aligned base typedefs also preserve observed boundaries,
including ignored/shared MS requests and GNU gaps; see the
[tested padding typedef example](examples/paddingtypedefs/call.go). Some small
GNU AMD64 gaps still require an unqualified implicit-padding facade.
Aligned record aliases, including anonymous bodies accessible only through
aligned typedefs, also use C bridges; the [tested record typedef example](examples/recordtypedefs/call.go)
covers inheritance, lowered/raised member alignment, arrays, packing, native
factories/pointers and stack arguments. Alias alignment is saved separately
from body size/alignment, and parsing checks that their calling ABI agrees.
The [anonymous record example](examples/anonymousrecords/call.go) verifies
independent body reconstruction, alias alignment, bitfield masks and call shapes.
Plain zero-width pragma boundaries preserve the producer's root alignment and
calling ABI across Clang and GCC, including floating values, lowered record
members and leading/trailing/consecutive boundaries; see the
[tested zero-width example](examples/zeroboundaries/call.go).
Complete struct pointees use `unsafe.Pointer` native addresses with their C
prototypes preserved. They do not accept pointers to public Go record storage;
use producer-owned storage or a compatible `abi.NativeValue` lease. Other
conventions require another adapter. Run the
[tested variadic example](examples/cgodeclarations/main.go) with
`examples/run.sh <go|llgo> cgodeclarations <library>`, or the
[record example](examples/cgorecords/main.go) with
`examples/run.sh <go|llgo> cgorecords <library>`.

`Header.LLGoSource(packageName, bindingType)` generates typed methods for
C cdecl scalars, ordinary records with fixed array fields, and native struct
pointers and fixed/variadic cdecl function-pointer parameters/results on the six qualified
64-bit llgo hosts. `Declaration.WithTail` selects a typed variadic method shape;
the generated call applies C default promotions and uses llgo's native ellipsis
interface. An unexpanded prefix calls with no tail. Constructors check Clang layouts
against generated llgo storage before resolving symbols. Methods call through
`//llgo:type C` pointers inside `Symbol.WithAddress`, without libffi. The caller
owns the session, pointer storage and returned native entry lifetimes. Run the [tested direct example](examples/directdeclarations/main.go)
with `examples/run.sh llgo directdeclarations <library>`.
Higher-order entries use nested `Callback` types; an `Adder *` slot becomes a
pointer to its native callback type. Factories, consumers and slot replacement
are tested through objects, archives and shared libraries.

Returned variadic pointer types expose `__llgo_va_list ...any`; pass explicitly
promoted `int32`/`float64` values, match the C argument contract, and retain the
producer image throughout later calls. Variadic Go callback entries are not supported.

Direct narrow integer parameters require the merged
[llgo C ABI fix](https://github.com/xgo-dev/llgo/pull/2776). Native variadic pointer
locals also require the merged [debug snapshot fix](https://github.com/xgo-dev/llgo/pull/2783);
CI pins the main commit containing that fix. Runtime tests check wide
results, native function pointers, callbacks,
stack parameters and variadic prefixes in objects, archives and libraries at
Clang `-O0` and `-O2` builds. See the [direct binding contracts](docs/clang.md#direct-llgo-bindings).

This complements [llcppg](https://github.com/goplus/llcppg)'s static bindings.

## C union facades

`compiler/cunion` generates C17 exports for explicitly selected scalar, native-pointer, ordinary struct or fixed array members of union arguments/results, including nested member paths. Arrays use ordinary struct wrappers with a `Values` field in generated Go bindings. The C compiler handles the native union ABI; the exported facade works with generated cgo/direct llgo bindings or dynamic `Bind`. The selected result member is a caller contract. `Bytes` selects a complete union representation with an exact compiler-checked size, including nested unions. See [union interfaces and limits](docs/unions.md).

<!-- embedme examples/cunion/manifest.go -->

```go
// Package cunion demonstrates C union members and byte views through compiled facades.
package cunion

import (
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cunion"
)

// Functions selects member/byte views; the C compiler handles the actual union ABI.
func Functions() []cunion.Function {
	integer := cunion.Value{Type: abi.I32, Union: "union Number", Member: "i"}
	pair := cunion.Value{Type: abi.Struct, Record: "struct Pair", Union: "union Records", Member: "pair"}
	double := cunion.Value{Type: abi.F64, Union: "union Number", Member: "d"}
	nested := cunion.Value{Type: abi.I32, Union: "union Nested", Member: "payload.inner.pair.a"}
	array := cunion.Value{Type: abi.Array, Array: "IntPair", Union: "union Arrays", Member: "ints"}
	bytes := cunion.Value{Type: abi.Array, Union: "union RawSmall", Bytes: 8}
	return []cunion.Function{
		{Name: "number_add", Symbol: "union_add", Args: []cunion.Value{integer, integer}, Result: integer},
		{Name: "target", Symbol: "union_double", Args: []cunion.Value{double}, Result: double},
		{Name: "record_pair_add", Symbol: "union_pair", Args: []cunion.Value{pair, {Type: abi.Struct, Record: "struct Pair"}}, Result: pair},
		{Name: "nested_add", Symbol: "union_nested_add", Args: []cunion.Value{nested, {Type: abi.I32}}, Result: nested},
		{Name: "arrays_add", Symbol: "union_arrays_add", Args: []cunion.Value{array, {Type: abi.I32}}, Result: array},
		{Name: "raw_small_echo", Symbol: "union_bytes_small", Args: []cunion.Value{bytes}, Result: bytes},
	}
}

// Generate renders the selected members as C17 exports.
func Generate(header string) (*cunion.Facade, error) {
	return cunion.Generate(header, Functions())
}

```

## Dynamic C callbacks

`abi.NewCallback` turns an explicit fixed C signature and a Go handler into a native function pointer through libffi. It supports the same scalars, pointers, and ordinary struct values as dynamic calls. This complete [callback example](examples/callback/main.go) captures a Go value, passes its entry to a C function, and prints 42. CI executes it with Go on eight targets and llgo on six targets:

<!-- embedme examples/callback/main.go -->

```go
//go:build libffi && cgo && (darwin || linux || windows)

// This example passes a Go closure through a dynamically described C entry.
package main

import (
	"fmt"
	"os"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
)

func run(path string) error {
	s := dylib.New(dylib.Options{})
	defer s.Close()
	if err := s.Load(path); err != nil {
		return err
	}
	f, err := s.Bind("callback_add", abi.Signature{
		Result: abi.I32,
		Args:   []abi.Type{abi.Pointer, abi.I32, abi.I32},
	})
	if err != nil {
		return err
	}
	offset := int32(2)
	callback, err := abi.NewCallback(abi.Signature{
		Result: abi.I32,
		Args:   []abi.Type{abi.I32, abi.I32},
	}, func(args []abi.Value) (abi.Value, error) {
		return abi.Int32(int32(args[0].Bits) + int32(args[1].Bits) + offset), nil
	})
	if err != nil {
		return err
	}
	defer callback.Close()
	err = callback.WithAddress(func(address uintptr) error {
		value, err := f.Call(abi.Ptr(address), abi.Int32(20), abi.Int32(20))
		if err == nil {
			fmt.Println(int32(value.Bits))
		}
		return err
	})
	if err != nil {
		return err
	}
	if err := callback.Err(); err != nil {
		return err
	}
	return s.Close()
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: callback OBJECT_OR_LIBRARY")
		os.Exit(1)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

```

Use `Callback.WithAddress` for synchronous calls. For a stored registration, hold an `Acquire` lease until native code has unregistered the entry and all callers have finished; release the lease, then close the callback. Handler errors, panics, and invalid results produce a zero native result and are retained by `Callback.Err`. Incoming pointers are borrowed native addresses; returned pointers must have caller-managed native storage. Variadic callbacks and temporary pointee results are unsupported.

C-created threads may invoke the entry concurrently; captures need appropriate synchronization. A handler may resolve, bind, and call functions on the same session, including the same bound function. It must not close its own session, call plan, or callback from an active call. With llgo, the public `//export` entry relies on compiler-generated foreign-thread protection; use the [qualified compiler revision](docs/ci.md). llgo v1.0.6 does not protect dependency-package or executable exports on this path. See [callback ownership and thread integration](docs/callbacks.md). Run `bash examples/run.sh go library` or `bash examples/run.sh llgo library` to execute the README examples, including this callback.

## C++ class facades

The optional [`compiler/cxx`](compiler/cxx) package generates C++17 adapters from
an explicit class manifest. A matching C++ compiler handles construction, exact
member signatures, class copies/references/pointers, public base conversions,
native function pointers, virtual calls and destruction. Captured Go callbacks use
explicit leases for native retention; see the [function manifest](examples/cxx/functions.go)
and [callback contracts](docs/cxx.md#native-function-pointers-and-go-callbacks). The
C interface returns status codes and writes results through output pointers, keeping catchable exceptions
inside C++. The [CI-executed example](examples/cxx/generate.go) selects a small
class interface:

<!-- embedme examples/cxx/generate.go -->

```go
// Package cxxexample demonstrates a generated C facade for a C++ class.
package cxxexample

import (
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// Generate selects constructor, member, base-view and destruction exports. Compile the
// returned CXXSource with the class's own C++17 toolchain/runtime. Its CHeader
// can use compiler/clang.Parse and CgoSource for Go/llgo without libffi.
func Generate(header string) (*cxx.Facade, error) {
	return cxx.Generate(header, []cxx.Class{{
		Name:   "example::Counter",
		Delete: "counter_delete",
		Constructors: []cxx.Constructor{{
			Symbol: "counter_new", Args: []abi.Type{abi.I32},
		}},
		Methods: []cxx.Method{{
			Name: "add", Symbol: "counter_add",
			Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}},
		}},
	}, {
		Name: "example::Derived", Delete: "derived_delete",
		Constructors: []cxx.Constructor{{Symbol: "derived_new", Args: []abi.Type{abi.I32}}},
		Bases:        []cxx.Base{{Name: "example::Counter", Symbol: "derived_counter"}},
	}})
}

```

Compile the returned `CXXSource` into a complete library. Parse `CHeader` with
`compiler/clang.Parse`, selecting `Facade.Functions`, then use `CgoSource` for
Go/llgo without libffi or `Session.Bind` for dynamic calls. Retain the library
through final object destruction. The [object manifest](examples/cxx/objects.go)
adds native copies, borrowed references, typed pointers and noncopyable value
results. The [callable manifest](examples/cxx/callables.go) selects `operator()`
on named captured lambdas and `std::function`, including mutable state, native
copies, move-only captures and retained Go callbacks. The
[native pointer manifest](examples/cxx/pointers.go) also selects scalar, character,
void, enum, array, union and multi-level pointees through opaque addresses. See
[C++ types, status codes and ownership](docs/cxx.md).

Optional `cxx.DiscoverBases` asks Clang for public, unambiguous base relations
among explicitly selected type names, including transitive/virtual bases and
aliases. The [executed discovery example](examples/cxx/discover.go) assigns export
names and feeds these relations into the existing facade generator. Class/member
selection and ownership remain explicit.

`cxx.DiscoverMembers` also lists direct public constructors, destructors, methods
and conversions for a selected class or alias. It preserves native C++ type
spellings and reports overloads, default parameters, deleted/defaulted members
and static/variadic status. The [executed member example](examples/cxx/members.go)
selects declarations and generates a callable C facade. ABI signatures, ownership
and export names remain explicit; discovery does not make unsupported C++ types
directly callable.

## Native Swift calls with ABIBridge

The optional [ABIBridge adapter](docs/abibridge.md) calls synchronous thin Swift functions on macOS amd64/arm64 through a separately built native facade. Go and llgo support fixed-width scalars, native pointers, explicitly described trivial Swift storage and formally indirect values without the Go `libffi` tag. Owned Swift values use complete metadata and copy/destroy witnesses; managed argument passing, generic calls, async/throws and device ABIs remain outside this adapter.

This [executed example](examples/swiftcall/call.go) receives a prepared runtime and the provider's symbol. Load Swift providers with `Options{KeepLibraries: true}`; native signatures require the actual Swift layout and ownership contract.

<!-- embedme examples/swiftcall/call.go -->

```go
// Package swiftcall demonstrates an explicit native Swift signature.
package swiftcall

import (
	"encoding/binary"
	"fmt"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abibridge"
)

// Add calls a thin Swift `func add(_ a: Int32, _ b: Int32) -> Int32`.
// The caller supplies a loaded ABIBridge facade and the leased Swift symbol.
func Add(runtime *abibridge.Runtime, symbol *dylib.Symbol, a, b int32) (int32, error) {
	call, err := runtime.Prepare(abibridge.Signature{
		Result: abibridge.Type{Kind: abibridge.Int32},
		Args:   []abibridge.Type{{Kind: abibridge.Int32}, {Kind: abibridge.Int32}},
	})
	if err != nil {
		return 0, err
	}
	defer call.Close()
	left, right := make([]byte, 4), make([]byte, 4)
	binary.LittleEndian.PutUint32(left, uint32(a))
	binary.LittleEndian.PutUint32(right, uint32(b))
	result, err := call.Invoke(symbol, left, right)
	if err != nil {
		return 0, err
	}
	if len(result) != 4 {
		return 0, fmt.Errorf("unexpected Swift result size: %d", len(result))
	}
	return int32(binary.LittleEndian.Uint32(result)), nil
}

```

`PrepareValueType` retains complete Swift metadata from an explicit C provider.
`ValueType.New`, `Value.Clone` and `Value.Close` initialize, copy and destroy
owned native storage. The [executed ownership example](examples/swiftvalue/clone.go)
reads a copy after closing the original; its adapters borrow pointers to the
compiler-matched Swift type. Close the runtime before the metadata provider.

<!-- embedme examples/swiftvalue/clone.go -->

```go
// Package swiftvalue demonstrates owned Swift storage with copy/destroy witnesses.
package swiftvalue

import "github.com/ramblelabs/dylib-go/abibridge"

// CloneAndUse initializes a value, copies it, and uses the copy after closing
// the original. Adapters receive borrowed native storage, never ownership bytes.
// The caller supplies compiler-matched adapters and keeps the Runtime open.
func CloneAndUse(kind *abibridge.ValueType, initialize, use func(uintptr) error) error {
	value, err := kind.New(initialize)
	if err != nil {
		return err
	}
	defer value.Close()
	copy, err := value.Clone()
	if err != nil {
		return err
	}
	defer copy.Close()
	if err := value.Close(); err != nil {
		return err
	}
	return copy.WithAddress(use)
}

```

## Language interfaces and calling conventions

Dynamic `Bind` defaults to the host C ABI (`FFI_DEFAULT_ABI`), with explicit cdecl and Windows 386 stdcall/fastcall/thiscall selection. Language C exports and native language ABIs have different support levels:

| Language / interface | Calling convention / ABI | Current support | Verified coverage / limits |
| --- | --- | --- | --- |
| C | Host default C ABI | Supported subset | Eight Go targets; six llgo targets. Scalars, pointers, ordinary structs, struct pointers and concrete variadic shapes; explicit packed/aligned records and bitfields through compiled C bridges |
| C union values | Explicit member and whole-union byte facades | Supported subset | `compiler/cunion` + matching C17 compiler; scalar/struct/array members, exact-size byte wrappers, nested paths, objects, archives and libraries; generated cgo/direct llgo or dynamic binding |
| C with x86 conventions | Windows 386 stdcall / fastcall / thiscall | Supported subset | Explicit `Convention` or generated `CgoSource`; fixed calls and native function pointers, exact symbols; thiscall has an explicit pointer receiver and uses compiled C bridges for struct results; tested with C fixtures |
| C callbacks into Go / llgo | Fixed host C ABI; Windows 386 stdcall / fastcall / thiscall | Supported subset | Scalars, pointers, ordinary struct values, captures, explicit leases, and C-created threads; no variadic callbacks |
| C vectorcall | Windows amd64 / 386 | Compiled bridge subset | `Header.CgoSource` with a vectorcall-capable C compiler; fixed scalar/pointer calls and native function pointers, plus ordinary records on amd64 and GNU 386; Go amd64/386 and llgo amd64 runtime tests |
| C with explicit amd64 conventions | Microsoft x64 / System V AMD64 | Fixed calls | `abi.Win64` on Linux/macOS/Windows amd64; `abi.SysV64` dynamic calls/callbacks on Linux/macOS amd64; generated C bridges support both on all three amd64 hosts. Mixed scalars, ordinary structs, indirect results, native pointers and register spills; Windows SysV Go callbacks need a compiled adapter |
| C with other conventions | Other non-default ABIs | Not implemented | Use a compiled adapter matching the convention |
| C++ C exports | `extern "C"`, host C ABI | Verified subset | C exports and global construction/destruction on native targets; ordinary raw fixtures disable exceptions and RTTI; Linux amd64/arm64/386 and macOS amd64/arm64 execute native throw/catch/rethrow and RAII with explicit compatible runtimes and `RegisterUnwind`; macOS accepts the implemented DWARF/compact tables |
| C++ free functions | C-compatible representation, exact mangled symbol | Conditional | Caller supplies the exact symbol and signature; Windows 386 DLL/import fixtures test primitive `int32` free functions by exact MSVC mangled names and through compiled C++ callers; class values and exceptions need a compiled adapter |
| C++ class facades | Generated C exports + matching C++17 compiler | Supported subset | Explicit `compiler/cxx` manifests: constructors, ordinary/const/static members, public multiple/virtual base views, native virtual dispatch, class values/references, named native pointees, native function pointers, named lambda/`std::function` calls and captured Go callbacks, owned results and exception status codes; complete libraries |
| Direct C++ ABI calls | Compiler-specific C++ object ABI | Adapter required | Generated C++ facades handle selected object calls; there is no Go-side class layout, vtable or member-pointer interpreter |
| Rust C exports | `extern "C"` | Verified subset | Linux/macOS amd64 and arm64; `no_std` leaf objects, `panic=abort` |
| Rust with explicit amd64 conventions | `extern "win64"` / `extern "sysv64"` | Verified subset | Linux/macOS amd64; `no_std`, `repr(C)` records, fixed scalar/pointer/struct calls and callbacks at O0/O2 from objects, archives and OS libraries |
| Rust native functions | Rust ABI | Not implemented | Use C exports; Rust-specific values and panic unwinding are not adapted |
| Zig C exports | `export fn`, C calling convention | Verified subset | Linux/macOS amd64 and arm64; C-compatible exported functions |
| Fortran C exports | `bind(C)`, `iso_c_binding` | Verified subset | Linux/macOS amd64 and arm64; `VALUE` for by-value arguments; native descriptors and hidden arguments are not adapted |
| Go gc C exports | `c-shared` + `//export`, generated C entries | Verified subset | All eight Go targets; retain runtime libraries with `KeepLibraries` |
| Go gc native functions | ABIInternal / ABI0 | Not implemented | Ordinary Go functions, strings, slices, and GC-managed values are not supported plugin interfaces |
| llgo C interfaces | C exports / `c-shared`; typed C-pointer adapters | Verified subset | Six amd64/arm64 targets; 386 is unqualified; runtime initialization and ownership still apply |
| Swift C exports | `@_cdecl`, C-compatible types | Verified subset | macOS amd64/arm64 dylibs; opaque object retain/release, leased Go observers and destruction cleanup; raw runtime-registration objects are rejected |
| Swift native functions | Swift calling convention through optional ABIBridge | Explicit trivial-value subset | macOS amd64/arm64 dylibs: scalars, pointers, mixed/padded/large records, formally indirect values, register spills and retained calls; managed arguments, generics, async/throws and devices remain unsupported |
| Owned Swift values | Complete metadata and copy/destroy witnesses through optional ABIBridge | Copyable native storage | macOS amd64/arm64: explicit initialization, independent copies, reference-counted members, borrowed pointer adapters and leases; no managed representation bytes or inferred passing convention |
| Objective-C / ObjC++ C exports | `extern "C"` facade, host C ABI | Verified subset | macOS amd64/arm64 ObjC++ dylibs; ARC-managed opaque objects, retain/release and leased Go observers |
| Objective-C / ObjC++ methods | Objective-C runtime dispatch | Not implemented | No built-in message-dispatch or ARC adapter; use an explicit C facade |
| Other native languages | Explicit C-compatible exports | Unverified | No compiler-producer execution tests or current support claim |

The [language producer probes](languages_test.go) primarily test `int32` addition through C exports. The [Apple C facades](docs/apple-facades.md) additionally test opaque Swift/ObjC objects and Go observer lifetimes, with runtime images pinned using `KeepLibraries`. C fixtures cover mixed scalars, structs and pointers. The Rust amd64 alternate-ABI tests additionally execute C-compatible scalar/record values, native factories and callback reentry. These tests do not establish every language's native value representation. On Windows 386, stdcall differs from cdecl, so a C-compatible Rust `extern "system"` entry needs explicit `abi.StdCall`; the Rust producer itself is not tested on Windows. On Windows amd64/arm64, an ignored `__stdcall` annotation does not by itself select a different ABI. Caller-defined cgo/llgo adapters can cover additional contracts, but that is not built-in dynamic ABI support.

See the [DDL and ABIBridge comparison](docs/comparison.md#remaining-gaps) for missing loader features, native language adaptation, and proposed integration with llcppg.

## Lifetime and execution boundaries

Inputs must be trusted native code. The parser and execution layer provide no security sandbox. Target ISA, object format, OS ABI, CPU features, and dependencies must match the host. Relocation does not emulate a different CPU or operating system.

Objects are allocated RW, relocated in Go, flushed from the instruction cache, and protected as RX/R/RW per section. No pages are RWX. Raw objects run supported initialization tables after link validation and session-owned exit callbacks/termination tables before unmapping. The raw path rejects unimplemented native TLS models, unsupported lifecycle forms, unsupported COMDAT selections, and language runtime registration requirements. Optional Windows amd64/arm64 runtime function tables, macOS amd64/arm64 DWARF/compact unwind and Linux amd64/arm64/386 libgcc frame registration support C stack traversal; Linux supports the tested C++ DWARF personality/LSDA subset and macOS supports DWARF/compact unwind. Windows language handlers and other raw unwind backends remain pending. Use complete shared libraries when these services are needed; the OS handles their dependencies, TLS, and initialization. Exceptions must still remain within the native call boundary.

`Symbol.WithAddress` and `Function.Call` support concurrent and reentrant calls with independent argument storage. `Close` rejects new calls with `ErrClosed`, waits for active address users and leases, then releases resources. A cleanup error keeps the session retired and retains resources still needed by native cleanup; call `Close` again to retry. Completed finalizers do not repeat. Existing `SymbolLease`/`FunctionLease` entries remain usable during session retirement so native unregister/join can finish. Finish all address use before an adapter returns; close owners outside their own calls, callbacks and held leases. Synchronize shared native state and mutable pointees. See [concurrency and retirement](docs/concurrency.md). Unmanaged raw addresses require caller-managed lifetimes. `Registration` groups explicitly declared code, native-value and callback leases with an application-supplied stop operation. `Symbol.WithAddress` guards session addresses. `abi.Callback` owns a separate native entry and captures, with leases that callers hold for registrations and native workers. Go function values cannot be cast into C function pointers; use `abi.NewCallback` to create an entry. `Define` accepts a native function or data address and does not acquire a callback lease for you.

Use `Options{KeepLibraries:true}` for Go/llgo `c-shared` libraries and other runtimes with background threads, retaining OS references until process exit. `ProcessSymbols:true` searches POSIX host exports; on Windows, load the supplying DLL explicitly.

The former experimental `BindInt32`, `CallInt32`, and `Int32Func` APIs have been removed. Migrate known signatures to `Resolve` plus a typed adapter, or use `Bind` with an explicit `abi.Signature` for dynamic calls.

## Source layout and verification

| Path | Purpose |
| --- | --- |
| `format_*.go`, `archive.go` | Go parsers and the unified object model |
| `linker.go`, `relocate*.go` | Symbol selection, archive extraction, layout, relocation |
| `symbol.go`, `function.go` | Generic symbol handles and owned dynamic call plans |
| `comdat.go` | COMDAT selection before dependency discovery |
| `abi/` | Go signatures, reusable libffi calls, and leased native C callbacks |
| `abi/signature/` | Pure Go declaration, typed-invocation, and typed literal parsing |
| `abibridge/`, `compiler/abibridge/` | Optional native Swift interfaces and a pinned native-core facade builder |
| `compiler/clang/` | Optional validated C prototypes, dynamic descriptors and direct llgo adapter generation |
| `compiler/cxx/` | Go-rendered C++17 class facades with fixed C entry points and exception status codes |
| `module/` | Go-only provider catalog discovery, versioned dependency graphs, explicit migration and managed replacement |
| `internal/native/` | OS memory, instruction cache, and shared-library operations |
| `examples/call/` | Fixed-signature adapter used only by the CLI and tests |
| `examples/cgo/`, `examples/llgo/`, `examples/bind/`, `examples/callback/` | Executable README examples |
| `examples/run.sh` | Build and run the test CLI or example checks with Go or llgo |
| `examples/readme/` | Build and check the CLI examples |
| `cmd/ddlgo/` | Test CLI for inspection and native calls |
| `testdata/` | Native compiler inputs for language probes |
| `docs/` | Design, compatibility, comparisons, CI, and evidence |

Run `scripts/verify.sh` for ordinary Go, no-cgo, and vet checks. Set `DYLIB_TEST_FFI=1 DYLIB_TEST_LLGO=1` to add race/libffi and llgo tests. Set `DYLIB_TEST_LANGUAGES=rust,zig,fortran,go` for those compiler probes, or `DYLIB_TEST_LANGUAGES=1 DYLIB_TEST_LLGO=1` for all macOS probes, including Swift, ObjC++ and llgo-produced libraries. Explicitly selected compilers are required; missing tools fail the test.

All fenced code in this README comes from source files under `examples/`, embedded with [embedme](https://github.com/zakhenry/embedme). Edit those files, run `npm ci --ignore-scripts` followed by `npm run readme`, and commit the source and regenerated README together. The README workflow runs `npm run readme:verify` to reject stale snippets; the Go and llgo workflows execute the examples and check their results.

See the [design and DDL mapping](docs/design.md), [ABIBridge / llcppg comparison](docs/comparison.md), [CI matrix](docs/ci.md), [validation evidence](docs/validation.md), and [roadmap](docs/roadmap.md).

## Test CLI

[examples/run.sh](examples/run.sh) builds the CLI with the selected compiler and passes the command and arguments through. `call` enables libffi; install its development files and pkg-config. Supply an object, archive, or shared library matching the host:

| Example | Command |
| --- | --- |
| Inspect | `examples/run.sh go inspect <library>` |
| Scalar declaration | `examples/run.sh go call "func add(int32,int32)int32" 20 22 <library>` |
| Generated C declarations | `examples/run.sh go declarations <library>` |
| Generated typed C bridges | `examples/run.sh go cgodeclarations <library>` |
| Generated direct llgo bindings | `examples/run.sh llgo directdeclarations <library>` |
| Typed invocation | `examples/run.sh go call "add(20:int32 22:int32)int32" <library>` |
| Struct value | `examples/run.sh go call "func sum_pair(struct{a,b int32})int32" "{a:20,b:22}" <library>` |
| Struct pointer | `examples/run.sh go call "sum_pair_ptr(&{a:20,b:22}:*struct{a,b int32})int32" <library>` |
| Array member | `examples/run.sh go call "func sum_array_i32(struct{values [2]int32})float64" "{values:{20,22}}" <library>` |
| Array pointer | `examples/run.sh go call "sum_array_ptr(&{20,22}:*[2]int32)int32" <library>` |
| Concrete variadic call | `examples/run.sh go call -variadic-from=2 "func var_fixed(float32,int32,float32)float64" 20.5 1 21.5 <library>` |

Replace `go` with `llgo` to use that compiler. Quote each declaration or invocation as a single shell argument. File paths remain relative to your working directory. Use Bash, including Git Bash/MSYS2 on Windows. CI checks struct declarations and typed pointers through this runner with both compilers.

To build and verify the included fixtures, run `examples/run.sh go quickstart`, `examples/run.sh go dynamic`, or `examples/run.sh go structs`. The first two run on Linux/macOS; `structs` runs on all supported native targets and produces `build/structs.so`, `build/structs.dylib`, or `build/structs.dll` for further calls. `examples/run.sh go library` checks the typed and dynamic library API examples.

See the [CLI reference](docs/cli.md) for complete syntax, supported types, and lifetime rules. Options such as `-keep-libraries`, `-process`, `-emulated-tls`, `-native-tls`, `-abi`, `-symbol`, and `-variadic-from` follow `call` and precede the signature, just as with `ddlgo`. The TLS flags select the library's supported raw TLS modes; OS-loaded libraries manage their own TLS.
