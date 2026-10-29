# Archive containers

Ordinary GNU/SysV, BSD and COFF archives embed their object bytes. GNU/LLVM
thin archives (`!<thin>`) reference external files instead. Both use the same
object parsers, symbol selection, relocation backends and session ownership.
The filename extension does not determine the format. Ordinary and thin/proxy archives with
LLVM bitcode members can first be converted using the optional
[`compiler/llvm.CompileArchive`](llvm.md#bitcode-archive-compilation) helper.
The helper snapshots external bitcode/native members and emits an owned native
archive. It exposes source dependency directories through `Archive.SourceDirectories`;
the caller configures the destination session's `LibraryPaths` explicitly.

| Container form | Behavior |
| --- | --- |
| Ordinary ar | GNU/COFF long-name tables and BSD extended names; embedded relocatable objects |
| GNU/LLVM thin ar | External objects, relative/absolute paths and long names; no inline object payload or object-size padding |
| GNU thin proxy `/name-offset:header-offset` | Select a member of an external regular archive by its validated header offset; regular GNU/BSD/COFF names are supported |
| Nested archive objects or recursive thin references | Rejected; flatten thin inputs with the producer rather than nesting archive members |
| BSD extended names in thin archives | Rejected; use GNU thin name tables |

`Inspect` marks thin containers with `Info.Thin` and reads referenced objects
without executing them. Relative paths resolve from the thin archive's
directory, independently of the working directory. Absolute paths use the
host filesystem's path rules. External files must remain available for
inspection/loading; relative paths are preferable when moving a build tree.
As with GNU ar, references may point outside the archive's directory.

`Load` validates the complete container, reads all external members and keeps
their object snapshots. Multiple references to the same external path share the
same read, including proxy members. Member size hints and ranlib indexes may
be stale: actual object contents rebuild the symbol index. Modifying/deleting
external object files after a successful load does not change later linking.
An unavailable member fails loading, even if it would not be selected for
execution. Failed loads publish no staged files or path entries and can retry.

`Link` selects only required members. Unresolved symbols in unused objects do
not affect execution. Selected COFF imports search for DLLs relative to their
external object or regular archive, preserving the existing explicit-library,
`Options.LibraryPaths` and Windows search rules. DLLs themselves are opened
normally during linking; their contents are not part of the object snapshot.

Explicit COFF import metadata references can join members across multiple
staged archives, including raw/archive mixtures. Selecting a validated
component selects all its members while unrelated components remain lazy.
Member origins and DLL search precedence are preserved. See
[cross-object directories](coff.md#cross-object-directories) for ownership,
ambiguity, bounds and failed-link retry.

The thin container plus unique external file reads are limited to 256 MiB.
Decoded member bytes, including repeated references, have a separate 256 MiB
limit. A referenced regular archive counts its complete file size toward the
read limit. The selected executable image still has the usual 64 MiB limit.
These containers add no CPU, relocation, language runtime or calling convention.

Native Go and llgo tests execute a GNU/LLVM-produced thin archive with relative
paths containing spaces, dependency extraction, unused unresolved symbols and
deleted backing files. Proxy execution tests cover references into regular
archives. Windows tests execute short/proxy/long imports whose DLLs live beside
external members. Pure Go tests cover foreign-target inspection, offsets,
origins, malformed headers, read limits, snapshot reuse and failed-load retry.

References: [GNU ar](https://sourceware.org/binutils/docs/binutils/ar.html),
[GNU BFD archive reader](https://sourceware.org/git/?p=binutils-gdb.git;a=blob;f=bfd/archive.c),
and [LLVM ar](https://llvm.org/docs/CommandGuide/llvm-ar.html).
