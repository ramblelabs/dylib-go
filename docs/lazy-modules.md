# Loading a graph on first use

`module.NewLazy(build)` owns one graph initialized by the first `WithGraph`.
Construction stores the builder and performs no loading. The builder may discover,
compile and open providers explicitly; it must return a fresh, unowned graph.
Every non-nil returned graph transfers to the lazy owner, including on error.
Captured inputs remain caller-owned and must stay valid until initialization;
construction does not snapshot files, manifests or options.

The [executed example](../examples/modules/lazy.go) calls `DiscoverSymbols` during
construction, then opens its exact manifest on first use. Catalog discovery reads
metadata; native files are opened later. Its context controls discovery, rather
than the later `Open`/OS loader. The whole selected graph, including every root
and dependency, loads as a unit. It does not add independent per-symbol loading,
signature inference or mutation of a published session.

| Operation | Behavior |
| --- | --- |
| `NewLazy(build)` | Store a non-nil explicit builder; no native loading |
| First `WithGraph(use)` | Run the builder once and borrow the successful graph |
| Concurrent first uses | Wait for the same initialization and failure cleanup |
| Later `WithGraph` | Borrow the initialized graph, or return its cached error |
| `Close` before first use | Retire admission and discard the builder without invoking it |
| `Close` during initialization | Retire admission, wait for the builder, and clean its result without admitting pending scopes |
| `Close` after use | Wait for scopes, then close the graph and external native leases |
| `Close` after failed cleanup | Retry retained cleanup; admission remains retired |

## Initialization and failure

Initialization runs without the admission mutex. A builder can use the existing
`Open`, `Load`, compiler adapters and graph ownership rules. Native initialization
has side effects; neither this owner nor ordinary graph rollback undoes them.
An error is cached, so subsequent uses do not retry initialization, recompile a
provider or try a fallback image. To attempt a new initialization, finish closing
the failed owner and explicitly create another owner.

A returned candidate with an error is closed. Failed cleanup remains owned until
a later `Close` succeeds, including retired graphs returned by a failed `Open`.
The original failure and candidate cleanup error are returned together. Close
reports its own cleanup result rather than replaying a cached load error.

A builder panic propagates to its initiating caller. Waiting/later users receive
`ErrInitializationAborted`; a builder that exits without returning has the same
cached outcome. Waiters are released rather than left blocked on initialization.
The builder owns any resources it has not returned, including if it panics before
returning. Waiting for the builder or OS loader is not cancellable by `WithGraph`;
builders may capture a context for operations that support cancellation.

## Scopes, reentry and close

`WithGraph` borrows the graph and its sessions until the callback returns or
panics. Do not retain or close those borrowed owners independently. Explicit
symbol/function leases may outlive a scope and retain native code/data through
close. Native objects, registrations, asynchronous activity and their stop/join
contracts remain application-owned, as with ordinary module graphs.

Loaded scopes may nest or reenter through native callbacks. No admission mutex
is held across user callbacks, native calls or graph cleanup. Close immediately
rejects new scopes, even while initialization or existing scopes are still
running. Existing scopes finish with the same graph; pending initial users cannot
borrow a graph returned after retirement. In-flight users receive `ErrClosed`,
joined with an initialization error when applicable. Concurrent closes share
one attempt; failed graph cleanup stays owned for retry.

Call Close outside this owner's scopes, builder and native callbacks. During
initialization, neither the builder nor its callbacks may call this owner's
`WithGraph` or `Close`: initialization would wait for itself. Finalizer reentry
through `WithGraph` observes retirement and receives `ErrClosed`. Do not copy a
lazy owner; nil/zero owners reject use, and their Close is harmless.

Go/llgo CI execute native objects, archives and libraries at `-O0`/`-O2`:
concurrent first calls share a graph, native initializers run once, loaded scopes
nest, external code/data leases survive retirement, missing native inputs remain
a cached failure after the file becomes available, and a new explicit owner can
initialize afterward. Tests also cover unused close, initialization/close races,
callback/builder panics, builder exit, failed candidate ownership, provider cleanup
ordering, repeated close and cleanup retry.
