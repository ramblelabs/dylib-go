# GitHub CI

Go and llgo use independent workflows and separately compiled test processes:

- [Go workflow](../.github/workflows/go.yml): `actions/setup-go@v7`, Go 1.27.x.
- [llgo workflow](../.github/workflows/llgo.yml): `xgo-dev/setup-llgo@v0.2.0`, llgo `main` built from source, Go 1.27.x, LLVM 22; Windows uses the MinGW profile. Each job records the resolved llgo revision in its summary.
- [README workflow](../.github/workflows/readme.yml): `actions/setup-node@v7`, Node 24, and lockfile-pinned embedme 1.22.1; verifies embedded source matches the README.

Workflows run on pushes to main, pull requests, and manual dispatch. Pure Go jobs set `CGO_ENABLED=0` and run parser, error, and execution-refusal tests in a process compiled for the target architecture.

Native Go suites allow 20 minutes per test package within their 40-minute jobs.
macOS Intel allows 30 minutes per package within a 90-minute job: its complete
compiler qualification package already takes about 19 minutes on a passing run,
leaving too little margin under the former 20-minute limit. Both ordinary and
race suites still run in full.
The llgo suites allow 35 minutes per package within 75-minute jobs; macOS Intel
has a 120-minute job budget. Its expanded compiler qualification matrix exceeded
a 60-minute single-process budget while tests were progressing. Intel macOS now
compiles that test binary once, enumerates its real top-level tests, and executes
four disjoint batches with 35-minute limits. Every test runs once; fresh processes
bound retained compiler-probe state without a manually maintained test list.
The full compiler matrix runs once with libffi. Direct calls in all other packages
also run without libffi, together with the compiler's record, callback-field and
metadata checks that exercise backend-unavailable behavior. This preserves both
backend modes without repeating the expensive producer/layout matrix.
Layout and calling-ABI checks reuse parsed headers where possible. The independent
Windows ARM64 Go process keeps its 20-minute package budget. All probes still run.

| Target | Go execution | Pure Go inspection | llgo execution |
| --- | --- | --- | --- |
| Linux amd64 / arm64 | Native runners | Native runners | Native objects and libraries |
| macOS amd64 / arm64 | Intel / Apple Silicon runners | Same runners | Native objects and libraries |
| Windows amd64 | Native runner, Clang/MinGW | Native runner | Native objects and DLLs, MinGW |
| Windows arm64 | Independent gc process tests objects/archives/DLLs and scalar/struct ABI; reuses llgo job's setup-go and C tools | Native ARM64 runner | Objects/archives/DLLs, scalar/struct ABI, and Go/llgo producers |
| Linux 386 | Objects/archives/ELF32 libraries, scalar/struct ABI, and gc c-shared producer; multilib Clang and i386 libffi | 32-bit Go process on amd64 Linux | Not qualified; setup-llgo does not install 386 |
| Windows 386 | Objects/archives/PE32 DLLs, scalar/struct C cdecl ABI, and gc c-shared producer; i386 toolchain and libffi under WoW64 | 32-bit Go process on amd64 Windows | Not qualified; setup-llgo does not install 386 |

All eight Go execution targets and six llgo targets set `DYLIB_TEST_REQUIRE_NATIVE=1`, `DYLIB_TEST_REQUIRE_SHARED=1`, and `DYLIB_TEST_REQUIRE_TOOLS=1`, failing if a required backend or tool is missing. Windows ARM64 runs independent Go and llgo object/archive/DLL suites. Clang generates i386 ELF/COFF and ARM64 COFF fixtures for target-boundary tests: matching hosts accept them, foreign hosts reject execution, and unsupported 32-bit Mach-O is rejected.

## Languages and calling adapters

| Interface / producer | Go host | llgo host |
| --- | --- | --- |
| C, C++ without exceptions/RTTI, archives, libraries | Eight raw-object targets | Six raw-object targets |
| CLI/compiler-probe example `int32(int32,int32)` | `examples/call` cgo adapter | `examples/call` direct C function-pointer adapter |
| Caller-defined `double(int32,double,float,uint64)` | `examples/cgo` on all eight library targets | `examples/llgo` on all six library targets |
| libffi mixed integer/floating-point signatures, variadic promotions/records/pointers, reusable plan lifetime | Eight Go targets; race checks on the five Go native amd64/arm64 jobs (Go race does not support 386) | Six targets; raw COFF constant-pool COMDAT exercised |
| C++ inline COMDAT/shared state from objects and archives | Eight targets | Six targets |
| Explicit amd64 Microsoft x64 / System V | O0/O2 objects/archives/libraries: mixed scalars, narrow/wide integers, bool/pointers, register spills, mixed/large structs, native factories and callback reentry; separate generated C bridges also execute both conventions on Windows where libffi lacks SysV64 | Same native ABI fixtures and generated bridges on amd64; non-amd64 hosts reject explicit selectors and compile declaration metadata |
| Windows 386 stdcall/fastcall/thiscall fixed calls and callback entries | MSVC/GNU thiscall fixtures at `-O0`/`-O2`: objects/archives and GNU DLLs, mixed widths/floats, receiver mutation, temporary receivers, native entry factories and nested Go/C callbacks; separate MSVC/GNU generated bindings execute hidden struct results without libffi, and native Clang callers exercise both caller targets against each producer. Native C fixtures; generated cgo bridges also execute mixed scalars, decorated exports and same/opposite-convention native function-pointer factories/consumers with and without libffi | Not qualified |
| Windows i386 C/C++ DLL imports | Native lld-link DLL/import libraries with cdecl/stdcall/fastcall/thiscall/vectorcall, ordinary/underscore-leading identifiers, primitive C++ functions/data and direct mangled calls, repeated object/archive consumer calls at `-O0`/`-O2` and retirement; MSVC/GNU name inspection on every host | Same name inspection; native llgo 386 unqualified |
| Explicit bounded COFF import directories | MSVC/GNU six-target metadata, ordinary/bigobj records, reversed order and independent LLVM archive/LLD table controls; Windows amd64/arm64/386 execute raw sets and index-selected regular/thin archive mixtures, named/data/ordinal imports, preserved IAT origins, failed staging, dependency retry, deleted snapshots and COMDAT/lifecycle selection at `-O0`/`-O2` | Same native execution on Windows amd64/arm64; pure Go metadata and selected thin-snapshot budgets on all hosts |
| Explicit auxiliary COFF bound metadata | Pure Go standalone and raw-object decoding; MSVC/GNU AMD64/ARM64/i386 regular/bigobj producers, nonzero roots, renamed sections, rejected ranges/relocations and independent linked PE comparison; shared PE-wide budgets and fuzz seeds | Same metadata tests on all native llgo hosts; no DLL execution required for inspection |
| Internal delayed-call entry boundary | Independent three-architecture assembler/unwind byte controls; Windows amd64/arm64/386 typed scalar/record/pointer/variadic calls, x86 conventions, native thread allocation/GC, SDK exception recovery and terminal subprocesses; x64/ARM64 actual Windows virtual-unwind checks | Same Windows amd64/arm64 execution and public callback boundary; portable emitter controls on all hosts |
| Image-owned lazy COFF imports | Portable cache/hook/reference/reentry/unload-gate tests; Windows amd64/arm64/386 raw object/archive/cross-archive calls, real GNU amd64/386 archives, mixed ABI consumers, reset/reload, C-thread allocation/GC, Go failure hooks, SDK recovered reference ownership, initialization/finalizer-time first resolution, real DLL-detach reentry and provider cleanup retry at `-O0`/`-O2` | Same Windows amd64/arm64 native execution, plus independent Go ARM64 host; llgo 386 unqualified |
| Raw COFF delay imports | Pure Go GNU/compatible-template decoding and strict rejection; Windows amd64/arm64/386 eager object/archive/cross-archive calls, retained ordinary/COMDAT code and lifecycle, default/unused policy, provider/dependency retry and independent system-linked lazy controls at `-O0`/`-O2`; genuine GNU archives execute on amd64/386 | Same Windows amd64/arm64 execution; 386 uses Go |
| Fixed C callbacks: scalars/structs, errors, captures, leases, C-created threads with allocation/GC | Eight targets; object/archive/library and native worker fixtures | Six targets; collector-aware foreign-thread entry |
| Foreign ELF/COFF COMDAT discard; COFF SECTION/SECREL; weak external search/aliases | Parser/relocation fixtures independent of host; Windows executes weak overrides and EXACT_MATCH/NEWEST selection | Same fixtures and native Windows execution |
| COFF high section indexes, bigobj, and extended relocation tables | Foreign fixtures on every host; Windows executes objects/archives and checks the final extended relocation | Same fixtures and Windows AMD64/ARM64 execution |
| Concurrent calls and callback reentry | Pure lifetime/retirement tests; native recursive same-plan calls, parallel scalar/struct-pointer calls, close during callbacks, finalizer observations, and C-thread nesting | Same native execution cases on six hosts |
| Initialization callback reentry | Scoped raw object/archive symbols and prior OS-library/provider symbols, nested Go/C calls, busy errors before publication, concurrent close and failed-XI scope ownership on eight native libffi jobs | Same native execution cases on six hosts; initialization itself needs no libffi |
| Mach-O short/padded symbol stubs | Four widths per architecture, O0/O2 objects/archives/retained providers, adjacent targets, scalar and indirect-record results, integer/floating register exhaustion; portable distant-island and alignment checks on all targets | Same native macOS execution and portable checks |
| Universal Mach-O | FAT32/FAT64 foreign inspection and malformed/subtype cases; macOS executes real lipo objects/archives and dylibs with architecture-specific results | Same macOS execution cases |
| Thin archives | Foreign object/proxy inspection, path origins, validation, read budgets and retry; genuine GNU/LLVM archive calls, deleted-file snapshots and proxy execution on eight hosts; Windows DLL origins | Same native cases on six hosts |
| Optional LLVM compiler | Text IR/bitcode native calls; explicit merged modules with cross-module calls, initialization, private symbol isolation, target/layout rejection and linker/compiler cancellation; ordinary/thin/proxy mixed archives with lazy selection, cleanup and README examples; Windows source DLL directories | Same native cases on six hosts; independent Go process on Windows ARM64 |
| Windows vectorcall compiled bridges | Fresh no-libffi Go amd64/386 callers; `-O0`/`-O2` objects/archives/DLLs, narrow/wide integers, floating register/stack exhaustion, ordinary/HFA/large records, native pointer factories/forwarding, decorated/underscore-leading names and retirement; unsupported compilers fail the build | Same native calls on Windows amd64; ARM64 and llgo 386 unqualified |
| C declaration generation | Fresh Clang AST compared with committed target-qualified Go declarations; scalar/float/Boolean/pointer/variadic calls, record layout validation, struct returns and pointer mutation; padded/nested/array/floating-point/large records, variadic record prefixes and mismatch rejection; C long/char probes; generated scalar/record/pointer callbacks and native/variadic function-pointer factories; Windows 386 conventions | Same generated/parsed native cases on six hosts; independent Go process on Windows ARM64 |
| Direct llgo declaration generation | Go parser/generator validation and rejection tests | Fresh six-target normal/packed output; scalar widths, float/Boolean/native pointers, zero/void, padded/nested/array records, floating-point and large returns, typed pointer mutation/identity, scalar/record/large/pointer native factories, C-invoked function pointers, noncapturing same-thread Go C entries, packed layout rejection, closed/nil handles and the executable library example without libffi. Objects, archives and libraries execute concrete variadic tails, C promotions, register/stack overflow, record prefixes/results and returned/forwarded variadic native entries |
| Typed cgo declaration generation | Fresh eight-target output; all scalar widths, Boolean/native pointers, zero/void, fixed/variadic mixed calls, promotions and native factories. Objects/archives/libraries at `-O0`/`-O2` execute record values, nested arrays, floating/large returns, native address fields, record/record-pointer function pointers, native struct-pointer mutation/identity/nulls and variadic record prefixes/results. Tagged tests integrate owned native storage. C layout checks reject incompatible packing flags; nil/closed/target validation and examples run without libffi | Same native cases on six hosts, including the no-libffi test suite; independent Go process on Windows ARM64 |
| C enums | Eight-target default/short type probes; Go cgo scalar and function-pointer fixtures run both integer and enum producers; dynamic calls at `-O0`/`-O2` check signed/unsigned boundaries, Boolean values, arrays/records, captured callbacks, native factories and variadic prefixes | Same dynamic/cgo fixtures on six hosts, plus direct enum scalar/record/function-pointer calls and `-O0`/`-O2` object/archive/library execution without libffi |
| Packed C members | Fresh eight-target bindings and `-O0`/`-O2` object/archive/library execution; packed arrays/addresses, nested arrays, explicit field alignment, signed boundaries, native factories, pointer mutation/nulls and concrete variadic calls | Same native C bridges on six llgo hosts without libffi |
| Explicit packed C records | Fresh eight-target CgoSource output; `-O0`/`-O2` object/archive/library calls verify values/returns, ordinary parents with packed multidimensional arrays, signed boundaries, Boolean/address fields, native factories, struct pointer mutation/nulls and concrete variadic promotions; pure Go checks layout metadata, rejection by dynamic/direct adapters and equivalent record declarations | Same native calls on six llgo hosts without libffi; independent Go process on Windows ARM64 |
| C bitfields and padding | Eight-target compiler mask probes and fresh generated bindings; `-O0`/`-O2` object/archive/library calls cover signed/Boolean and cross-byte fields, unnamed/zero-width boundaries, leading/trailing padding, floating fields, packed combinations, nested arrays, native pointers/factories and concrete variadic promotions; shifted masks are rejected before symbol resolution | Same native calls on six llgo hosts without libffi; independent Go process on Windows ARM64 |
| Named member-packed bitfields | Eight-target layout/mask probes, MSVC/MinGW allocation-unit comparisons, and real small/large record calls, packed/aligned members, nested records, native pointers/factories and variadic prefixes at `-O0`/`-O2`; Windows 386 also runs GCC-host bridges | Same native fixture paths on six llgo hosts without libffi |
| Member-packed bitfields with unnamed padding | Eight-target masks/layouts, Windows MSVC/MinGW comparisons for leading/trailing padding, consecutive zero-width fields, base-type changes and explicit alignment; native nested arrays, pointers, factories, variadic calls and small returns at `-O0`/`-O2` | Same native fixture paths on six llgo hosts without libffi |
| Explicitly packed unnamed/zero-width bitfields | Eight-target layout/mask probes, metadata snapshots, MSVC/MinGW packing comparisons, and real padding-only allocation units, nested records and packed zero-width small-record calls at `-O0`/`-O2` | Same native fixture paths on six llgo hosts without libffi |
| Aligned unnamed/zero-width bitfields | Eight-target numeric/omitted alignment and bit-position probes, MSVC and GNU producer bridges on three Windows architectures, ignored shared/consecutive requests, floating aggregates and packed/padding-only units; fresh generated object/archive/library calls at `-O0`/`-O2` | Same native fixture paths on six llgo hosts without libffi |
| Nominal C record identities | Eight-target metadata, aliases, nested arrays, typed pointers/function pointers, JSON and LLVM call shapes; fresh `-O0`/`-O2` object/archive/library calls execute same-field ordinary/packed/aligned values, factories, pointers and variadic prefixes; Darwin ARM64 adds unequal unnamed-field layouts | Same native fixture paths on six llgo hosts without libffi; dynamic shared plans retain distinct logical result identities |
| Ordinary pragma-packed C records | Eight-target packing caps 1/2/4/8/16, nested push/pop and record alignment; Windows MSVC/GNU size/alignment/offset comparisons; guarded text-AST decoding and ABI ambiguity rejection; fresh object/archive/library calls at `-O0`/`-O2` cover arrays, natural outer layout, native pointers/factories/function-pointer fields and variadic prefixes/results | Same native fixture paths on six llgo hosts without libffi |
| Ordinary over-aligned pragma members | Eight-target numeric/omitted/type requests, packing caps 1/2/4/8/16, packed records, nested aligned arrays, floating aggregates and function-pointer members; MSVC/GNU bridge layout comparisons and cloned/generated storage alignment metadata; fresh `-O0`/`-O2` object/archive/library calls check factories, native pointers, mutation/nulls, variadic promotions and MSVC i386 indirect arguments | Same native fixture paths on six llgo hosts without libffi |
| Global Clang packing defaults | Eight-target caps 1/2/4/8/16; response files, forwarded/numeric overrides, bounded driver inspection, MSVC oversized pragma rules and MinGW layout/mask checks. Fresh `-O0`/`-O2` object/archive/library calls execute nested arrays, alignment/floats, named/zero-width bitfields, Boolean/native addresses, pragma override/pop, factories/function-pointer fields, pointer mutation/nulls and variadic prefixes/results | Same native fixture paths on six llgo hosts without libffi |
| Pragma-packed bitfields | Eight-target named/unnamed/zero-width layouts with caps 1/2/4/8/16; MSVC/MinGW byte-mask and layout comparisons; object/archive/library calls at `-O0`/`-O2` cover small/large returns, packed/aligned unnamed fields, inactive/active/consecutive/trailing boundaries, floating aggregates, nested arrays, native pointers/factories/function-pointer fields and variadic prefixes/results | Same native fixture paths on six llgo hosts without libffi |
| Over-aligned pragma bitfields | Eight-target caps 1/2/4/8/16, named/shared/unnamed unit requests, active/ignored zero widths, packed records, ordinary aligned fields and nested arrays; MSVC/MinGW/GNU layout/mask comparisons, GNU LLVM call-shape checks, independent GCC bridges and malformed storage/gap metadata rejection. Fresh `-O0`/`-O2` object/archive/library calls cover small/large/floating returns, native pointer mutation/nulls, factories/aligned entry fields and variadic promotions | Same native fixture paths on six llgo hosts without libffi |
| Compiler-evaluated C alignment | Eight-target numeric/type-based member requests, omitted GNU record/member alignment and fresh generated bindings; numeric equivalence and missing-probe guards; `-O0`/`-O2` object/archive/library calls cover over-aligned values, packed/bitfield combinations, nested arrays/large returns, floating aggregates, native address alignment, factories and variadic prefixes/results; MSVC i386 argument probes reconcile MinGW cdecl/stdcall/fastcall/thiscall callers; incorrect alignment is rejected before symbol resolution | Same native calls on six llgo hosts without libffi; independent Go process on Windows ARM64 |
| Aligned typedef bitfields | Eight-target layouts and masks, MSVC/MinGW and GNU producer comparisons, default packing, LLVM argument/result shapes, independent GCC checks including Windows 386, and malformed metadata rejection; fresh `-O0`/`-O2` object/archive/library calls cover raised/lowered aliases, shared units, member packing/alignment, signed/Boolean/enums, nested arrays, variadic prefixes, preceding arguments, native factories and pointer mutation/nulls | Same native fixture paths on six llgo hosts without libffi |
| Aligned unnamed typedef bitfields | Eight-target layouts, MSVC/MinGW bridges, LLVM calling shapes and independent GCC checks; fresh `-O0`/`-O2` object/archive/library calls cover raised/lowered aliases, Boolean/enums, active/ignored/consecutive zero-width fields, packing caps, nested arrays, SSE overflow, variadic prefixes, preceding scalars, native factories and pointer mutation/nulls; unsupported SysV gaps and malformed metadata are rejected | Same native fixture paths on six llgo hosts without libffi |
| Aligned record typedefs | Eight-target body/alias layout and LLVM calling-ABI checks, MSVC/MinGW comparisons, default packing and an independent GCC bridge; fresh `-O0`/`-O2` object/archive/library calls cover raised/lowered/inherited requests, small/large returns, nested arrays, packed/pragma/member alignment, factories, aligned native pointers/nulls, exhausted registers and variadic tails. | The same compiler checks and native executions on six 64-bit hosts, including committed binding freshness. |
| Anonymous aligned record typedefs | Eight-target synthetic body/original size, offset, alias alignment and bitfield mask comparisons; LLVM argument/result lowering, MSVC/MinGW and GCC checks; fresh O0/O2 object/archive/library calls cover inherited/raised/lowered aliases, packed/pragma/member attributes, nested arrays, float/double and large returns, factories, aligned pointers, register overflow and variadic tails. | The same compiler probes and actual native calls on six 64-bit hosts. |
| Plain GNU zero-width pragma boundaries | Eight-target layout/mask and LLVM calling-shape checks, compiler default packing, independent GCC bridge checks and conservative SysV rejection. Fresh `-O0`/`-O2` object/archive/library calls cover lowered record members, float/HFA values, leading/trailing/consecutive and non-power-of-two boundaries, packed/large/nested records, native factories/pointers/nulls, register exhaustion and variadic tails. | The same compiler checks and native executions on six 64-bit hosts without libffi. |
| GNU AMD64 unnamed tail padding | Eight-target layouts, LLVM argument/result shapes, independent GCC layout checks and unqualified-shape rejection; fresh `-O0`/`-O2` object/archive/library calls cover float/double/integer values, nested arrays, exhausted registers, following stack arguments, variadic promotions, factories and native pointer mutation/nulls | Same fixture paths on six llgo hosts without libffi |
| Darwin ARM64 plain unnamed fields | 585 compiler layouts and LLVM calling shapes, original metadata retention and invalid extent rejection; Darwin ARM64 executes independent Clang definitions through generated GCC bridges at `-O0`/`-O2`. Fresh object/archive/library calls cover scalar/native-address fields, nested arrays, variadic prefixes and records, exhausted registers, factories and native pointers | The same compiler matrix, GCC calls and Darwin ARM64 native fixture without libffi |
| Native layout queries | Actual C `sizeof`, `_Alignof` and `offsetof` compared with backend layouts from objects/archives/libraries: all scalar widths, Boolean/pointers, padded/nested records and arrays/matrices; independent results, limits and conventions | Same native cases on six hosts; independent Go process on Windows ARM64 |
| Owned native storage | Scalar/record/array round trips, snapshots, stable/interior addresses, invalid writes, concurrent reads/writes and lease retirement. C objects/archives/libraries verify layouts, retain pointers across GC/calls, mutate data and invoke a Go reader; executable retained-record example | Same native cases on six hosts; independent Go process on Windows ARM64 |
| Retained registrations | Acquisition rollback, independent code leases, concurrent/reentrant operations, stop failure/panic retry; object/archive/library C registrations retain records/callbacks during owner retirement; native OS thread join precedes code/data/entry release; executable registration example | Same native cases on six hosts; independent Go process on Windows ARM64 |
| Retained symbol dependencies | Definition rollback, zero-value owners and concurrent close; raw object/archive/library providers supply functions/data while provider and consumer retire; native finalizers and failed-initializer exit callbacks execute before dependency release; executable import example | Same native cases without requiring libffi; independent Go process on Windows ARM64 |
| Unpublished image rollback | Real native cleanup refusal after primary link failure, joined errors, no image publication or lifecycle execution, retained libraries/provider leases, blocked mutation and repeated `Close`; POSIX mapping-release refusal and Windows locked TLS carrier deletion; successful rollback permits a real initialized call | Same native ownership cases without libffi; independent Go process on Windows ARM64 |
| Session-owned caches | Different symbol addresses share a plan; distinct logical signatures share compatible native resources; concurrent calls, cross-plan callback reentry, independent retirement, failure isolation, snapshots, logical metadata, raw oversized pointees, layout reuse and allocation rollback | Same native cases on six hosts |
| Dynamic argument storage | 64 scalar/struct arguments and callback parameters; variadic totals 1/32/33/64, zero arguments, count mismatch, and declaration/invocation parsing | Same native cases on six hosts; Go covers 386 |
| COFF long imports | Foreign-target template validation and genuine GNU Binutils fixtures; Windows executes GNU function/data/ordinal imports and MinGW consumers on AMD64/ARM64/386 | Same fixtures and Windows AMD64/ARM64 execution |
| GNU i386 compatibility imports | Genuine `--compat-implib` inspection, same-slot name/ordinal/data aliases, ordinary/bigobj mixed/COMDAT members and conflicting-alias rejection. Windows Go 386 executes both original IAT spellings at `-O0`/`-O2`, retained/discarded lifecycle, raw mixed members, lazy archives, source removal and dependency retry | Same portable inspection/validation; llgo 386 remains unqualified |
| GNU i386 compatibility delay imports | Shared single-slot name validation, pure/mixed/bigobj snapshots and conflicting-alias/thunk rejection. Independent PE controls at `-O0`/`-O2`; Windows Go 386 runs both original spellings through the OS helper and explicit raw eager binding, default rejection, mixed object/archive/cross-archive calls, retry, unused members and retained/discarded lifecycle | Same portable validation and PE-link controls; llgo 386 remains unqualified |
| Mixed COFF long imports | All three targets: retained ordinary code/data/BSS/lifecycle and COMDAT groups, stable indices, import metadata, selections 1–7, associations, relocation discard and rejection boundaries. Native Windows AMD64/ARM64/386: DLL calls, ordinary dependencies, retry, retained/discarded associative initialization/finalization, unused missing DLL and retirement | Same Windows AMD64/ARM64 execution, plus independent Go on Windows ARM64 |
| Multi-entry COFF long imports | Three-target function/data/const/name/ordinal metadata, contiguous/shared-descriptor tables, optional ILT, mixed/COMDAT retention, duplicate thunk sites and malformed later-slot rejection. Windows AMD64/ARM64/386 executes pure and mixed archive calls at `-O0`/`-O2`, imported data, failed-link retry, retained/discarded lifecycle entries and lazy missing DLLs | Same Windows AMD64/ARM64 runtime cases, plus independent Go on Windows ARM64 |
| COFF short imports and DLL dependencies | All-target metadata policies; Windows executes genuine LLD `.lib` files, functions/data, name/ordinal imports, paths, handle reuse, and failure/retry | Same metadata and Windows AMD64/ARM64 execution |
| Rust `extern C`, Zig `export`, Fortran `bind(C)` | Both Linux/macOS architectures | Both Linux/macOS architectures |
| Rust fixed `win64` / `sysv64` | Linux/macOS amd64 O0/O2 objects, archives and libraries: mixed scalars, spills, struct/indirect results, native factories and callback reentry | Same amd64 execution with the optional libffi backend |
| Swift C-exported library and raw metadata refusal | Both macOS architectures | Both macOS architectures |
| Optional ABIBridge native Swift calls | Pinned native-core/ffi build; both macOS architectures execute unoptimized/optimized swiftcc scalars, pointers, mixed/padded/tail-size/large/indirect values, register spills, concurrent plans and close during native execution; real README examples, owned Swift metadata/copy/destroy, String/reference-counted members, empty values, leases, retirement and Go destructor callback reentry; Go race; no Go libffi dependency | Same native call/value cases and README examples through the direct C facade on both macOS architectures |
| Swift/ObjC++ opaque C facades | Both macOS architectures, unoptimized/optimized dylibs; null handles/observers, object retain/release, Go observer calls, destruction during owner retirement, cleanup retry and raw metadata refusal | Same native lifecycle cases through libffi on both macOS architectures |
| Generated C++ class facades | Eight-target C/C++ declaration compilation; fresh no-libffi cgo callers and dynamic libffi calls against `-O0`/`-O2` libraries, exceptions, const overloads, scalar widths, native pointers, generated multiple/virtual base views and secondary-base virtual dispatch; native class copies/references/pointers, noncopyable value results, overloaded `operator&`, copy exceptions, occupied owned slots, static-only classes, native function factories/round trips, constructor-retained captured callbacks, nested reentry during retirement, mixed-width/void/address callbacks, and compiler rejection of invalid base/object/function contracts | Same generation and actual caller execution on six native hosts, plus fresh direct llgo callers without libffi; dynamic cases run in the libffi suite |
| Selected C++ member discovery | Eight-target named/alias/template queries; namespace identity, forward declarations, access, overload/default/deletion metadata, abstract and target-dependent declarations; fresh cgo and dynamic libffi facade calls at `-O0`/`-O2` | Same metadata and native calls, plus freshly generated direct llgo callers without libffi |
| Versioned module graphs | Multiple roots, retained function/data aliases, object/archive/library providers at `-O0`/`-O2`, lazy unused members, version preflight, native failure rollback and finalizers, concurrent close with external observer leases; no libffi | Same native execution through direct llgo adapters |
| Provider catalog discovery | Pure Go directory/JSON/version/ambiguity/cycle/cancellation/budget contracts; separate provider/consumer catalog directories and exact-version object/archive/library graph calls at `-O0`/`-O2`, with missing native files for an unused version | Same discovered graph execution without requiring libffi, including independent Go on Windows ARM64 |
| Compatible provider versions | Explicit ordered acceptable versions, backtracking after later-root conflicts, dependency/root constraints, invalid-candidate fallback, ambiguity, cancellation and search budgets; actual selected object/archive/library calls at `-O0`/`-O2` | Same native graph and retirement checks through direct llgo adapters, including independent Go on Windows ARM64 |
| SemVer provider intervals | Strict grammar and precedence, prerelease opt-in, half-open bounds, stable build-metadata ties, exact dependencies, graph backtracking and search budgets. Distinct native provider results verify stable/prerelease selection, metadata fallback, retirement and native failure without fallback for objects/archives/libraries at `-O0`/`-O2` | Same Go-only selector and native calls through direct llgo adapters, including independent Go on Windows ARM64 |
| C union facades | Separately compiled providers/facades; objects, archives and libraries at `-O0`/`-O2`; scalar widths, pointers, floating bit patterns, packed/aligned/large unions, struct/array views, exact-size whole/nested union byte views, multidimensional/record/HFA/pointer elements, independent/shared wrapper headers, nested paths with zeroed neighbors, compiler contract failures and retirement | Same tests with generated cgo and direct llgo callers without libffi, plus dynamic binding |
| Raw C++ exceptions | Linux amd64/arm64/386 and macOS amd64/arm64 objects, reversed object order and lazy archives at `-O0`/`-O2`; integer/double throws, rethrow, RAII counts, native library throw, missing-root retry, FDE lookup/deregistration, retiring code leases, ten simultaneous images and macOS finder-slot exhaustion/retry; explicit libstdc++/libgcc on Linux and libc++/system libunwind with DWARF and compact unwind on macOS | Same native execution on Linux/macOS amd64/arm64 without libffi |
| Module replacement | Native state migration, failed builder/migration and panic cleanup, publication with old scopes/code leases, close during build/migration and retirement; no libffi | Same native old/new code execution and ownership checks |
| Go c-shared | Eight library targets, including both 386 hosts | Six library targets |
| llgo c-shared | Local macOS and CI Windows ARM64; other targets use llgo hosts | Six library targets |

These are workflow requirements; inspect [Go](https://github.com/ramblelabs/dylib-go/actions/workflows/go.yml) / [llgo](https://github.com/ramblelabs/dylib-go/actions/workflows/llgo.yml) checks for the relevant commit's results. Each producer validates a sample C ABI subset. Windows Rust/Zig/Fortran, Linux Swift, and native ObjC ABI are outside this matrix.

`DYLIB_TEST_LANGUAGES=rust,zig,fortran,swift,objc,go,llgo` selects producers explicitly. A selected compiler that is missing or inappropriate for the host fails. Override executable paths with `DYLIB_RUSTC`, `DYLIB_ZIG`, `DYLIB_FC`, `DYLIB_SWIFTC`, `DYLIB_OBJCXX`, `DYLIB_GO`, and `DYLIB_LLGO`. Go/llgo library calls run in child processes that retain their runtime library references until exit, allowing Windows to remove DLL files afterward. Apple object-facade tests keep runtime images pinned while explicitly destroying object references and retiring their Go owners.

`bash examples/run.sh go library|llgo` delegates to `scripts/verify-examples.sh`, which builds a complete C library, independently compiles and executes the appropriate typed adapter (`examples/cgo` or `examples/llgo`), and then runs the dynamic-call, callback, array, native-value and registration examples with `-tags libffi`. Each program must print exactly 42. The examples use complete C libraries; separate native tests exercise raw COMDAT selection. The six amd64/arm64 targets execute the typed adapter, dynamic call, and Go capture callback examples with each host compiler; both 386 targets also execute them with Go; the Go Windows ARM64 examples run in the llgo workflow's independent Go step.

`bash examples/run.sh go quickstart|llgo` runs the Linux/macOS object/archive smoke test in `examples/readme/quickstart.sh`. Both compiler workflows execute it on amd64/arm64, and Go also executes it on Linux 386, checking object and archive results and exercising no-cgo inspection. Windows raw-object execution is verified separately by the native test suite and CLI script.

`scripts/verify-cli.sh go|llgo` independently compiles the dynamic CLI with `-tags libffi`. All eight Go native library targets, and six llgo targets, execute Go-style declarations and typed invocations with mixed scalars, small integers, C booleans, integer boundaries, native/temporary pointers, zero arguments, and void returns. Struct and array tests cover by-value struct arguments/results, integer/floating-point arrays, multidimensional arrays, arrays of structs/pointers, C-created callback records, variadic array members, nested padding, large returns, floating-point records, pointer fields, in/out mutation, alias identity, and temporary pointer lifetime checks. All eight raw targets execute scalar, struct, and array-member object/archive calls. The library example suite also builds and runs `examples/arrays` with Go and llgo. `bash examples/run.sh go dynamic|llgo` runs both README input forms through `examples/readme/dynamic.sh` on Linux/macOS. `bash examples/run.sh go structs|llgo` executes `examples/readme/structs.sh` on the six amd64/arm64 targets with both hosts and on both 386 targets with Go. Pure Go parser tests cover all eight inspection jobs, including 386.

README library code blocks are embedded from the executed Go source files under `examples/` with an `<!-- embedme ... -->` marker. The separate README job checks freshness with `npm ci --ignore-scripts` and `npm run readme:verify`; compilation and result checks remain in the native Go and llgo jobs. The short README fixture commands use the same `examples/run.sh <go|llgo> <example>` invocations as the native workflows. The CLI script also tests `examples/run.sh <go|llgo> call` with a struct declaration, a relative library path containing spaces from another directory, and a typed struct pointer loaded from an archive. Script implementations are linked, rather than embedded. To update a Go snippet, edit its source and run `npm run readme` before committing.

## Toolchain limits

[setup-llgo v0.2.0](https://github.com/xgo-dev/setup-llgo/tree/v0.2.0)'s [platform validation](https://github.com/xgo-dev/setup-llgo/blob/v0.2.0/src/platform.ts) accepts amd64/arm64 only, although the compiler has some 386 capabilities. This project provides native Go i386 backends for Linux ELF32 and Windows COFF/PE32, but does not claim llgo 386 execution. An installer or cross-compilation alone cannot qualify a native execution target.

On Linux, llgo v1.0.6 misparses newline-only `pkg-config --cflags libffi` output as `-`, causing Clang to read an extra stdin input and emit two AST JSON documents ([upstream issue #2749](https://github.com/xgo-dev/llgo/issues/2749)). Users of that older version can set `PKG_CONFIG_ALLOW_SYSTEM_CFLAGS=1` as a workaround. The qualified compiler revision includes the upstream fix, so CI no longer needs that setting.

Runner labels follow [GitHub's official list](https://docs.github.com/en/actions/reference/runners/github-hosted-runners): ubuntu-24.04, ubuntu-24.04-arm, macos-15-intel, macos-15, windows-2022, and windows-11-arm.

Older llgo revisions have a macOS ARM64 signed narrow-argument issue
([#2767](https://github.com/xgo-dev/llgo/issues/2767)). The qualified compiler
includes the merged [C ABI fix](https://github.com/xgo-dev/llgo/pull/2776), and CI
executes direct `LLGoSource` calls with negative narrow arguments. Generated cgo
bridges retain 32-bit integer transport and restore the C widths, with negative
arguments and variadic promotions checked at runtime.

Pure Go module tests cover provider ordering, concurrent close failure, failed load rollback ownership, and failed old/candidate/migration/panic cleanup retained by managers.

Native close-retry tests use a deterministic unregister-validation failure on every host. They verify retirement, active leases, concurrent/sequential error propagation, retained mappings/OS libraries/provider leases/TLS values, native finalizers running once and successful recovery. POSIX also forces a safe mapper slice-validation error to verify image-release retry. These run in Go/llgo, including independent Go on Windows ARM64.

Native C++ TLS fixtures execute on Linux amd64/arm64/386, macOS amd64/arm64 and Windows amd64/arm64/386 (Clang GNU/MSVC ABI) with `NativeTLS` alone. They share the emulated TLS destructor suite: O0/O2 object/archive construction/destruction, isolation, recursive/late registrations, budgets, failed initialization cleanup, pending/active retirement, Go callbacks during constructor/thread exit and native TLS reentry. Constructor retirement tests keep code leases until workers join. Cross-target tests verify native code generation and option isolation rejects emulated address helpers/descriptors.

Native ELF TLS fixtures execute Clang O0/O2 object/archive calls on Linux amd64/arm64/386, plus GCC O0/O2 calls. They reuse the native value/thread/lifecycle/retirement suite, compare ordinary and descriptor entries with independent assembly and check AArch64 descriptor register preservation. Portable tests reject malformed runtime/layout/relocation inputs; native tests exercise explicit dynamic models and reject initial/local-exec TLS.

Native Mach-O TLS fixtures execute on both macOS architectures in Go and llgo. They cover O0/O2 objects and lazy archives, initialized/zero/aligned storage, record/pointer templates, wide C calls, mixed emulated TLS, independent native and pinned Go threads, lifecycle/unwind, retained providers and retirement. Assembly probes verify the Darwin TLV register contract; both architectures also compare the Go-generated entry against an independent LLVM assembly reference on every CI host. Portable negative tests reject malformed descriptors, bootstrap overrides, unsupported section types and invalid TLVP instructions/addends.

Emulated TLS fixtures cross-inspect all eight targets plus Windows GNU ABI variants. Clang MSVC emulated TLS fixtures also execute thread initialization, plain destructors, shared guards across objects and constructor reentry. Native Go/llgo tests execute unoptimized/optimized object/archive C++ constructors and thread destructors, LIFO and recursive registration, reentrant TLS access, independent images, manual registration without descriptors, the 4096-entry thread budget, null callbacks, current-thread cleanup, pending/active callbacks during retirement and retry, and global/thread hooks registering each other. These cases run without libffi; the optional ABI suite also invokes captured Go callbacks during ordinary foreign-thread work and again from thread-exit destructors. Existing constant/zero/aligned TLS, provider retry, inline descriptors, native thread isolation, lifecycle and retirement tests remain covered. The independent Windows ARM64 Go process runs the same cases. See [TLS ownership](tls.md).

Raw lifecycle fixtures run in every native Go and llgo job, without requiring libffi: C++ dependency initialization/destruction, ordinary archive selection, C termination tables, `atexit`/`__cxa_atexit`, selective and recursive `__cxa_finalize`, two-image isolation, validation rollback, and close idempotence. A retained observer library verifies callbacks after raw-image release. Pure Go jobs cross-inspect the eight target lifecycle table formats. See [lifecycle coverage](lifecycle.md).

Linux jobs also execute real Clang `-fno-use-init-array` `.ctors/.dtors` output, including 386 Go. Tests cover reversed constructor entries, forward destructor entries, `0/-1` sentinels, normalized priorities mixed with modern arrays, C++ dependency/ordinary-archive selection, retry, and finalizer validation before initialization. All pure Go jobs inspect legacy ELF tables for amd64, arm64, and 386 and check malformed tables and priorities.

Linux native jobs execute independently assembled split `.init/.fini` fixtures and the installed system `crti.o/crtn.o`, with ordinary object and lazily selected archive bodies. Tests check preinit/startup/init ordering, fini/startup termination ordering, original archive positions, missing-epilogue retry and repeated close. Pure Go jobs inspect AMD64/ARM64/i386 fragments and check NOP gaps, invalid section storage, CET frames and paired ARM64 PAC/BTI forms.

Windows jobs execute COFF integer `.CRT$XI*` initialization before void `.CRT$XC*`, including native Go i386 cdecl. Positive/negative errors stop initialization, prevent repeated calls/publication, and retain registered-exit code until `Close`. Tests check dependency/root validation retry, invalid entries, and successful/failed cleanup. POSIX jobs exercise the shared integer-call and ownership machinery through a private test table, without claiming ELF/Mach-O integer-array support. libffi jobs run cleanup callbacks that reenter Go and verify `ErrClosed` without deadlock.

Windows amd64/arm64 Go and llgo jobs execute optional raw runtime function table registration. A DLL observer uses `RtlVirtualUnwind` through two real raw C frames, from objects and archives. Tests verify OS lookup, deletion after close, and ownership during failed C initialization cleanup. Pure Go jobs inspect real AMD64/ARM64 `.pdata/.xdata` producer records and validate malformed/unsupported metadata. See [unwind scope](unwind.md).

Windows AMD64 also executes two-level version 1 chained C unwind records from
objects and archives, with default loading, OS lookup for every range, owned
unregistration and validation retry. Pure Go jobs relocate genuine MSVC/MinGW
assembler metadata, check shared/empty/FAR chains, and reject malformed tails,
frame mismatches and cycles, including a 2048-level chain.

The Windows AMD64 suites also execute overlapping LLVM `.seh_startchained`
output, from objects and archives. Seven native contexts validate partial
prologs, nested saves, resumed parents and restored registers/stack/return PC;
`VirtualQuery` checks proxy page permissions. Pure Go tests check the normalized
intervals, frame metadata, siblings, shared ends, unchanged input bytes and limits.

Linux amd64/arm64 Go and llgo jobs, and native Go 386, execute optional ELF frame registration with libgcc, `_Unwind_Backtrace` through two raw C frames, and `_Unwind_Find_FDE` before/after close. Object/archive calls, default loading, validation retry and failed-initializer ownership are covered. Pure Go jobs inspect all three ELF producers and reject malformed CIE/FDE/CFI records.

macOS amd64/arm64 Go and llgo jobs execute optional per-FDE DWARF registration with system libunwind, real object/archive stack traversal, before/after-close FDE lookup, validation retry and failed initialization cleanup. Pure Go jobs inspect real `__eh_frame` compiler output and verify explicit ARM64 pairs and implicit x86-64 address rebasing.

CI pins merged llgo `main` commit `5926054cd0ae0e5a621557288e79d2c28bdb5fa2`, which includes the
[imported bound-method identity fix](https://github.com/xgo-dev/llgo/pull/2851), the
[channel wake lifetime fix](https://github.com/xgo-dev/llgo/commit/78b265a31),
[Darwin detached-thread state](https://github.com/xgo-dev/llgo/pull/2804),
[SysV aggregate register exhaustion](https://github.com/xgo-dev/llgo/pull/2822)
and [ARM64 HFA stack layout](https://github.com/xgo-dev/llgo/pull/2825), as well as
[public C-export foreign-thread guards](https://github.com/xgo-dev/llgo/pull/2752),
[indirect C variadic arguments](https://github.com/xgo-dev/llgo/pull/2766), the
[C ABI fix](https://github.com/xgo-dev/llgo/pull/2776), and the
[native function-pointer debug fix](https://github.com/xgo-dev/llgo/pull/2783).
The macOS ARM64 llgo job installs Homebrew's `lld` (23.x >= 23.1.3) and sets
`LDFLAGS=--ld-path=<lld prefix>/bin/ld64.lld` for SDK 27 native linking.
The compiler backend remains LLVM 22, as selected by `setup-llgo`.
`TestNativeDirectAggregateRegisters` executes caller-defined `//llgo:cdecl`
adapters through `Symbol.WithAddress` on all six native llgo hosts, without
libffi. `TestNativeDynamicAggregateRegisters` independently exercises libffi
on Go and llgo. Both use objects, archives and complete libraries at `-O0`/`-O2`,
covering exhausted/partial register banks, mixed integer/float rollback, nested
float arrays, four-double aggregates and hidden result pointers. These cases
exercise native calls, rather than only compiler output inspection.

The library uses ordinary `//export` declarations and has no private llgo runtime
hooks; v1.0.6 cannot qualify C-created-thread callbacks or these indirect variadic
calls. `setup-llgo` builds the upstream commit from source; the job summary records
that revision and the full matrix tests it without local compiler patches.

The macOS jobs also execute default compact unwind, omitted-frame-pointer and large-stack C++ variants. Each checks actual compact-table use, exceptions, RAII, concurrent images, registration failure/retry and retirement. All host jobs relocate genuine Clang compact records for both macOS architectures; malformed storage, range, LSDA, encoding and indirect stack-size instruction cases are rejected.

Linux amd64/386 TLSDESC fixtures use Clang GNU2 and GCC GNU2 at O0/O2.
`DYLIB_TLSDESC_CLANG` selects the qualified producer (the already installed
Clang 19 in CI); Clang 18 cannot generate x86 GNU2 output. Other fixture
producers keep their existing selection. Each
producer executes object/archive, pure descriptor and mixed GD/descriptor
images, on the caller thread and four joined C threads. The shared TLS lifetime
probes also cover pinned Go threads, retained providers, initialization/finalization
and owner-before-join/concurrent-exit retirement. Value fixtures retain
integer/FP arguments, aggregate results, initialized pointers and per-image
isolation; C++ fixtures trap if TLS destruction occurs after the global
finalizer. A hostile allocator independently checks every GPR, XMM and a full
x87 stack, both automatic XSAVE and forced FXSAVE; CPU-gated AVX/AVX-512 probes
check upper vector registers and masks. Cross-target encoding, relocation bounds
and malformed module-base tests run on every host.

AArch64 traditional ELF TLS fixtures execute GCC `-mtls-dialect=trad` at O0/O2,
with global-dynamic/local-dynamic producer options (both emit TLSGD page/add
pairs). Go and both llgo call paths load objects and archives, mix GCC GD with
Clang TLSDESC in either direction, verify one per-thread module allocation,
exercise foreign and pinned Go threads, and check pointer/aggregate/FP values,
C++ initialization/destructor retention, lifecycle and code retirement. Portable
assembly fixtures and rejection tests validate relocation records, instructions,
addends, bounds and selection of the index versus descriptor half of a record.
This earlier producer matrix emits TLSGD pairs. The independent TLSLD suite below qualifies distinct local-dynamic relocations; static TLS models remain unsupported.

Additional AArch64 TLSGD assembly fixtures exercise ADR and canonical 64-bit
G1 MOVN/MOVZ plus G0 MOVK, alongside the page/add control. They execute against
Clang TLSDESC and GCC traditional providers at O0/O2, directly and in archives,
on the calling thread and four joined C threads. The same inputs also link
with GNU ld into complete shared libraries and execute through OS TLS as an
independent control; external variables must retain STT_TLS declarations.
Range tests cover both ADR
limits, negative/positive GOT offsets, G1 sign selection/overflow and malformed
instructions. Full 32-bit GOT delta boundary tests require a 64-bit test process;
the 386 inspection jobs retain all instruction validation and cross-target
fixture checks. These forms reuse the same image owner and C address entry.

AArch64 TLSLD fixtures explicitly emit all 24 module-index and DTP-relative
relocation kinds. ADR, page/add, GOT-relative MOVW and a literal Q-register index
copy execute with Clang TLSDESC/GCC traditional providers at O0/O2, as objects
and archives, on the caller and joined C threads. Compiler getters cross-check
initialized/zero-filled integer, FP and 128-bit values, positive/negative field
addends and offsets beyond 64 KiB. Nonthrowing C++ providers verify five TLS
destructors precede global teardown. Retirement tests retain a code lease while
native threads pause and cover owner-before-join and concurrent thread exit.

GNU ld controls use the page/add index form and ordinary Q accesses through
DTP-relative ADD pointers: GNU ld 2.40 lacks module-index MOVW/literal and
128-bit DTPREL load/store relocation support. A local TLS anchor also avoids
mixing GD and LD GOT uses for one symbol in that linker. The raw-object cases
retain all 24 relocation kinds and the compiler-generated GD/TLSDESC providers.
Portable rejection and field/range tests cover option isolation, instructions,
widths/shifts, explicit/encoded addends, template extents and datum alignment.

AArch64 TLSDESC fixtures execute literal/ADR, ADR with LDR/ADD annotations,
GOT-relative MOVW with register LDR/ADD annotations, and the existing page
sequence. All four run with Clang TLSDESC/GCC traditional providers at O0/O2,
as objects, archives and identical GNU ld-linked OS libraries. Nonthrowing C++
providers check receiver values and five thread destructors before global
teardown; code-lease retirement covers both thread-exit orders. Each address
form also drives the X1–X18/full Q0–Q31 preservation probe. Portable tests check
all ten relocation kinds, preserve annotation bytes, reject invalid operands,
and cover descriptor-half addresses and both signed PC-relative limits. The
existing register fixture keeps page addressing as its default. `tiny` compiler
probes remain page-based; additional forms are qualified by independent assembly.

Native Windows TLS cases compile both Clang MSVC and GNU targets at O0/O2, execute
objects and archives, and validate packed section-relative offsets, large/aligned
values and native-pointer constants. C threads and pinned Go threads are created
before carrier loading; additional C threads cover OS TLS allocation at thread
creation. Two concurrent images check distinct indices and values. Lifecycle,
code-lease retirement and carrier file cleanup run through Go amd64/arm64/386
and llgo amd64/arm64. Pure Go PE tests inspect both optional-header widths, all
three machine types, TLS VA base relocations and the minimal entry and absence of writable executable sections.

Windows GNU native C++ TLS also runs O0/O2 object/archive code on C threads that
predate carrier loading. Exact initialized values, destructor receivers and nested
thread-exit registrations verify that FLS dispatch still has valid OS-owned TLS.
The shared C++ suite executes on native Go and direct/libffi llgo, with independent
Go on Windows ARM64. CLI object/archive tests use the same GNU native ABI and
verify current-thread destruction before global finalizers. MSVC emulated C++
tests retain their existing producer. Mixed GNU-native/GNU-or-MSVC-emulated C++
images run both O0/O2 object and archive forms, require both options and verify
independent value state and destruction before global finalization. Native MSVC cases additionally verify a shared native guard across objects,
initializer table snapshots, constructor reentry and active-constructor close
retry. All three guard getter entries match independent assembly.

Function-local C++ TLS uses the same discovered Go/llgo test commands. Native
and emulated modes run O0/O2 objects/archives on Linux/macOS and both Windows
GNU/MSVC ABIs. Separate entry calls verify lazy construction independently,
including reversed first-use order and exact destructor events. Large aligned
objects check double/self-pointer fields and cross-page zero fill at thread
exit. Foreign-thread Go callbacks and constructor reentry run in the libffi
suite; independent Windows ARM64 Go also executes them. Eleven-target metadata
checks distinguish function-local MSVC exit registrations from module-wide
initialization guards/tables. No hand-maintained CI test list is required.

Mach-O coalescing tests use the discovered test commands without additional
workflow entries. Every native host parses both target architectures, including
large multi-atom sections and malformed boundaries. macOS Go/llgo executes
O0/O2 objects/archives, strong/weak/common ordering, canonical pointer and
subtractor fixups, poison-member laziness, retry, lifecycle and unwind rejection.
Independent Apple-linked executables run the same probes with both nlist and
section-relative encodings; no library loader participates in those controls.
