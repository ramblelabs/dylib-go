# Platforms, compilation targets, and language outputs

The tables distinguish file inspection, implemented relocations, and actual execution evidence. Passing samples do not establish support for every relocation, TLS model, exception mechanism, or language ABI on a platform.

## Host platforms

| Host / target | Raw objects | System libraries | Evidence and limits |
| --- | --- | --- | --- |
| macOS arm64 | Mach-O 64 `.o` / `.a` | `.dylib` / bundle | Go and llgo execution tests, including optional libffi |
| macOS amd64 | Mach-O 64 `.o` / `.a` | `.dylib` | Go and llgo on GitHub Intel runners; additional local Rosetta tests |
| Linux arm64 | ELF64 LE RELA `.o` / `.a` | `.so` | Go and llgo on GitHub ARM64 runners |
| Linux amd64 | ELF64 LE RELA `.o` / `.a` | `.so` | Go and llgo on GitHub AMD64 runners |
| Windows amd64 | AMD64 COFF `.obj` / `.lib` archives / short/GNU long imports | PE `.dll` | Native Go and llgo objects, archives, DLLs, and scalar/struct ABI calls |
| Windows arm64 | ARM64 COFF `.obj` / `.lib` archives / short/GNU long imports | PE `.dll` | Independent Go and llgo object/archive/DLL calls and c-shared producers |
| Linux 386 | ELF32 little-endian REL/RELA `.o` / `.a` | ELF32 `.so` | Native 32-bit Go objects, archives, libraries, scalar/struct calls, and c-shared producer on an amd64 runner |
| Windows 386 | i386 COFF `.obj` / `.lib` archives / short/GNU long imports | PE32 `.dll` | Native 32-bit Go under WoW64; C cdecl calls and Go c-shared producer. setup-llgo does not install 386 |
| Linux RISC-V, LoongArch, PPC, s390x, ARM32 | Standard parsers can inspect some ELF inputs | No execution support in this project | No matching relocation backend or execution tests |
| FreeBSD and other BSD systems | Pure Go inspection can be built | No native backend | Needs OS operations, ABI validation, and execution tests |
| iOS / tvOS / watchOS / visionOS | Some Mach-O metadata can be inspected | No host integration | Mobile objects are identified and rejected as macOS plugins; execution policies also apply |
| Wasm, GPU, eBPF, bare-metal MCU | Outside native input support | Not applicable | Requires a separate executor, driver, or kernel loader |

Pure Go inspection does not require a file's CPU to match the host. Execution requires matching architecture, width, and format, and checks available OS metadata. Common ELF `OSABI_NONE` inputs do not identify their OS/libc, so their headers cannot prove ABI compatibility. Callers also remain responsible for required CPU features. Mach-O CPU subtypes are retained, and unimplemented authenticated ABIs such as arm64e are rejected.

## Files and relocations

| Input | Current behavior |
| --- | --- |
| Supported ELF/Mach-O/COFF 64-bit objects and i386 ELF32/COFF relocatable objects | Go links implemented relocations directly without first producing an OS shared library |
| Ordinary GNU/BSD ar (`.a`, ordinary COFF `.lib`) | Inspect all members, but map only needed members; unresolved dependencies in unused members do not affect linking |
| COFF bigobj, directly or inside ordinary `.lib` archives | 32-bit section numbers and 20-byte symbols; the same relocation and lifecycle limits as ordinary COFF |
| COFF short import objects, directly or inside `.lib` archives | Selected imports bind functions/data/ordinal exports from session-owned DLLs; all five name policies supported |
| GNU long import libraries (`.a` / `.lib`) | Standard per-symbol and contiguous multi-entry IAT/thunk templates for one DLL, decoded across descriptor/DLL-name members; functions/data/const/names/ordinals supported. Verified templates can coexist with retained ordinary sections and groups; cross-DLL and disjoint IAT layouts remain unsupported. See [mixed-member limits](coff.md#gnu-long-import-libraries) |
| Clang `-femulated-tls` raw objects / archives | Explicit `Options.EmulatedTLS`; image-owned descriptors and per-native-thread values; constant initialization, aligned storage and thread-exit/owner cleanup. Documented C++ thread constructors/destructors are supported. Native Mach-O TLV uses the separate option below. See [TLS scope](tls.md) |
| Native ELF TLS, Linux amd64/arm64/386 | Explicit `Options.NativeTLS`; C values, nonthrowing C++ wrapper initialization and thread-exit registrations, image-owned templates; amd64/386 GD/LD/TLSDESC and arm64 GD/LD/TLSDESC. Clang/GCC object/archive calls use Go-generated entries; static TP offsets and external TLS definitions remain unsupported. See [TLS models](tls.md#native-elf-dynamic-tls) |
| Native Mach-O TLV, macOS amd64/arm64 | Explicit `Options.NativeTLS`; C values, nonthrowing C++ wrapper initialization and thread-exit registrations, image-owned descriptors and per-thread storage. Go generates a register-preserving TLV entry; descriptors, initializers and raw variables have the [documented limits](tls.md#native-mach-o-tlv) |
| `.so`, `.dylib`, PE DLL | Host OS loads complete images, dependencies, TLS, and initialization |
| PE EXE, ELF EXEC/PIE, Mach-O EXEC | Identified and rejected as library inputs |
| GNU/LLVM thin archives | External objects and GNU proxy references into regular archives; snapshot reads and the same lazy extraction as ordinary archives. See [paths and limits](archives.md) |
| Universal Mach-O FAT32/FAT64 | Inspect all slices; macOS selects baseline amd64/arm64 objects or ordinary archives. Universal libraries use the OS loader. See [selection and limits](universal.md) |
| Mach-O external `N_INDR` symbols | Strong forwarding aliases, chains, private-external names, archive target extraction, and dependency ordering. See [alias rules](macho.md) |
| Mach-O weak atoms | Bounded symbol-delimited atoms in ordinary code/data/BSS and `S_COALESCED`, losing-fixup and associated DWARF/compact unwind pruning before dependency extraction, canonical local pointers/GOT/indirect entries, explicit subtractors, weak/common priority and retry. Same-address weak/mixed public groups and bounded interior/one-past public alternate entries retain independent name selection; shared/ambiguous boundaries remain outside the subset. Ordinary sections require the subsections flag. See [coalescing limits](macho.md#coalesced-weak-atoms) |
| Mach-O indirect symbol tables | Eager non-lazy/lazy/lazy-dylib pointer binding; regenerated amd64 stubs of at least 5 bytes and aligned arm64 stubs of at least 4 bytes, including padding and distant-target islands; local/absolute pointer markers supported. See [binding rules](macho.md) |
| Nonstandard/delay import tables | Rejected or unsupported; use a supported import library, an ordinary target slice/object, or load the DLL directly |
| LLVM bitcode and text IR | Explicit optional `compiler/llvm` compilation to native objects; explicit modules merge with matching targets/layouts; ordinary/thin/proxy mixed archives retain lazy members; see [coverage and limits](llvm.md) |
| OMF, D `.ddl`, Go gc `.a` | Not directly loaded |

Native Windows COFF TLS additionally supports the documented C `.tls/.tls$` subset and Clang GNU/MSVC ABI nonthrowing C++ namespace/function-local initialization and thread-exit registration with `Options.NativeTLS`, packed SECREL offsets and an OS-owned minimal carrier. See [native COFF TLS](tls.md#native-coff-tls).

Implemented relocation families:

Opt-in ELF dynamic TLS additionally supports amd64 GD/LD and DTP-relative offsets,
i386 GD/LDM/LDO, x86 TLSDESC address/call pairs, AArch64 TLSGD ADR/page/add/MOVW forms, local-dynamic module indices and checked/unchecked DTP-relative MOVW/ADD/8–128-bit load/store, plus TLSDESC literal/ADR/page/MOVW address forms and validated LDR/ADD/CALL annotations. See the
[native TLS model table](tls.md#native-elf-dynamic-tls) for exact instructions and limits.

- ELF i386: NONE, 32, PC32, PLT32, GOT32/GOT32X, GOTOFF, GOTPC, SIZE32; implicit REL and explicit RELA addends. GOT slots are 4 bytes; register-relative and baseless memory accesses use the [documented instruction rules](elf.md#i386-got-instruction-forms). Relative/size arithmetic wraps within the 32-bit address space.
- COFF i386: ABSOLUTE, DIR32, DIR32NB, REL32, SECTION, SECREL. Leading C linker underscores are normalized; stdcall/fastcall and vectorcall decorations remain. Vectorcall preserves identifier-leading underscores. Dynamic calls support explicit cdecl, stdcall, fastcall and thiscall on Windows 386.
- COFF ARM64: ABSOLUTE, ADDR32/ADDR32NB/ADDR64, BRANCH26, PAGEBASE_REL21, REL21, PAGEOFFSET_12A/12L, BRANCH19/14, REL32, SECTION, SECREL, SECREL_LOW12A/HIGH12A/LOW12L. Conditional branches must fit their architectural displacement.

- ELF x86-64: NONE, 64, PC32, PLT32, GOTPCREL/GOTPCRELX/REX_GOTPCRELX, GOT32/GOT64, GOTOFF64, GOTPC32/GOTPC64, GOTPCREL64, deprecated GOTPLT64, PLTOFF64, 32/32S, PC64, SIZE32/SIZE64. See [GOT and large-code rules](elf.md#amd64-got-offsets-and-large-code-models) and [symbol-size rules](elf.md#symbol-size-relocations).
- ELF AArch64: NONE (0/256), ABS64/32/16, PREL64/32/16, MOVW_UABS/SABS/PREL/GOTOFF groups, ADR/ADRP page, ADD/LDST8/16/32/64/128 low12, literal loads, CALL26/JUMP26, CONDBR19/TSTBR14, GOT literal/page/load64, GOTREL64/32, GOTPCREL32, 15-bit GOT/GOT-page loads, and PLT32 function data. See [instruction ranges and addends](elf.md#aarch64-instruction-relocations), [wide moves/data](elf.md#aarch64-wide-moves-and-narrow-data), and [GOT offsets](elf.md#aarch64-got-offsets-and-function-data).
  Unresolved weak references use the [AArch64 relative/call rules](elf.md#aarch64-unresolved-weak-references); absolute references and GOT targets remain zero.
- Mach-O x86-64: UNSIGNED, SIGNED, BRANCH, GOT_LOAD/GOT, external SUBTRACTOR+UNSIGNED, SIGNED_1/2/4; opt-in zero-addend TLV MOV loads.
- Mach-O arm64: UNSIGNED, external SUBTRACTOR+UNSIGNED, BRANCH26, PAGE21/PAGEOFF12, GOT page/offset, POINTER_TO_GOT, paired ADDEND. Opt-in native TLVP_LOAD_PAGE21/PAGEOFF12 ADRP/LDR references are supported. Local section-ordinal instruction relocations remain unsupported.
- COFF AMD64: ABSOLUTE, ADDR64, ADDR32, ADDR32NB, REL32 through REL32_5. Out-of-range executable direct calls/jumps and near conditional branches use adjacent stubs with their absolute target offset/bias. Out-of-range data references remain errors. SECTION/SECREL encode logical image-section ordinals and offsets. `__imp_` names use GOT slots for explicit DLL symbols.

Unknown relocations, overflow, writes outside sections, incompatible targets, and duplicate non-COMDAT strong symbols fail without publishing a partial executable image. Default loading skips unwind metadata. `Options{RegisterUnwind: true}` registers supported Windows amd64/arm64 runtime function tables, macOS amd64/arm64 DWARF/compact records and Linux amd64/arm64/386 libgcc frame sections for C stack traversal; Linux/macOS additionally support native C++ throw/catch/rethrow and RAII with explicit compatible runtime dependencies and the documented DWARF/compact metadata subsets. Windows language handlers and other raw unwind backends remain pending. See [unwind scope](unwind.md). Modern lifecycle tables, legacy ELF `.ctors/.dtors`, GNU ELF split CRT `.init/.fini` fragments, COFF integer initialization with owned failure cleanup, and image-owned `atexit`/`__cxa_atexit` registrations are supported; see [initialization and cleanup](lifecycle.md) for ordering, tests, and remaining forms. Objects with unimplemented native TLS models, unsupported lifecycle forms, unsupported COMDAT selections, or Swift/ObjC registration requirements are rejected.

ELF `GRP_COMDAT` groups retain the first signature-matched group. COFF supports NODUPLICATES, ANY, SAME_SIZE, EXACT_MATCH, ASSOCIATIVE, LARGEST, and timestamp-based NEWEST. Selection drops whole groups and their relocation dependencies before archive extraction, follows associative chains, and snapshots inputs for retry. Conflicting rules, content/size mismatches, and association cycles fail. COFF weak externals support NOLIBRARY, LIBRARY, and ALIAS fallback chains, strong overrides, archive search policy, and initialization dependencies; malformed records and used alias cycles fail. See [COFF rules and tests](coff.md). Cross-group references to discarded local symbols fail rather than guessing a corresponding symbol. Mach-O supports the bounded coalesced weak atoms documented above. COFF SECTION ordinals describe this loader's separate input sections, not a generated PE section table.

COFF bigobj retains full 32-bit section references and associative parent numbers. Ordinary COFF retains legal unsigned section numbers above 32767. Both readers support extended relocation tables, including the overflow-count marker. A 16-bit SECTION relocation still cannot represent an image ordinal above 65535; bigobj does not change the width of individual relocation fields. File/image size and page-alignment limits still apply.

Supported import libraries stage DLL dependencies until a member is selected. Imports resolve against their specified DLL, rather than the first matching export in another library. Explicit matching DLLs take precedence, followed by `Options.LibraryPaths`, the importing file's directory, and the Windows loader search paths. The session retains opened DLL references through link failure for retry and releases them on close unless `KeepLibraries` is set. Session-owned exit helpers retain their lifecycle behavior. See [import names, IAT slots, and ownership](coff.md#short-import-libraries).

## Language outputs

File format support alone does not establish language support. A callable entry also requires compatible ISA/OS ABI, an exact prototype, and an initialized language runtime.

| Producer | Boundary | Evidence and limits |
| --- | --- | --- |
| C (Clang; native GCC output may also work) | Ordinary C ABI; `-fPIC` where appropriate | Clang objects, data, BSS, common, cross-object calls and libraries tested; optional [prototype generation](clang.md) validates scalars, ordinary records/array members, struct/function pointers and supported conventions; fixed scalar/record/native-pointer/function-pointer direct llgo generation with compiler layout checks is optional; compiler-specific GCC extensions not comprehensively verified |
| Assembly (llvm-mc/Clang/as) | C ABI entry points | ISA and relocation subset must match; arbitrary assembly packages are not automatically compatible |
| C union values | Explicit selected-member C17 facade | `compiler/cunion` transports scalar/native-address/struct leaves and named fixed arrays through ordinary wrappers, including nested member paths; the compiler handles native union arguments/results. Direct union signatures and automatic active-member discovery remain unsupported |
| C++ | `extern "C"` facade; simple C-compatible functions may use exact mangled names | [Generated class facades](cxx.md) cover scalar/native-address constructors, const/static/virtual members and exception status codes in complete libraries. Explicit native class values/references/pointers and function pointers are supported through generated facades. Linux/macOS raw objects/archives additionally execute the documented C++ exception subsets; exceptions cannot cross Go frames. Header discovery and direct mangled entry calls remain unqualified |
| Rust | `extern "C"`, stable exported names, `panic=abort` | `no_std` leaf objects on both Linux/macOS architectures; Rust ABI, trait objects, and panic unwinding unsupported |
| Zig | `export fn`, C-compatible arguments | Objects on both Linux/macOS architectures; internal Zig ABI and complex layouts are not adapted |
| Fortran | `bind(C)`, `iso_c_binding`, explicit `value` arguments | gfortran objects on both Linux/macOS architectures; I/O/descriptors need runtime libraries and wrappers |
| Go gc | `go build -buildmode=c-shared`, `//export` | Libraries on all six amd64/arm64 targets and both Linux/Windows 386 hosts; Go `.a`, ordinary Go ABI, and GC-managed layouts are not C ABI inputs |
| llgo | Compile the host with llgo; plugins export C ABI / c-shared | Host and producer tested on six amd64/arm64 targets; setup-llgo does not provide a 386 installation, so 386 is unqualified; LLVM output still requires correct GC, initialization, and metadata |
| Swift | Complete library with C exports or optional [ABIBridge native calls](abibridge.md) | Both macOS architectures: opaque object retain/release and leased observers through [C facades](apple-facades.md); explicit native scalar/pointer/trivial/indirect Swift calls and owned Copyable values with complete metadata/copy/destroy witnesses through ABIBridge. Runtime libraries pinned; raw registration objects rejected; managed argument passing, generic calls and async/throws unsupported |
| Objective-C / ObjC++ | OS-loaded framework/dylib with C facade or explicit adapter | Both macOS architectures execute ObjC++ C facades with ARC-managed opaque objects and leased Go observers; runtime libraries pinned; raw registration sections rejected; no built-in objc_msgSend signature or ARC management |
| D | Independent C ABI boundary can be attempted as native input | No DDL-specific D compatibility or D compiler/runtime verification |
| Odin, Nim, Pascal, other native compilers | C exports with complete runtimes or standalone computation objects | Possible extensions, but no execution evidence and no current support claim |

`Resolve` and `Symbol.WithAddress` support caller-defined adapters for any compatible native signature or data layout. llgo can compile known C signatures into direct calls; ordinary Go can use typed cgo bridges. The loader does not infer or validate those signatures. Optional dynamic `Bind` uses libffi for fixed-width 8/16/32/64-bit integers, C `bool`, `f32/f64`, pointers, and ordinary C structs, with argument storage sized to the concrete signature and the host C ABI, plus fixed stdcall/fastcall/thiscall calls on Windows 386. Thiscall takes an explicit pointer receiver; struct results require generated C bridges. Variadic signatures specify a fixed-prefix count and one concrete tail; small scalar tail types receive C default argument promotions. Bindings retain reusable CIFs and record layouts until session close. Standalone `abi.Prepare` plans require explicit close and external code ownership. Struct arguments/results, fixed-length array members/pointees, nesting, and temporary pointers are tested; unions, packed records, and bitfields remain unsupported. Arrays cannot be bare function arguments/results; see [array types and limits](arrays.md). It does not supply C++/Swift or cross-OS ABI adaptation.

`abi.NewCallback` supplies fixed native C entries for Go/llgo handlers with the same scalar and ordinary struct descriptors, including Windows 386 stdcall/fastcall/thiscall. Object, archive, and shared-library fixtures cover both call directions. C-created threads exercise allocations, GC, and concurrent captures on the eight Go and six llgo targets. Callers own registrations, library lifetimes, pointer storage, and leases; variadic callbacks are unsupported. See [callback contracts](callbacks.md).

## Other execution domains

Python/Ruby/Lua extensions, JNI, databases, and native image/audio/video plugins may reuse the loader when the host language runtime is already initialized and the entry protocol is correct. Symbol discovery does not initialize an interpreter or manage its objects.

Wasm needs a separate Go executor interface, such as a future evaluation of wazero. CUDA/HIP/Metal/OpenCL need device drivers, modules, and launch APIs. eBPF needs a kernel verifier/loader. These are candidates for independent plugin backends, and none is integrated here.

Cross-CPU execution can use a separate process with RPC; cross-OS execution additionally requires the relevant system and runtime. Relocation cannot replace emulation, Wine, ABI thunks, or OS services.

Explicit amd64 conventions use `abi.Win64` for Microsoft x64 and `abi.SysV64` for System V. Fixed scalar, pointer and ordinary struct signatures are supported; register exhaustion and hidden aggregate results follow the selected ABI. Dynamic Win64 calls/callbacks require an amd64 host. Dynamic SysV64 calls/callbacks require Linux/macOS amd64 because Windows libffi does not expose that ABI; compiler-generated C bridges cover both conventions on Windows amd64 as well. Source declarations retain the producer's C widths/layout and native function-pointer conventions. This does not load foreign-OS images, supply their system calls, or adapt C++ objects.

Rust `no_std` exports using fixed `extern "win64"` and `extern "sysv64"` are tested on Linux/macOS amd64 with C-compatible scalars, `repr(C)` ordinary records, native factories and callbacks. Rust native ABI values and panic unwinding remain unsupported.
