# Internal COFF delayed-call entries

The assembler files are independent references for the Go instruction emitter.
They separate the fast leaf range from the register-saving slow function. The
x64 epilogue uses a REX.W-prefixed memory-indirect jump accepted by Windows unwinders.
ARM64 publishes and reads the canonical cell through the native interlocked and
acquire operations. The 386 entry restores the original incoming stack.

`probe.c` supplies real typed C targets and callers, including stack spills,
ordinary and homogeneous aggregates, indirect large results, pointers and
variadic arguments. x86 callers also exercise vectorcall; 386 additionally tests
stdcall, fastcall and thiscall. Volatile entry loads retain actual indirect
calls under optimization. Go tests use the public Windows `syscall.NewCallback`
boundary and perform managed allocation and collection on C-created threads.

Windows x64/ARM64 `RtlVirtualUnwind` controls reconstruct caller contexts at
partial prologues, ordinary function bodies and partial epilogues. Vectored
handlers inspect the SDK's actual `DelayLoadInfo` and recover missing modules
and procedures, including consecutive recovery steps. The test pairs its vectored
exception handler with a continue handler scoped to the recovered record and
native thread: Go's final continue handler would otherwise terminate the host
even after native recovery. Separate subprocesses check unhandled module,
procedure and invalid-parameter native exception codes. A Go host reports the
exact native exception before exiting with its runtime's status 2; other hosts
can retain the native exception status.

The native bridge uses the installed SDK's `delayimp.h`; the native
`DelayLoadInfo` layout is not manually duplicated in Go. These tests qualify the internal entry/transport
boundary. Image-owned lazy provider resolution, hook policy and explicit unload
are separate integration work. The public raw delay policy currently supports
disabled and eager modes.

References: [Windows x64 prologues and epilogues](https://learn.microsoft.com/en-us/cpp/build/prolog-and-epilog),
[ARM64 exception handling](https://learn.microsoft.com/en-us/cpp/build/arm64-exception-handling),
[delay helper contract](https://learn.microsoft.com/en-us/cpp/build/reference/understanding-the-helper-function),
and [native error handling](https://learn.microsoft.com/en-us/cpp/build/reference/error-handling-and-notification).
