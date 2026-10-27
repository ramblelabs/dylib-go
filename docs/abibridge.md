# Optional native Swift calls

`abibridge.Runtime` prepares explicit native Swift signatures and invokes thin
function symbols from OS-loaded dylibs. This is a separate Swift ABI backend;
`Session.Bind` and C signature strings keep their existing C ABI meaning.

| Contract | Support |
| --- | --- |
| Host and function images | macOS 15.4+, amd64/arm64; complete native dylibs |
| Go / llgo transport | Fixed C facade entries through cgo; no Go `libffi` build tag |
| Scalars | Signed/unsigned 8/16/32/64-bit integers, Float/Double, native addresses, void results |
| Trivial Swift values | Explicit compiler-confirmed sizes, alignment and scalar component offsets; supported natural component layouts |
| Large values / results | Backend-selected indirect storage based on the declared Swift components |
| Formally indirect values | Explicit `Indirect` storage; tested with a small resilient Swift struct |
| Managed arguments / results | Explicit `ValueType` and compiler-confirmed passing shape; borrowed guaranteed arguments and owned results, including direct, mixed integer/floating, large auto-indirect, resilient formally indirect and empty values |
| Owned Swift values | Complete metadata from an explicit C provider, aligned native storage, copy/destroy witnesses, scoped access and retained leases; tested with String, reference-counted members and empty values |
| Concurrency | Reusable interfaces, independent per-call storage, function-image leases, close waits for active invocations |
| Unsupported | Managed values passed directly through `Invoke`, non-Copyable values, managed closures, tuples/packs, inout/consumed parameters, generic calls, async/throws, self contexts, Objective-C dispatch, UI threading, arm64e and devices |

## Build and load

Supply clean local checkouts of [ABIBridge
`4dfbda2`](https://github.com/lynnswap/ABIBridge/tree/4dfbda22afb9a1492e1c5c99933a9defbc5437a4)
and its [ZDLibffi
`e9737c7`](https://github.com/lynnswap/ZDLibffi/tree/e9737c755088a4a3d2ba03f2ca5bfeadff9c09b2)
to `compiler/abibridge.Build`. It checks both revisions and local changes, then
uses the selected Xcode tools. It does not fetch sources or select another SDK.
Keep tool and SDK selection consistent when supplying custom environment flags.

The builder creates a private tools-5.9 SwiftPM manifest for the C++20 native
invocation/value-witness sources and libffi only. It does not compile SDK-specific pointer
hooks or build ABIBridge's Swift runtime/UI products, and leaves upstream
manifests unchanged. Individual objects are archived before linking. Eleven private C
exports implement version checking, call interfaces and owned values. Upstream APIs and
libffi symbols are hidden from this facade's exports.

The version 3 facade adds managed invocation to the version 2 value-witness
entries. Version 1 remains usable for trivial calls; `PrepareValueType` returns
`ErrValueUnsupported` there. Versions 1/2 reject managed signatures and
`InvokeManaged` with `ErrManagedUnsupported`. Incomplete and unknown facade
versions are rejected when opening the runtime.

Open the resulting `Library.Path` with `abibridge.Open`. Close every runtime
before closing its build artifact. The upstream projects retain their own MIT
licenses; preserve their copyright/license notices when distributing compiled
facades. The project's core loader has no default ABIBridge dependency.

The executable [Go library example](../examples/swiftcall/call.go) prepares an
Int32 signature. `Prepare` and `Invoke` support trivial calls; `InvokeManaged`
handles owned values. The [native tests](../abibridge/native_test.go) include mixed integer/floating
records, tail sizes different from C struct sizes, padded results, forty-byte
values, formally indirect storage and twenty parameters exhausting both register
banks. Compiler-emitted IR confirms these are `swiftcc` functions.

## Storage and lifetime

`Type` describes the Swift function's actual storage and formal passing mode.
For a scalar, set `Kind`. For `Storage`, supply size, alignment and scalar
components with verified offsets. Components must have supported natural
aggregate offsets, but the Swift value size need not include C tail padding.
Use `Indirect` only when the actual Swift signature is formally indirect.
There is no automatic C-to-Swift record conversion or runtime type inference.

`Invoke` accepts one byte slice per argument and returns independent result
bytes. All supported hosts are little endian. Descriptions are snapshotted when
prepared. The native facade copies inputs into aligned, zeroed native storage,
dispatches through ABIBridge and copies only the declared result size. Storage
allocation includes aligned stride. Result padding starts zeroed.

Descriptions allow at most 256 arguments, 64 scalar components per value, 1 MiB
per value, 4096-byte alignment and 16 MiB total call storage. Both Go and the
native decoder check bounds. Input byte counts must match exactly.

Bytes must represent trivial values with guaranteed parameter ownership.
Reference-bearing values use the owned storage API below; their representation
bytes are not an ownership transfer. Native pointer values
need separate data/code leases for as long as the pointees can be used.

Swift runtime metadata can outlive a call or session. Load providers with
`Options{KeepLibraries: true}`, as for the existing Apple C facades. `Invoke`
additionally leases the selected symbol for the active call; runtime/interface
close waits for those calls before releasing its native plan. Closing a runtime
or interface from its own invocation is not supported.

## Owned Swift values

`Runtime.PrepareValueType` accepts a symbol implementing the C contract
`const void *metadata(void)`. A compiler-matched Swift adapter can export this
using `@_cdecl` and return its complete type metadata. The caller must establish
that the metadata is valid, complete and describes a Copyable type. Checking
layout bounds cannot make arbitrary metadata pointers safe. There is no type
name lookup or implicit generic binding.

The resulting `ValueType` retains its metadata provider's symbol lease until
runtime close. `Layout` reports Swift size, stride and alignment, including empty
values with zero size and positive stride. Storage is limited to 1 MiB stride
and 4096-byte alignment. `ValueType.New` allocates uninitialized native storage
and calls a supplied initializer with its address. The adapter must initialize
exactly one value on success and leave it uninitialized on error; it must not
panic after initialization. Swift pointer adapters should bind newly allocated
memory to the exact type before initialization.

`Value.Clone` uses the type's copy witness to initialize independent storage.
`Value.Close` uses its destroy witness exactly once, including when the last
strong reference releases a Swift object. `WithAddress` lends initialized native
storage to a matching pointer adapter. `Acquire` retains storage, metadata and
native code; the lease's `Address` or `Pointer` can be used for stored native
registrations until users have unregistered and joined. These pointers do not
transfer ownership. Native code must not destroy or consume borrowed storage.
The [executed example](../examples/swiftvalue/clone.go) closes the original before
reading a copy containing a long Swift String.

Runtime retirement rejects new operations, waits for existing operations and
leases, then destroys values before releasing metadata and native code. Existing
leases remain usable during retirement. Release leases before waiting for value
or runtime close, and close runtimes before their metadata provider sessions.
Value witnesses run without Go mutexes held, allowing a destructor callback to
close other values or call interfaces in the same retiring runtime. Closing the
owner from its own active adapter/destructor is unsupported. Synchronize native
access yourself, including thread affinity and non-Sendable values; leases only
protect lifetime. `Call.Invoke` accepts trivial representations only.

## Managed argument and result calls

Add `ValueType` to a `Type` describing the compiler-confirmed Swift passing
shape. Its size/alignment must exactly match that prepared metadata, and it must
belong to the same runtime. Metadata alone does not identify scalar components
or formal passing mode. Use `Storage` with verified components for fixed values
and `Indirect` for formally indirect values. An empty formally indirect managed
value has size zero and still owns positive-stride native storage.

`Call.InvokeManaged` accepts `Argument{Value: value}` for each managed parameter
and `Argument{Bytes: bytes}` for each trivial parameter. Values must use the exact
`ValueType` instance in the signature. It borrows their initialized native
storage until invocation returns, including repeated arguments sharing a value.
Swift must use guaranteed ownership: consumed/inout parameters are unsupported.
Native access needs caller synchronization, including non-Sendable values.

The returned `Result` contains either trivial `Bytes` or an owned `Value`. Swift
writes a managed result into fresh native storage; native success commits that
value once. No representation bytes pass through Go, and no extra copy or
retain changes the returned ownership. Close the result, or let runtime close
retire it. If runtime retirement wins publication, the initialized result is
destroyed and the call returns `ErrClosed`. Argument, call, runtime and metadata
provider close wait for their active owners. The
[executed example](../examples/swiftvalue/invoke.go) transforms an owned value
and reads its result after closing the input.

## Validation

The macOS Go and llgo CI jobs fetch the two immutable source revisions, build one
facade per job, then run native tests and the real README example. Missing
configured dependencies fail. Go also executes race/lifetime checks. Native
tests run unoptimized/optimized Swift code, narrow and wide scalar boundaries,
floating values, record layouts, pointer allocation/mutation/free, reusable and
concurrent interfaces, malformed native descriptions and close during a blocked
Swift invocation. Managed tests verify copy ownership, final strong-reference
destruction, empty values, failed initialization, concurrent copies, value/runtime/
provider retirement, and metadata preparation/initialization racing retirement.
Managed-call tests verify compiler SIL guaranteed/owned contracts and IR
direct/indirect lowering, aliased arguments, concurrent calls, malformed native
buffers, metadata mismatches, input/call/runtime/provider retirement, and a
returned value surviving input close. Libffi builds also
execute Go callbacks from Swift destruction and cleanup reentry. No-libffi runs
execute the same value ownership tests through the fixed C transport.

Pure Go tests validate metadata and unsupported-host behavior on the full project
matrix. Cross compiling the core or parsing an Apple image does not establish
device, arm64e, or other-OS Swift execution support.
