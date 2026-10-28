# COFF symbol and section selection

The Go linker implements these rules for AMD64, ARM64, and i386 COFF. Inputs still need supported relocations and a matching Windows host for execution.

## Ordinary objects and bigobj

Ordinary COFF/PE parsing uses Go's `debug/pe`. An independent Go reader handles bigobj's 56-byte header, format UUID, 32-bit section numbers, and 20-byte symbol records, following the [LLVM COFF format definitions](https://github.com/llvm/llvm-project/blob/llvmorg-22.1.8/llvm/include/llvm/Object/COFF.h). Both paths then use the same symbol, COMDAT, relocation, and lifecycle implementation.

Ordinary COFF section references above 32767 remain positive; only reserved values are interpreted as signed. Bigobj associative section records combine the low/high parent fields. Direct objects and archive members are accepted by content. Section data, symbol/string tables, auxiliary counts, and relocation ranges are checked before access.

Extended relocation tables use the first record as a count marker, including that marker in the stored count. It is validated and excluded from actual relocations. This works for ordinary objects and bigobj. Bigobj does not widen relocation fields: a 16-bit SECTION relocation cannot encode an image ordinal above 65535. It also does not relax image size, W^X, architecture, TLS, or unwinding limits.

i386 C names remove one extra linker underscore. Import normalization retains
the `__imp_` marker and the fastcall `@` prefix: `__imp_@fast_add@8` remains
unchanged. MSVC C++ IAT names such as `__imp_?add@api@@YAHHH@Z` also
retain their complete import marker. Vectorcall retains its `@@N` suffix and identifier-leading underscores.
The DLL export spelling follows its import record, independently of the normalized
public name. Native Windows 386 tests use real linker-generated import libraries
for cdecl/stdcall/fastcall/vectorcall, including underscore-leading identifiers,
with repeated object/archive consumer calls at `-O0` and `-O2`. C++ fixtures
check primitive functions and data, exact mangled direct calls and compiled C++
callers, with exceptions and RTTI disabled.

## Short import libraries

Short import objects use a 20-byte header followed by the public symbol, DLL name, and optional export-name string. Their signature resembles bigobj, but their version and content select a separate Go parser. Ordinary `.lib` containers can mix short imports with regular COFF members. Only selected imports open a DLL; unused missing dependencies remain inert.

`Inspect` reports `Info.Imports` with the DLL, normalized public name, export name or ordinal, and code/data/const kind. Inspection opens no dependency and is independent of the host architecture.

| Import name policy | DLL lookup |
| --- | --- |
| ORDINAL (0) | Header ordinal; no name lookup |
| NAME (1) | Original public name |
| NOPREFIX (2) | Remove one leading `?`, `@`, or `_` |
| UNDECORATE (3) | Remove that prefix and truncate at the first `@` |
| EXPORTAS (4) | Separate export-name string |

These transformations operate on the original linker name before normalizing the i386 C underscore. They follow [Microsoft's short import format](https://learn.microsoft.com/en-us/windows/win32/debug/pe-format#import-library-format) and [LLD's import implementation](https://github.com/llvm/llvm-project/blob/llvmorg-22.1.8/lld/COFF/InputFiles.cpp). The loader does not infer calling conventions from decorations.

CODE imports expose the callable public name and `__imp_` IAT name. DATA exposes the IAT name. CONST exposes both names as aliases of the IAT slot. Slots are populated before protection becomes read-only, including import-only sessions. Calls use existing branch stubs when the DLL entry is out of range. Imports resolve against the declared DLL even if another loaded library exports the same name. Ordinary object definitions override callable import names; IAT names keep their DLL binding. Equivalent imports share definitions, while conflicting explicit DLL imports fail.

`Define` can supply the native function/data address for an import's normalized public name. Reserved exit/lifecycle helpers use the session-owned implementation, including imports through IAT slots. For other dependencies, explicitly loaded matching DLL names win; otherwise search `Options.LibraryPaths`, the importing file's directory, then native loader paths. DLL names are basenames; directories belong in `LibraryPaths`. An omitted extension defaults to `.dll`. The options slice is copied by `New`.

Each opened DLL reference belongs to the session. Link failure publishes no raw image and retains opened DLLs for retry, matching explicitly loaded dependencies. `Close` unloads them after raw finalization unless `KeepLibraries` requests retention. Native DLL initialization is an OS-loader side effect and cannot be rolled back by the raw linker.

## GNU long import libraries

Standard GNU Binutils import libraries store each IAT slot and optional function thunk in an ordinary COFF member. DLL descriptors and names live in separate members. The Go parser follows `.idata$7` RVA references to the `.idata$2` descriptor and its DLL-name reference; `.idata$5`/`.idata$4` entries identify a hint/name record or ordinal. It converts a validated import-only member to the same symbol model used by short imports, so the OS does not need to initialize the raw tables.

The decoder verifies matching lookup/address entries, known tail-jump code and its relocations. Descriptor helpers remain metadata and are not extracted for a converted import. Ambiguous/missing descriptors, isolated thunk members, malformed entries, and arbitrary custom import layouts retain an explicit unsupported marker. The parser never discards unknown executable behavior merely because a section has an `.idata` name.

A member can contain multiple contiguous IAT slots for one DLL. The optional ILT must match every slot, and each slot must have one global `__imp_` name. Imports may mix functions, data, const aliases, export names and ordinals. `.idata$7` can contain one shared descriptor reference or one reference per slot; all must identify the same DLL. Each slot is checked through the existing single-import decoder, including exact thunk bytes and unique relocation sites. DLL metadata is validated once per descriptor. The normalized import names have the same aggregate byte budget as an input file.

A member can also contain ordinary sections alongside the verified import templates. Its `.text` section must contain only the recognized thunks; additional code belongs in separate sections such as `.text$mix`. Ordinary code, data, BSS, initialization/finalization arrays, symbols and relocations remain in the selected object. References to each public import or its global IAT name use the existing DLL/owned-slot binding. Original symbol indices remain stable; direct references into removed table sections or local thunk labels are rejected. Native table addresses and slot adjacency are not preserved. Ordinary unresolved references still require providers and participate in lazy archive extraction and failed-link retry.

The mixed-member path reuses the import-only validator before removing any template sections. Ordinary COMDAT sections are retained with their original selection rules, section indices and associative parents, including zero-size sections. Associations may cover ordinary initialization/finalization arrays; discarding their parent also discards those entries and their references. COMDAT groups and their parents must stay entirely outside the removed import template.

GNU force-reference templates still require one DLL. Extra code sharing the thunk's `.text`, arbitrary descriptor/code combinations, repeated IAT sections and nonstandard `.idata` sections remain unsupported. The separate self-contained directory form below supports explicit cross-DLL association. Descriptor and DLL-name helpers must remain metadata-only. Unrelated unsupported features such as TLS retain their diagnostics. An unused mixed archive member does not load its DLL; selecting one preserves its ordinary initialization and finalization under the normal session lifetime rules.

`Info.Imports`, `LibraryPaths`, DLL ownership, and failure/retry behavior are shared with short imports. MinGW consumers using explicit `__declspec(dllimport)` function/data declarations execute on Windows AMD64/ARM64/386; calling conventions still require an appropriate caller adapter. Compiler-driven data auto-import/pseudo-relocations are not implemented.

Tests execute genuine [GNU Binutils 2.45 dlltool](https://sourceware.org/binutils/docs/binutils/dlltool.html) archives alongside freshly compiled DLLs. The fixtures and their regeneration script are in [testdata/coffimports](../testdata/coffimports/README.md). The decoder follows [PE import descriptor/lookup-table semantics](https://learn.microsoft.com/en-us/windows/win32/debug/pe-format#the-idata-section), with a bounded GNU per-symbol template.

Mixed-member tests inspect function/data imports and apply ordinary relocations for all three COFF architectures on every host. Native Windows Go/llgo tests call the retained code, initialized data and an ordinary dependency; observe DLL calls from retained initializers/finalizers; verify missing-dependency retry without initialization; and leave an unused mixed member's missing DLL unopened. Windows 386 uses Go.

COMDAT mixed-member tests additionally verify selections 1–7, association and relocation discard, original input snapshots, grouped-template rejection and zero-size groups. Native Windows tests execute retained and discarded associative initialization/finalization at `-O0`/`-O2`, with ordinary archive extraction, failed-link retry and an unused grouped import whose DLL is absent.

Multi-entry tests inspect pure, mixed and COMDAT members on all three architectures, including data-first/ordinal-only tables, const aliases, optional ILTs, shared descriptors and malformed later entries without partial conversion. Native Windows tests call named/ordinal functions and imported data from one member through pure and mixed archives at `-O0`/`-O2`; mixed cases retain dependency extraction, retry, associative lifecycle selection and lazy missing-DLL behavior.

ARM64EC hybrid imports, delay-load tables, and linker directives for automatic default-library selection remain outside the current implementation.

## Self-contained import directories

A raw object can instead contain a complete bounded `IMAGE_IMPORT_DESCRIPTOR`
array in `.idata$2`. Each descriptor explicitly identifies a DLL name and its
terminated `.idata$5` address subtable and an optional `.idata$4` lookup subtable.
Multiple descriptors
may identify different DLLs exporting identical names. RVA references determine
that association; the decoder does not infer it from table or symbol order.
The directory ends with a null descriptor, and each nonempty subtable ends with
a null pointer. Pointer-aligned zero padding between subtables is allowed.

When `OriginalFirstThunk` is zero and has no relocation, the unbound IAT supplies
the lookup entries. Each descriptor may independently include or omit its ILT;
the entire `.idata$4` section may be absent. A nonzero or relocated lookup field
must still reference a valid ILT. A malformed reference never selects the IAT
fallback. The timestamp and forwarder fields must remain zero; prebound native
addresses and delay-import state are rejected. The
[PE specification](https://learn.microsoft.com/en-us/windows/win32/debug/pe-format#import-directory-table)
describes the matching lookup/address contents before binding.

The Go decoder validates matching IAT/ILT bytes and name references when both
tables exist, nonoverlap, 16-bit nonzero ordinals, complete relative-address fields and bounded native
names. Each imported row needs one public `__imp_` identity. Named rows refer
to hint/name entries in `.idata$6`; DLL names reside in `.idata$6` or `.idata$7`.
An optional `.text` contains only the existing exact tail-jump templates, matched
to their own IAT rows. Additional callable names and const/data aliases keep
the existing import symbol semantics. There are at most 65,536 descriptors and
65,536 imported rows, with the usual input-file/name budget.

The resulting imports use the same DLL lookup, owned IAT slots, lifetime and
retry rules as short/GNU imports. Native table storage and adjacency are not
exposed. The same overlay preserves original symbol/section indices, ordinary
code/data/BSS, dependencies, COMDAT associations and lifecycle entries. Live
references into removed private helpers are rejected. Unknown fixups, executable
metadata and groups touching import metadata prevent conversion. Other
unsupported features keep their original diagnostics.

This form requires one section per supported table name and self-contained
metadata references. Public descriptor/name helpers, separate null
descriptor sections, bound imports, delay imports, repeated IAT sections,
auto-import/pseudo-relocations and arbitrary inline code remain unqualified.
Complete DLLs continue to use the OS loader for those native table layouts.

Compiler-produced AMD64/ARM64/i386 objects and rejection cases run on all
native CI hosts. Optional cross-target LLD controls link the original inputs
into complete PE DLLs and inspect their imports. Ordinary ILT controls use Go's
`debug/pe`; IAT-only controls use LLVM's independent `llvm-readobj` reader because
`debug/pe.ImportedSymbols` stops at a zero `OriginalFirstThunk`. The controls also
check that the linker preserved each descriptor's ILT presence. Windows
Go/llgo tests additionally execute those DLLs through the OS loader, alongside
raw-object/archive calls. Distinct providers exporting identical names verify
DLL-specific function/data/ordinal selection. Mixed tests cover ordinary and
COMDAT code, retained/discarded initialization/finalization, missing-dependency
retry, staged snapshots and an unused missing-DLL archive member. Go qualifies
i386; native llgo qualifies AMD64/ARM64.

IAT lookup tests cover omitted and mixed-present ILTs, reversed descriptor order,
same-name exports from distinct DLLs, names/ordinals/data/const aliases, empty
optional lookup sections and mixed ordinary/COMDAT members. Native Windows
object/archive and OS-library controls run at `-O0`/`-O2`; mixed members retain
dependency retry, lazy unused imports and lifecycle selection. Bound pointers,
nonzero or malformed lookup references, overlapping subtables, missing terminators
and unclaimed metadata bytes/fixups are rejected without changing staged input.

## Weak externals

A COFF weak external has an auxiliary record naming its fallback symbol and archive search policy. It is not just an undefined ELF weak symbol. The supported policies follow the [Microsoft PE/COFF specification](https://learn.microsoft.com/en-us/windows/win32/debug/pe-format#auxiliary-format-3-weak-externals):

| Policy | Archive extraction for the weak name | Fallback |
| --- | --- | --- |
| NOLIBRARY (1) | No | Used when no selected definition or explicitly supplied native symbol exists |
| LIBRARY (2) | Search before following the fallback | Used if archive search cannot supply the weak name |
| ALIAS (3) | No | Used when no selected definition or explicitly supplied native symbol exists |

An ordinary reference to the same public name follows its weak alias. Fallbacks can refer to another alias, a selected object, or an archive definition. Selected strong definitions and explicit native symbols override fallbacks. SECTION/SECREL relocations and lifecycle dependency ordering follow the resolved provider too. Conflicting fallback declarations, malformed auxiliary records, and cycles reached by a used reference fail before initialization. ARM64EC anti-dependency policy is outside the supported target set.

## COMDAT

| Selection | Behavior |
| --- | --- |
| NODUPLICATES (1) | Reject duplicate keys |
| ANY (2) | Retain the first input |
| SAME_SIZE (3) | Require matching sizes, then retain the first input |
| EXACT_MATCH (4) | Require matching section size and raw contents, then retain the first input |
| ASSOCIATIVE (5) | Retain only with the parent COMDAT |
| LARGEST (6) | Retain the largest section; ties retain input order |
| NEWEST (7) | Retain the largest COFF header timestamp; ties retain input order |

EXACT_MATCH compares raw contents without comparing relocation targets or alignment, matching [LLD's link.exe-compatible implementation](https://github.com/llvm/llvm-project/blob/llvmorg-22.1.8/lld/COFF/InputFiles.cpp). Discarded sections and their references never participate in dependency extraction or initialization. Inputs are snapshotted so an unsuccessful link can be retried.

NEWEST is an explicit loader policy for selection value 7; current LLD rejects this value. It uses the unsigned header timestamp, not file modification time or input order. Reproducible objects with equal or zero timestamps retain the first definition. This does not claim that current Microsoft/LLVM toolchains emit NEWEST objects.

## Validation

Clang-generated fixtures inspect all three COFF targets. Tests cover malformed records, alias chains/cycles, strong/native overrides, fallback archive extraction, address and section relocations, equal/unequal EXACT_MATCH content, and timestamp/tie selection. Existing Windows Go and llgo suites execute weak function calls from objects and archives and functions selected by EXACT_MATCH/NEWEST. Windows 386 executes with Go because setup-llgo does not install that architecture.

Large fixtures are genuine Clang output with 33000 and 65540 padding sections, testing ordinary unsigned indexes, bigobj, high associative parents, weak fallback indexes, and archive parsing. Windows native tests call functions from high-section bigobj objects/archives and verify the last of 65536 relocated data pointers. Separate malformed-input tests cover header/UUID/version, sizes and offsets, auxiliary counts, and overflow markers.

Import tests cover all name policies and CODE/DATA/CONST metadata on every host. Windows jobs build a real DLL and `.lib` with Clang/LLD, execute imported functions/data, and test ordinal lookup, export aliases, explicit DLL reuse, moved-library search paths, unused missing DLLs, failed links, and retry. Go and llgo use the same runtime tests; Windows 386 runs Go.
