# Apple libraries through a C facade

Swift and Objective-C++ libraries can expose C-compatible functions while keeping
their objects and native calls inside the producing language. macOS amd64/arm64
Go and llgo tests exercise this boundary through `Session.Bind` and libffi.
This does not add native Swift signatures, Objective-C message dispatch, or an
ABIBridge dependency to the Go loader.

The matching [Swift](../testdata/applefacade/exports.swift) and
[Objective-C++](../testdata/applefacade/exports.mm) implementations provide the
[same C interface](../testdata/applefacade/exports.h):

| Entry | Contract |
| --- | --- |
| `apple_create(initial, observer)` | Return one owned opaque counter reference; the optional C observer is borrowed |
| `apple_retain(handle)` | Add one reference and return the same handle; null stays null |
| `apple_release(handle)` | Consume one reference; null is inert |
| `apple_add(handle, delta)` | Update the counter and synchronously call its observer; no observer means return the value directly |
| `apple_live()` | Report the fixture's live object count for lifecycle checks |

The Swift side uses `Unmanaged` for explicit reference ownership and `@_cdecl`
for C entry points. The Objective-C++ side uses ARC bridge casts, autorelease
pools, and ordinary Objective-C methods behind `extern "C"` declarations.
Go stores an opaque native address; it never interprets or copies object storage.
Final destruction invokes the borrowed observer with the fixture's reserved `-1`
event, so the observer must remain leased through the final release.

## Owning a language object

Bind the factory, object operations and release function, then create a
`Registration` containing their `Function` owners and the `abi.Callback` owner.
Publish callback addresses and create objects inside `WithLeases`. Its stop
operation releases every owned object reference before releasing the callback
and code leases. Keep the registration handle after a stop failure and retry
`Close`; ordinary uses are already retired. See [registration ownership](registrations.md).

The [executed Go test](../languages_apple_test.go) retains two references, forces
Go GC, calls the object and its captured Go observer, and releases one reference.
It then retires both the session and callback owners. Normal calls are rejected,
but registration leases still permit language destruction. An injected cleanup
failure keeps the object/code/observer alive for a retry. The final release must
invoke the observer exactly once and reduce the live object count to zero before
the retired owners finish closing. Null handles and observers are also checked.

The fixture uses `Options{KeepLibraries: true}`. Swift/ObjC runtimes can retain
type/class metadata beyond the last application-owned object; object destruction
does not establish that unloading such a runtime image is safe. The session still
retires its Go operations and plans, while the OS image reference remains pinned
until process exit. Distinct test builds use distinct Swift module and ObjC class
names because multiple runtime-registered images remain loaded.

## Execution and limits

Select the producers with `DYLIB_TEST_LANGUAGES=swift,objc` and run
`go test -tags libffi -run '^TestAppleCFacadeProducers$' -v .`, or the same command
with `llgo`. CI selects both on its Intel and Apple Silicon macOS runners.
`DYLIB_SWIFTC` and `DYLIB_OBJCXX` override the compiler executables; selecting a
missing compiler fails. Swift unoptimized/optimized and ObjC++ `-O0`/`-O2` dylibs
execute independently. Their raw runtime-registration objects remain rejected.

This example serializes all native object operations and uses synchronous C
callbacks. It has no UI/main-thread requirement. An adapter for a UI API must
separately enforce its thread contract. Native async tasks, Swift closures,
generics, thrown exceptions, SwiftUI values, arm64e authentication and non-macOS
Apple devices are outside this qualification. The separate optional
[ABIBridge adapter](abibridge.md) executes explicit thin Swift calls, including
guaranteed managed arguments and owned Copyable results through metadata and value witnesses;
these opaque-object fixtures do not execute ABIBridge. Additional Swift ownership conventions,
runtime ownership and devices still need their own adapters and tests.
