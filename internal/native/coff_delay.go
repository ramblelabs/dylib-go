package native

// COFFDelayRequest is a synchronous snapshot of a native helper's stack-owned
// request. Neither the request pointer nor its native DelayLoadInfo may escape
// the public callback invocation.
type COFFDelayRequest struct {
	Owner, Slot, Thread uintptr
	Recovering          bool
	Module, Address     uintptr
	PreviousModule      uintptr // Module before the current native recovery exception.
	NativeError         uint32
	Exception           uint32
}
