# Versioned module dependencies

The optional Go-only `module` package loads explicit dependency manifests using
ordinary `dylib.Session` owners. It adds graph planning and exact version checks;
native parsing, archive selection, linking and code retention stay in the core.
No llgo compiler, libffi or external dependency resolver is required.

| API | Behavior |
| --- | --- |
| `Manifest.Validate()` | Check structural contracts, names, versions, declared exports and cycles without reading native inputs |
| `Read(reader)` | Decode and validate one JSON document, up to 1 MiB; reject unknown fields and trailing documents |
| `Open(base, manifest, options)` | Load selected roots and their transitive providers, resolving relative file paths from `base` |
| `OpenCompiled(ctx, base, manifest, options, compile)` | Prepare selected files with an explicit compiler before native loading; own returned artifacts through graph close and cleanup retry |
| `Load(path, options)` | Read a manifest and use its containing directory as the input base |
| `Discover(ctx, roots, directories...)` | Read provider catalogs from explicit directories and return a complete manifest for exact root versions; no native loading |
| `DiscoverCompatible(ctx, roots, directories...)` | Select a consistent graph from explicit acceptable root versions in preference order, pinning exact imports before native loading |
| `DiscoverVersionRanges(ctx, roots, directories...)` | Expand caller-declared SemVer root intervals, then select and pin a consistent exact-version graph before native loading |
| `NewLazySymbol(ctx, request, build, directories...)` | Select and load one function/data symbol's provider graph on first `WithSymbol`; share initialization and cached failures |
| `Graph.Session(name)` | Borrow one loaded session for normal library calls and adapters |
| `Graph.Close()` | Retire graph lookup, then close consumers before providers; concurrent closes share one attempt; failed cleanup retains consumers/providers for retry |

The [compiler contract](compiled-modules.md) integrates optional source/IR/archive
preparation with graph ownership. Ordinary `Open` and `Load` keep their existing
native input behavior.
The [per-symbol lazy contract](lazy-symbols.md) defers discovery and supports
either ordinary or compiler-backed builders, with independent owners per symbol.

The [executed manifest example](../examples/modules/manifest.go) imports a C
function and global data using different consumer linkage names. Native tests
serialize that same manifest to JSON, load it from a separate directory, and
execute the consumer through a typed Go or direct llgo adapter without libffi.

## Manifest contract

| Field | Meaning |
| --- | --- |
| `format` | Required schema version, currently `module.FormatVersion` (1) |
| `roots` | Distinct selected module names; at least one |
| `modules[].name` | Unique, case-sensitive module identifier |
| `modules[].version` | Nonempty opaque ABI contract version |
| `modules[].files` | One or more explicit native input paths; objects, archives and complete OS libraries use normal content identification |
| `modules[].exports` | Exact core linkage names made available to consumers; also explicit roots for lazy archive extraction |
| `modules[].imports` | Explicit provider, exact required version, exported symbol, and optional consumer alias `as` |

Providers can appear after consumers in the document. Root and import order
determine a stable dependency traversal; a shared provider is loaded once even
when multiple roots or imports use it. Unreachable definitions are validated but
their files are not opened. Unused archive members remain unselected; declaring
an export explicitly makes it an archive root.

Versions match exactly. They are supplied contracts, not a binary ABI probe,
semantic-version range, signature inference or authenticity check. Missing
providers, version mismatches, undeclared imports, duplicate aliases and cycles
fail before native file loading. Actual exports, reserved definition names,
relocations, calling conventions and host compatibility still follow core
validation. A raw missing export root fails before initialization. OS libraries
can initialize as soon as they are loaded, before later failures are discovered.

`DefineSymbol` supplies retained function/data addresses to each consumer. Normal
core name precedence applies. This does not rewrite an OS library's internal
imports or adapt an ABI: complete libraries must already have their normal
native runtime/dependency linkage. `dylib.Options` apply to every session without
rebasing their `LibraryPaths`; file paths alone use the manifest base directory.
Absolute input paths remain absolute. Keep the manifest/options stable during
`Open`.

Windows consumer declarations use `__declspec(dllimport)` for retained imports,
including data. The core binds these through session-owned IAT slots, allowing
providers outside the reach of a direct PC-relative data reference. The same
declaration works with object, archive and DLL providers.

## Lifetime and failure

The graph owns its sessions. Borrow them for `Resolve`, `Bind`, or application
adapters; call `Graph.Close` to close the whole graph. Closing an individual
provider first can wait for still-open consumers. Returned symbols/functions and
their leases retain code under the existing core rules.

`Close` first rejects new graph lookups, then closes sessions in reverse
dependency order. A provider remains alive through consumer native finalization.
External leases can delay cleanup; release them after their native users finish.
Stop native registrations, join workers and destroy language objects before
closing their graph. Call close outside its own calls, callbacks and finalizers.

Open failure attempts to close every session created by that attempt, including the failing
consumer. Cleanup releases retained provider leases in dependency order and
returns any cleanup errors together with the original failure. Native
initialization/finalization has side effects; cleanup does not undo those effects.

CI runs object/archive/library providers and multiple roots at `-O0` and `-O2`,
including archive members that must remain unused. Lifecycle tests observe native
initializers/finalizers, a failed consumer link, stale exports, exact-version
preflight, and concurrent graph close while an external observer lease remains
callable. These execute on all eight Go targets and six llgo targets. No new
execution platform or object format is introduced by this package.

`module.Manager` adds [explicit replacement and state migration](replacement.md),
preserving old scopes and code leases until retirement.

## Provider catalogs

`Discover` scans explicit directories for regular `*.dylib.json` files without
recursion. A `Catalog` document contains `format` and `modules`, using the same
module declarations as a manifest. It has no `roots` field: callers select roots
with `Requirement{Name, Version}`. Imports may refer to providers declared in
other catalog files or directories. Each native file path is resolved relative
to its declaring catalog; returned manifest paths are absolute.

All available declarations receive local contract validation. Duplicate
name/version declarations are ambiguous and fail rather than selecting the first
directory. Different available versions may coexist, but a selected dependency
graph must use one exact version per module name. Missing providers, conflicting
versions, missing exports and reachable dependency cycles fail before loading.
Unreachable declarations may refer to uninstalled providers; they are omitted
from the result and their native files are never opened. Invalid local metadata
still fails discovery even when unused.

Pass the discovered manifest to `Open` to load it, or use it in a replacement
builder. Discovery itself reads metadata only and does not initialize images,
infer signatures or negotiate version ranges. Catalogs and native files are not
snapshotted together: `Open` reads the native files later. Their ABI contracts and
provenance remain caller-supplied, just as with ordinary manifests.

Discovery supports 1-256 roots, 1-16 explicit directories, at most 4096 directory
entries, 256 catalog files, 256 module declarations, 4096 native input paths and
8 MiB total JSON, with the existing 1 MiB per-document limit. Repeated directory
paths are normalized and scanned once. Filenames are sorted within each
directory, preserving caller directory/root/import order. Symlink catalog
files are rejected; symlink directory arguments follow normal OS behavior.
Cancellation is checked between filesystem operations, reads and graph visits;
an in-progress OS filesystem operation cannot be interrupted.

The [executed example](../examples/modules/manifest.go) selects `app/1` with
`Discover`. Native tests place consumers and providers in separate catalog
directories, select the required version while another version has missing
native files, and execute object/archive/library graphs at `-O0`/`-O2` with Go
and llgo. Existing graph retirement and rollback rules apply unchanged.
Automatic ABI/signature discovery, semantic-version ranges, system-directory
search and symbol-triggered compilation remain outside these APIs.

## Explicit compatible versions

`DiscoverCompatible` accepts `CompatibleRequirement{Name, Versions}` roots. Each
`Versions` slice lists ABI contracts that the caller accepts, ordered by preference.
Versions remain opaque strings: `app/2` does not imply compatibility with `app/1`.
Catalog imports still require their exact declared versions. A provider that is
also a root must satisfy that root's acceptable versions, even if another root
reaches it first.

Selection tries root/version combinations in caller order, returning the first
complete graph whose versions, exports and dependency cycles validate. A conflict
introduced by a later root can cause an earlier root to use its next version.
Missing provider versions, conflicting dependencies, missing exports and cycles
invalidate a candidate. Locally malformed declarations or ambiguous duplicate
providers fail discovery regardless of preference; they cannot be hidden behind
a fallback. Unselected candidates do not leave dependencies in the final graph.

The result is an ordinary manifest with one exact version per name, absolute
catalog-relative file paths, and the same `Open`/retirement behavior. It can be
serialized with Go's JSON encoder or supplied to a replacement builder. The
[executed example](../examples/modules/manifest.go) prefers `app/2`, accepts `app/1`,
and pins `math/1`. Native tests select and execute the compatible graph while an
unacceptable provider version names a missing file, using objects, archives and
libraries at `-O0`/`-O2` through Go and llgo adapters.

All catalog limits above apply. At most 4096 total acceptable versions and 4096
candidate attempts are allowed; exhausting either limit returns an error with no
manifest. Cancellation is checked during search as well as catalog I/O. Root
preference order is deterministic, not a global newest-version policy. Discovery
does not open native inputs. A native loading, initializer or ABI failure in
`Open` does not trigger another version attempt or execute a fallback image.

A failed native close stops reverse dependency cleanup at that consumer. Its
providers remain alive, graph lookup remains retired, and another `Graph.Close`
retries unfinished sessions. Completed sessions and native finalizers do not
repeat. If `Open`/`Load` fails and rollback cleanup also fails, it returns a
non-nil retired graph together with the joined error. The caller owns that graph
and must retry its `Close`, including when opening returned an error.

## Caller-declared SemVer intervals

`DiscoverVersionRanges` accepts `VersionRangeRequirement{Name, AtLeast, Before,
IncludePrerelease}`. Both bounds are required and describe the half-open interval
`[AtLeast, Before)`. Versions follow [SemVer 2.0.0](https://semver.org/spec/v2.0.0.html):
complete `MAJOR.MINOR.PATCH`, optional prerelease/build identifiers, no leading
`v`, wildcards or comparison expressions. Version strings are limited to 256
ASCII bytes. Numeric identifiers are compared without machine-integer overflow.

Available matching versions are tried from newest to oldest. Build metadata is
ignored for precedence; equal-precedence versions use ascending literal text for
a deterministic tie-break. Prereleases are excluded unless `IncludePrerelease`
is true, including when a bound itself is a prerelease. Opaque and overlong
catalog versions remain valid exact contracts but do not match an interval.

The caller declares acceptance of every matching version. The interval does not
prove native ABI compatibility or inspect signatures. Catalog dependencies keep
their exact literal versions, including build metadata. A dependency that is
also a root must satisfy that root's interval. The existing bounded graph search
can reconsider versions after later-root conflicts or invalid metadata graphs.
Catalogs are read once; discovery retains the catalog and 4096-attempt budgets,
cancellation, validation and native-failure behavior described above. The result
pins exact versions and absolute native paths before loading.

The [executed example](../examples/modules/manifest.go) accepts stable `app`
versions from `1.0.0` up to `2.0.0`. Native Go/llgo tests use providers with distinct
results to verify stable/prerelease selection, metadata fallback, retirement and
native failure without fallback. Objects, archives and shared libraries execute
at `-O0` and `-O2`.

## Symbol-selected provider roots

`DiscoverSymbols(ctx, requests, directories...)` selects catalog providers by
exact declared linkage name and opaque ABI version. `SymbolRequirement.Provider`
is optional: when empty, exactly one module must export that symbol at the
requested version. Multiple matches fail, even if one candidate has missing
native files or an invalid dependency graph. Directory order and native loading
attempts never resolve ambiguity. An explicit provider name selects one module;
all local catalog declarations still receive normal validation.

`SymbolSelection.Manifest` is an ordinary complete manifest with exact versions,
absolute input paths and reachable dependencies. `Symbols` preserves request
order, fills each provider name and retains repeated requests; each selected
provider is a manifest root once. Requests for conflicting versions of one module
fail, including conflicts introduced by dependencies. Selection copies the request
records and does not mutate the caller's slice.

The [executed example](../examples/modules/symbols.go) requests a function and a
data export without their module names. Pass the manifest to `Open`, obtain each
provider with `Graph.Session(binding.Provider)`, then use its symbol with the
normal typed adapter or `Bind` signature. This flow reads catalogs once and
loads providers selected by the requested exports; metadata discovery itself
opens no native input and executes no code. A stale declared export is detected
by `Open`, and native failure does not select another provider.

The existing catalog/IO limits apply, with 1-256 symbol requests. Cancellation,
missing/ambiguous contracts, invalid dependencies and exceeded budgets return a
zero selection rather than a partial plan. Matching an export proves neither its
signature nor binary ABI compatibility. The API does not search system paths,
compile providers, demangle names, or add deferred loading to a published session.
Serialize the selection's `Manifest` field when a normal manifest JSON document
is needed; its schema stays unchanged.

Go/llgo CI execute automatic and explicit provider selection with native objects,
archives and complete libraries at `-O0`/`-O2`. Function calls consume retained
provider data, and external function/data leases remain usable while graph
lookup is retired. Tests also cover native missing inputs and stale exports
without fallback, catalog ambiguity, deterministic diagnostics, exact-version
conflicts, repeated requests, request ownership and cancellation.


`module.NewLazy` can defer the selected graph's native loading until first use;
see [lazy graph ownership](lazy-modules.md). All selected roots load together.
This leaves the core session's publication rules unchanged and does not add
per-symbol mutation to an already published graph.
