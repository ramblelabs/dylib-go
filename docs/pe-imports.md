# PE import metadata

`dylib.Inspect` exposes PE image imports through `Info.PEImports`. The standalone
`peinfo.Inspect(data)` package uses Go's `debug/pe` headers and a bounded RVA
reader. It needs no native execution, cgo, libffi, LLVM or loaded dependencies.
PE32 and PE32+ images can be inspected on any host.

| Metadata | Reported values |
| --- | --- |
| Ordinary import directory | DLL name, timestamp, forwarder chain, lookup/IAT RVAs, lookup availability and symbols |
| Imported symbol | Export name and hint or explicit `ByOrdinal` flag and 16-bit ordinal, plus the original on-disk IAT word |
| Bound import directory | Module timestamps and forwarder module names/timestamps, including reserved forwarder words |
| Delay import directory | Attributes, timestamp, normalized module-handle/IAT/INT/bound/unload RVAs, symbols and auxiliary address tables |

The reader keeps identity and cached addresses separate. With an ILT, names and
ordinals come from that table. An unbound descriptor without an ILT uses its IAT
for lookup. A bound descriptor without an ILT reports `LookupAvailable=false`:
its cached addresses remain visible, while symbol names and ordinals are
unavailable. Addresses are never interpreted as lookup names in this case.
`ByOrdinal` preserves ordinal imports including ordinal zero. The metadata reader
reports the wire identity; it does not decide whether the target DLL exports it.

`Symbol.Address`, `BoundAddresses` and `UnloadAddresses` are on-disk values.
They are not validated callable process addresses. ASLR, binding, delay helpers
and explicit unloading can change those values after loading an image.

Modern delay descriptors set the `RvaBased` attribute. Legacy descriptors use
VAs; the reader subtracts the preferred image base from descriptor pointers and
named lookup entries. Returned fields are always RVAs. The attributes word is
preserved. Legacy execution controls qualify the PE32 form; modern PE32/PE32+
controls qualify i386, AMD64 and ARM64.

The reader enforces a 256 MiB input/name budget and at most 65,536 aggregate
records, symbols and auxiliary address slots. Tables and names must be
file-backed, with bounded null termination. Module-handle storage can reside in
the zero-filled image tail. Overlapping virtual mappings, RVA/VA arithmetic
overflow, truncated records and inconsistent table terminators fail without
partial output or input mutation. Returned metadata owns its strings and arrays.
Concurrent inspections share no mutable decoder state.

## Loading and verification

Complete PE libraries continue to use the Windows loader and their native delay
helpers. Metadata inspection does not open DLLs, bind imports, invoke helpers or
alter IATs. Raw COFF delay execution and auxiliary bound metadata are separate
loader work; this package inspects explicitly bounded PE image directories.

Tests compile MSVC/GNU AMD64/ARM64/i386 PE controls from C and assembler sources.
LLVM independently checks ordinary/delay lookup identities, directory fields,
IAT words and unload-pointer base relocations. Bound-directory records and
forwarders use assembler-produced metadata selected through the PE header.
Malformed inputs, missing lookup tables, cache identity, snapshots, concurrency,
record budgets and Go fuzz seeds are also exercised.

Windows Go amd64/arm64/386 and llgo amd64/arm64 controls execute the compiled DLLs
at `-O0` and `-O2`. A small CRT-independent C helper follows the documented delay
helper ABI, loads providers relative to its own DLL, resolves named/ordinal
entries and restores relocated unload tables before freeing providers. Counters
verify deferred first resolution, reuse and resolution after explicit unloading.
The PE32 control also executes legacy VA descriptors.

The wire layouts follow the
[Microsoft PE/COFF specification](https://learn.microsoft.com/en-us/windows/win32/debug/pe-format)
and the documented
[delay-load helper interface](https://learn.microsoft.com/en-us/cpp/build/reference/understanding-the-helper-function).
