# Preparing selected provider inputs

`module.OpenCompiled(ctx, base, manifest, options, compile)` prepares the selected
graph's inputs with an explicit Go `InputCompiler`, then uses ordinary `Open`.
The compiler can compile source/IR/archives or return native inputs unchanged.
The [executed LLVM example](../examples/modules/compile.go) uses the optional
`compiler/llvm` package for each selected IR/bitcode file. The loader keeps its
independent Go implementation; callers choose installed tools and their options.

| Phase | Contract |
| --- | --- |
| Validate | Check every manifest declaration, including unused definitions, before invoking the compiler |
| Select | Copy selected manifest records and resolve their input paths against `base` |
| Prepare | Invoke the compiler once per selected file, with absolute paths, in provider order; unused modules remain unprepared |
| Load | Open the graph after every preparation succeeds; ordinary exports, imports and host/ABI checks apply |
| Close | Drain native leases and close consumers/providers before compiler artifacts, then close artifacts in reverse preparation order |
| Retry | Retain failed native cleanup and its artifacts, or a failed artifact and earlier artifacts, for a later `Graph.Close` |

Each compiler returns an `InputArtifact` with an absolute loadable `Path` and an
optional `Owner`. A native pass-through input can have no owner. Every non-nil
returned owner transfers, including when the callback also returns an error.
Return a fresh owner for each invocation and keep unreturned resources under
the compiler's own cleanup. File ownership is explicit; the library does not
delete original inputs merely because they were used by a compiler.

Preparation failures do not load the native graph. Returned artifacts are closed;
failed cleanup returns a non-nil retired graph and the combined error. A native
load failure uses the same rollback/retained-owner rule. On successful loading,
artifacts stay owned through native close, including OS library files that cannot
be deleted while loaded on Windows. Successful cleanup is never replayed during
a retry. An owner controls deletion policy for inputs intentionally kept alive
through options such as `KeepLibraries`.

Use returned errors for compiler failures. Panics and goroutine exit propagate
after best-effort cleanup of earlier returned artifacts; cleanup errors during
that unwinding cannot be returned through the normal result. Artifact closers
must return normally and support retry after an error.

Cancellation observed during preparation stops further inputs and prevents entry
into `Open`. Once native loading starts, the OS loader and initialization follow ordinary `Open`
semantics. Files and options must stay valid during the call. Original dependency
directories stay explicit in `options.LibraryPaths`: temporary compiler paths
do not infer which native dependency provider to use.

`NewLazy` can defer an `OpenCompiled` builder until first use. Its captured context
and compiler inputs must remain valid until initialization. That owner shares
one compilation/loading result, caches failures, and retains failed candidate
cleanup under the [lazy graph contract](lazy-modules.md).

Go/llgo CI execute text IR, bitcode and mixed bitcode/native archives at `-O0` and
`-O2`, retaining archive selection, initialization, imported code/data and native
leases. Tests also execute owned copies of OS libraries, verify deletion after
native close, and exercise preparation errors, cancellation, record snapshots,
callback aborts, native load failure and artifact cleanup retry.
An actual LLVM compilation failure combined with filesystem cleanup failure
returns a retired graph; after recovery, its `Close` removes the retained tree.
