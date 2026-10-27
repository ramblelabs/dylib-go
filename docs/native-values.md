# Owned native storage

For registrations combining retained values, callbacks and code, see
[registration ownership](registrations.md). The registration acquires separate
leases and releases them only after successful unregister/join; resource owners
remain independently owned.

`abi.NewNativeValue(description, initial)` owns stable host C storage for scalars,
ordinary structs and fixed arrays. It snapshots logical metadata and copies the
initial value field by field. It requires cgo with either Go or llgo and does
not require the libffi tag. `abi.StorageAvailable()` reports this capability.
The host C compiler supplies scalar size/alignment; Go computes ordinary record
and array layouts and copies scalar bits into a native allocation. Layouts
match `abi.LayoutOf(description, abi.CDecl)`; the 64 KiB native value limit applies. This is an independent byte-storage owner,
not a Session resource or a C++/Swift object constructor/destructor adapter.

| Operation | Contract |
| --- | --- |
| `Description`, `Layout` | Independent metadata snapshots while the owner is open |
| `Read` | Independent logical Go data; pointer fields remain borrowed native addresses |
| `Write` | Replace fields and clear padding in the same allocation |
| `Acquire` | Lease stable storage until explicitly released |
| Lease `Address` | Use with `abi.Ptr(address)` or a caller-defined adapter; valid only for the lease lifetime |
| Lease `Pointer` | Same lifetime as Address; supplies `unsafe.Pointer` to typed cgo/llgo adapters without an integer conversion |
| Lease `Read`, `Write` | Remain usable during owner retirement; reject a released lease |
| `WithAddress` | Short lease for synchronous code; unregister/join users before the function returns |
| `Close` | Reject new leases/operations, wait for leases, free native bytes/layouts once |

Array storage is permitted even though C array parameters decay to pointers.
Nested records/arrays and opaque pointer fields use compiler/backend layout rules,
not raw Go struct memory. Compare layout snapshots with the producer's C layout
before sharing an address. Unions, packed structs and bitfields need another
adapter. `AddressOf` temporary pointees are rejected, including nested fields;
use independently owned native addresses instead.

Go reads/writes serialize with each other. A lease does not lock out C access or
make a C registration thread safe. Synchronize foreign readers/writers before
reading or replacing fields. `Write` clears padding but does not acquire/free
pointers stored in the value. Self/interior addresses remain valid only while
the corresponding allocation is leased. Reads do not extend pointed-to owners.

Keep leases alive while C retains an address. Remove registrations and join
native workers before releasing a lease. The owner cannot revoke pointers that
uncooperative native code retained. Keep code images and callback owners alive
separately. Native code must not free this allocation. There is no GC finalizer.
Do not call owner `Close` from its own `WithAddress` function or while personally
holding a lease that will only be released after Close returns.

Retirement waits without the owner mutex. Existing leases can read/write until
released; new owner operations return `abi.ErrValueClosed`. Concurrent closes
wait for the same cleanup. Nil/zero owners and released leases return that error;
nil/zero/idempotent Close is harmless. Invalid writes leave storage unchanged.

The [library example](../examples/nativevalue/main.go) publishes a record to C,
calls C again through the retained address, and unregisters before releasing
storage. Run `examples/run.sh <go|llgo> library`; the existing example suite builds
and executes it without `-tags libffi`, printing 42. Native object/archive/library
tests compare C sizes/alignment/offsets, retain addresses across GC and calls, mutate records and
arrays, and read through a C-invoked Go callback. ABI tests cover every scalar,
pointer/nested-array snapshots, stable/interior addresses, invalid writes,
concurrent reads/writes, retirement and unavailable backends. The existing eight
Go and six llgo native targets execute them without separate workflow steps.
