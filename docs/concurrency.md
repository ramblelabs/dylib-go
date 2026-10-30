# Concurrent calls and owner retirement

`Symbol.WithAddress`, `Function.Call`, and `abi.CallPlan.Call` may overlap or
reenter the same owner. A Go callback can resolve/bind another function in its
calling session or recursively call the same bound Function. The owner mutex
protects state changes and active-use counts; it is released before invoking
adapters/native code.

Prepared libffi CIFs and layouts are immutable after preparation. Calls have
separate argument/result buffers and temporary native pointee copies. This
follows [libffi's thread-safety rules](https://github.com/libffi/libffi/blob/v3.4.8/doc/libffi.texi#thread-safety):
prepare each owned type graph before publication, then share its prepared CIF.
Concurrent callers must still synchronize mutable Go pointees, callback
captures, and native state that their C API does not permit sharing.

## Close and retirement

| Owner state | New calls/lookups/leases | Existing calls/leases | Cleanup |
| --- | --- | --- | --- |
| Open | Accepted | May overlap or nest | None |
| Unpublished rollback failed | Stored link and cleanup error | No image published | Retains image, libraries and provider leases; call `Close` |
| Retiring after Close starts | `ErrClosed` | Retain resources; existing lease calls can finish unregister/join | Waits without an invocation lock |
| Cleanup failed | `ErrClosed` | None | Retains unfinished image/library resources and provider leases; retry `Close` |
| Closed | `ErrClosed` | None | Completed |

`Session.Close` retires the owner before waiting. A callback racing close can
query the session and receive `dylib.ErrClosed` immediately. It cannot start a
new call while the outer native invocation remains active. Existing uses finish
normally, including result conversion/copy-back, before unmapping or DLL release.
`CallPlan.Close` applies the same protocol to its prepared ABI resources and
returns `abi.ErrClosed` for new calls. Concurrent close callers wait for the
same cleanup attempt and receive its result. Failed session cleanup can be retried
with another `Close`; completed finalizers do not repeat. A failed image cleanup
keeps its OS libraries and provider leases alive. Failed library releases keep
their handles and dependencies for retry. A panic in a Go address adapter releases its
active-use guard while preserving the panic.

`CallPlan.Share` gives each logical plan an independent retirement state while
retaining one native resource group. Closing one plan does not retire its
siblings. The final plan waits for its calls and releases the group's layouts
and idle pools once. Sharing can begin while the original has an active call.
See [shared call resources](shared-plans.md) for signature compatibility rules.

Session finalizers run outside the session mutex after retirement. They may
observe `ErrClosed` from session methods without a lock cycle. Call `Close`
outside an owner's own adapters, native handlers, or finalizers: close waits
for those operations and cannot wait for itself. Other resource owners retain
their own contracts; in particular, do not reacquire/close a callback lease
from its handler when a callback close could be pending.

## Boundaries

`Symbol.Acquire` and `Function.Acquire` retain the session across separate calls.
Their leases remain usable during session retirement, including native cleanup,
then reject new uses and wait for active lease calls when released.
`Registration` groups code, value and callback leases with an explicit stop
operation; it waits for Go operations before unregister/join, and retains every
lease if stop fails. See [retained registrations](registrations.md).

Synchronous guards alone do not own arbitrary pointers retained by native
code. Join workers and remove native registrations before releasing leases.
Raw `Lookup` addresses require caller-managed lifetimes.
`DefineSymbol` imports retain their provider through consumer cleanup. Close
consumers first, or concurrently with providers; consumer code leases transitively
retain imported functions/data during retirement. See [dependencies](dependencies.md).
Loading and linking remain serialized staging operations; constructors during
OS load or raw initialization must not reenter the loading session. Supporting
staged-symbol lookup during initialization requires a separate linking state.

CI executes same-Function recursion for objects, archives, and OS libraries,
simultaneous native calls, independent struct-pointer copies, close during a
callback, and finalizer queries. A C library also starts four native threads,
each performing C → Go → same-session C → Go calls through a shared plan. The
existing Go and llgo platform suites execute these tests, including Go 386.

`abi.NativeValue` follows the same retirement sequence for native storage.
`Close` rejects new leases and waits for existing leases before freeing bytes and
layouts. Existing leases can read/write during retirement. Go reads/writes are
serialized; foreign memory access still needs application synchronization. See
[owned native values](native-values.md).
