# LLVM IR and bitcode compilation

`compiler/llvm.Compile(ctx, input, options)` optionally invokes an external
`llc` to generate PIC object code. It snapshots a regular input file, validates
the output with the Go object parser and returns an owned `Object` containing
`Path` and `Info`. It does not add LLVM libraries to the core loader or start
a JIT. The compiler executable defaults to `llc`; set `Options.Compiler` to a
different installed version or path. `Options.TempDir` selects temporary storage.

The module supplies its target triple and data layout. The helper passes no
target override. A target-less module uses the LLVM tool's default target,
which may differ from the Go process, especially for 386 hosts using 64-bit
tools. Prefer explicit module targets. LLVM versions must be compatible with
the input producer. These behaviors and object emission are provided by the
[LLVM static compiler](https://llvm.org/docs/CommandGuide/llc.html).

## Coverage

Every native Go and llgo CI job compiles both text IR and bitcode produced by
its Clang toolchain, loads the resulting object, removes compiler files before
linking, and executes C exports and an initializer. Foreign-target tests inspect
all eight object targets on each installed LLVM backend; they do not execute
foreign code.

`DYLIB_LLC` selects the test compiler; `DYLIB_LLVM_CLANG` selects its IR/bitcode
producer (otherwise `CLANG` or `clang`). macOS Go jobs select a matched LLVM 22
pair without changing the C compiler used by the other native fixtures.

| Object target | Output | Go native call tests | llgo native call tests |
| --- | --- | --- | --- |
| Linux amd64 | ELF | Yes | Yes |
| Linux arm64 | ELF | Yes | Yes |
| Linux 386 | ELF | Yes | Unqualified |
| macOS amd64 | Mach-O | Yes | Yes |
| macOS arm64 | Mach-O | Yes | Yes |
| Windows amd64 | COFF | Yes | Yes |
| Windows arm64 | COFF | Yes, in the ARM64 llgo job | Yes |
| Windows 386 | COFF | Yes | Unqualified |

LLVM tools can generate other architectures and operating systems, but this
helper does not extend the loader's execution backends or relocation support.
ELF objects with `OSABI_NONE` do not uniquely identify their OS in `Info.OS`.
The compiler and execution tests remain separate from metadata inspection.

## Ownership and limits

Call `Session.Load(object.Path)` before `object.Close()`. The session snapshots
native object bytes, so compilation storage can be deleted before `Link` and
native execution. `Object.Close` removes only its owned directory and is
idempotent. Do not read the path concurrently with closing it. Session closing
and native code lifetime follow the normal loader rules.

The input snapshot is limited to 256 MiB and compiler diagnostics to 32 KiB.
Cancellation terminates the compiler process. Failed compilation or output
validation attempts to remove temporary files. If cleanup fails, `Compile`,
`CompileArchive` and `CompileModules` return a non-nil artifact along with the
joined compiler/validation and cleanup errors. That result owns failed cleanup
only: retry its `Close` after resolving the filesystem error; its `Path`/`Info`
are not a valid compilation result. Merge/archive failures retain the whole
owned tree, including failed member compilation storage. A compilation failure
whose cleanup succeeds returns no artifact. The executed examples close returned artifacts even on
error and report cleanup failures.

Portable Go/llgo tests force real cleanup failures after compiler failure: denied
directory access on Unix and an open file handle on Windows. Object, archive
member and module-merge cases preserve both errors, then remove retained storage
through repeated `Close` after permission/handle recovery.

No shell
interprets file names or compiler paths. Successful object output is inspected
under the existing parser limits. Unsupported features can still be reported
in `Info.Unsupported` or rejected when linking.

`Object.SourceDirectories` retains unique absolute input directories in input order,
including after `Close`. Use them explicitly with `dylib.Options.LibraryPaths` for original dependencies;
the compiler's temporary directory is the staged object's default search origin.
The [tested library example](../examples/llvm/main.go) makes that path explicit.
It runs through `examples/run.sh <go|llgo> llvm <module.ll|module.bc>` and uses a
declared C signature rather than inferring one from IR symbol names.

## Language and container scope

LLVM output from C, C++ facades, Rust C exports, llgo C exports or another
frontend can use this path when its version, native target, ABI and required
runtime services match. Compilation does not supply missing runtimes, foreign
TLS, exception machinery, garbage collector registration or language ABI
adaptation. Existing object initialization, imports, unwind and relocation
restrictions still apply. Keep exports at a supported C boundary.

Compile modules or archives explicitly. `Session.Load` still does not directly
load IR, bitcode or archives containing bitcode members. Only `CompileModules`
merges modules explicitly; the other helpers retain separate object members.
They do not infer signatures, generate language facades or translate an
already lowered ABI between operating systems.

## Bitcode archive compilation

`CompileArchive(ctx, path, options)` converts ordinary GNU/SysV, COFF and BSD, and GNU thin/proxy
archives containing raw/wrapped LLVM bitcode, native objects, or both. Each
bitcode member goes through `Compile` independently. Native members retain their
bytes, even when they contain embedded bitcode sections; this is not an LTO
pipeline. Native-only archives do not require an installed compiler.

The output is an owned `Archive` with `Path`, `Info` and idempotent `Close`.
Load its path into a session, close compilation storage, then link requested
roots. Members retain order and names, including duplicates and long names.
Compilation does not interpret names as filesystem extraction paths.

The loader rebuilds native symbol indexes and selects members on demand. Unused
members' unresolved native symbols and initializers remain unselected, while
all member contents must be valid for compilation/inspection. Heterogeneous
native targets can be inspected; execution still checks each selected member.
The input's bitcode index is discarded. The generated BSD-style ar has no ranlib
index; it targets this loader, and external static linkers may require indexing.

Input and generated archives each have a 256 MiB limit, including headers and
names. For thin inputs, the container plus unique external reads has the input
limit; decoded member bytes (including repeated references) have another
256 MiB limit. A referenced regular archive is charged at its full size.
At most 4096 object members are decoded per container, including proxy archives.
Context cancellation stops further external reads/members and terminates an
active compiler. Any failure removes owned files. Nested/recursive archives and
text IR archive members are rejected. Use `CompileModules` for explicitly selected modules.

`Archive.SourceDirectories` contains unique absolute original member directories
in member order, adding the input container's directory if absent. It remains available after `Close`.
Set `dylib.Options.LibraryPaths` explicitly with these directories or your own
search policy. Compilation does not copy DLLs or automatically preserve
per-member search origins in the output archive. If different source directories
contain DLLs with the same basename, choose the intended library explicitly.
The [tested archive example](../examples/llvmarchive/call.go) passes these paths
to the session. Native Go and llgo tests use genuine GNU/BSD/thin archives with
mixed native/bitcode members, duplicate names, dependencies in reverse order,
selected/unused initialization, and source/artifact deletion before linking.
They run on the same execution targets in the coverage table above. Tests delete
external thin members before loading the compiled archive, resolve regular
archive proxies by validated header offsets, and execute Windows DLL exports
whose import members live in another directory. Metadata tests preserve all
eight targets in ordinary/thin/proxy inputs and exercise raw/wrapped bitcode.

Thin containers validate their full headers before external I/O, snapshot each
path once, retain member order and interpret relative paths from the original
container. Missing or invalid external members fail even if unused. The source
files can be removed after successful compilation; required DLLs must remain
available for the later native load. The core native thin loader continues to
retain per-object search origins independently of this compilation helper.

References: [LLVM archive containers and bitcode indexes](https://llvm.org/docs/CommandGuide/llvm-ar.html),
[LLVM bitcode wrappers](https://llvm.org/docs/BitCodeFormat.html#bitcode-wrapper-format).

## Explicit module merging

`CompileModules(ctx, inputs, MergeOptions{Options: options, Linker: linker})`
snapshots an explicit list of text IR or raw/wrapped bitcode modules, invokes
`llvm-link`, then emits one PIC native `Object` through `Compile`. The linker
defaults to `llvm-link`; select a version compatible with the producers and
`Options.Compiler`. It uses the same optional external-tool model as `llc`.

Each module is validated independently by LLVM before any cross-module link.
Every module must declare a nonempty target triple and data layout. The helper
compares their exact canonical header strings, rejects mismatches, and checks
the merged headers again. It does not silently choose the first module's target
or override another module's ABI. Equivalent-looking triples or layouts with
different strings are conservatively rejected. LLVM can upgrade legacy bitcode
layouts during reading; use compatible producers and explicit current layouts.

Merging is eager: all listed modules contribute their definitions and initializers
to one object. LLVM handles internal symbol renaming and reports duplicate strong
definitions. This differs from `CompileArchive`, whose separate members retain
lazy extraction. Archives and native objects are not module inputs. Callers must
select modules explicitly; there is no automatic LTO archive selection, runtime
linking, signature inference or translation between language calling conventions.

The limits are 1–256 modules, 256 MiB of combined input snapshots, 256 MiB of
combined normalized IR, and 256 MiB of merged IR. Diagnostics stay bounded to
32 KiB per process. All paths become owned indexed snapshots before any LLVM
process starts; relative indexed arguments keep command lines bounded on Windows.
Cancellation terminates active linker/compiler processes. Failures remove the
whole owned tree; `Object.Close` removes successful snapshots, merged IR and object
storage together. Caller inputs and dependency libraries are never removed.

The [tested library example](../examples/llvmmodules/main.go) uses the original
`Object.SourceDirectories` explicitly for dependency search and closes compilation
storage before execution. Run `examples/run.sh <go|llgo> llvmmodules <module>...`.
Native tests on all execution targets above merge Clang-produced text/bitcode,
execute cross-module calls and initialization, preserve distinct internal
functions with the same name, and delete source/artifact files before linking.
Metadata tests preserve all eight targets; failure tests cover missing/conflicting
headers, duplicate definitions, invalid inputs, budgets, and active linker/compiler
cancellation.

Reference: [LLVM bitcode linker and linkage options](https://llvm.org/docs/CommandGuide/llvm-link.html).
