# Selecting a provider when its symbol is first used

`module.NewLazySymbol(ctx, request, build, directories...)` owns one exact
function/data symbol contract. Its first `WithSymbol` discovers a provider,
loads that provider's graph through the explicit Go builder, and verifies the
selected export before borrowing it. Concurrent first users share this result.
The constructor validates arguments and freezes absolute directory paths without
reading catalogs or native files.

The [executed examples](../examples/modules/lazy_symbol.go) use ordinary `Open`
and optional `OpenCompiled`. The compiler-backed example prepares selected inputs
only when that symbol is first used. Options, compiler inputs and the captured
context must stay valid until initialization.

| Operation | Behavior |
| --- | --- |
| Construct | Copy the request, freeze directory paths and retain the builder |
| First use | Apply `DiscoverSymbols`' exact version, provider disambiguation and bounded metadata rules; then load and check the export |
| Concurrent/later uses | Share initialization and borrow the selected Symbol through the callback |
| Discovery/build/export failure | Cache the error; changed catalogs/files do not retry this owner |
| New explicit owner | Select again from the catalogs available on its own first use |
| Unused close | Discard the builder without discovering or loading anything |
| Close during initialization/use | Retire admission, drain initialization/scopes, then close the graph and external native leases |
| Failed cleanup | Retain ownership for a later `Close` retry |

The builder receives a pinned `SymbolSelection` and must load its exact manifest
into a fresh, unowned graph. Every non-nil returned graph transfers, including on
error. The owner keeps its selected provider binding even if the builder mutates
the supplied selection records. Provider/session and export checks reject wrong
graphs, while ABI versions remain declared contracts rather than native signature
inference. Native initialization effects cannot be undone by rollback.

Each owner selects one symbol's complete provider graph, including dependencies.
Separate owners have independent selections and graph sessions; they do not pool
providers. Raw images can therefore initialize separately, while complete OS
libraries retain their normal OS reference sharing. Group symbols in an explicit
`NewLazy` graph when they must share one graph/state instance. An unused symbol
owner does not load its native inputs.

`WithSymbol` borrows a generic `dylib.Symbol`, allowing caller-defined adapters
and dynamic signatures for functions, or native-address access for data. Keep
the borrowed Symbol within the callback. Explicit symbol/function leases can
outlive scopes and retain code/data during retirement. Ordinary native object,
registration and asynchronous stop/join contracts still apply.

The initialization context controls discovery, is passed to the builder, and is
checked before publication. Observed cancellation retains a returned graph for
cleanup instead of publishing it. Native OS loading itself is not cancellable.
After initialization succeeds, context cancellation does not retire the owner.
Call `Close` for retirement. Native files/catalogs are selected on first use and
are not snapshotted by construction.

Loaded callbacks may nest. Call Close outside this owner's builder/scopes/native
callbacks; builder/initialization/failure-cleanup callbacks must not wait on this
same owner's `WithSymbol` or `Close`. The [lazy graph rules](lazy-modules.md) govern
panic/goroutine exit, cached aborts, concurrent close and cleanup retry. Nil/zero
symbol owners reject use and close harmlessly.

Go/llgo tests execute object/archive/library function and data selections at
`-O0`/`-O2`, concurrent first calls, loaded reentry, initialization counts,
post-initialization context cancellation and external leases during close. They
also cover delayed catalog installation, cached discovery/native failures,
explicit recovery through a fresh owner, wrong provider/export graphs, unused
owners, and first-use LLVM IR/bitcode compilation with graph-owned artifacts.
