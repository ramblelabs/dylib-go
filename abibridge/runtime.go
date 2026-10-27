package abibridge

import (
	"fmt"
	"sync"

	dylib "github.com/ramblelabs/dylib-go"
)

// Runtime owns an optional native facade built by compiler/abibridge.Build.
// Close waits for active operations and releases its prepared interfaces.
// A Runtime must not be copied or closed from one of its active native calls.
type Runtime struct {
	mu           sync.Mutex
	cond         *sync.Cond
	closed       bool
	users        int
	done         chan struct{}
	closeErr     error
	session      *dylib.Session
	prepare      uintptr
	invoke       uintptr
	invokeValues uintptr
	release      uintptr
	interfaces   map[*Call]bool
	valueOps     valueEntries
	valueTypes   map[*ValueType]bool
	values       map[*Value]bool
}

// Open loads a compatible facade. Neither ABIBridge nor libffi is a default Go
// build dependency; the facade owns its private native call implementation.
func Open(library string) (*Runtime, error) {
	if !nativeAvailable() {
		return nil, ErrUnsupported
	}
	r := &Runtime{session: dylib.New(dylib.Options{}), done: make(chan struct{}), interfaces: make(map[*Call]bool)}
	r.cond = sync.NewCond(&r.mu)
	if err := r.session.Load(library); err != nil {
		r.session.Close()
		return nil, err
	}
	var version uintptr
	for name, output := range map[string]*uintptr{
		"dylib_abibridge_version": &version,
		"dylib_abibridge_prepare": &r.prepare,
		"dylib_abibridge_invoke":  &r.invoke,
		"dylib_abibridge_release": &r.release,
	} {
		symbol, err := r.session.Resolve(name)
		if err == nil {
			err = symbol.WithAddress(func(address uintptr) error { *output = address; return nil })
		}
		if err != nil {
			r.session.Close()
			return nil, err
		}
	}
	facadeVersion := nativeVersion(version)
	if facadeVersion != 1 && facadeVersion != 2 && facadeVersion != 3 {
		r.session.Close()
		return nil, fmt.Errorf("abibridge: incompatible native facade")
	}

	if facadeVersion >= 2 {
		for name, output := range map[string]*uintptr{
			"value_type": &r.valueOps.prepare, "value_type_release": &r.valueOps.releaseType,
			"value_new": &r.valueOps.newValue, "value_pointer": &r.valueOps.pointer,
			"value_commit": &r.valueOps.commit, "value_release": &r.valueOps.release,
		} {
			symbol, err := r.session.Resolve("dylib_abibridge_" + name)
			if err == nil {
				err = symbol.WithAddress(func(address uintptr) error { *output = address; return nil })
			}
			if err != nil {
				r.session.Close()
				return nil, err
			}
		}
	}
	if facadeVersion >= 3 {
		symbol, err := r.session.Resolve("dylib_abibridge_invoke_values")
		if err == nil {
			err = symbol.WithAddress(func(address uintptr) error { r.invokeValues = address; return nil })
		}
		if err != nil {
			r.session.Close()
			return nil, err
		}
	}
	return r, nil
}

func (r *Runtime) begin() error {
	if r == nil {
		return ErrClosed
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.session == nil {
		return ErrClosed
	}
	r.users++
	return nil
}

func (r *Runtime) end() {
	r.mu.Lock()
	r.users--
	r.cond.Broadcast()
	r.mu.Unlock()
}

// Call owns a reusable native Swift interface. It retains its Runtime's native
// code until Close; Invoke separately leases the selected function's image.
// A Call must not be copied or closed from its own active invocation.
type Call struct {
	mu        sync.RWMutex
	owner     *Runtime
	plan      uintptr
	signature Signature
}

// Prepare snapshots a signature and constructs its native Swift interface.
func (r *Runtime) Prepare(signature Signature) (*Call, error) {
	signature, wire, err := signature.encode()
	if err != nil {
		return nil, err
	}
	if err := r.begin(); err != nil {
		return nil, err
	}
	defer r.end()
	if signature.managed() && r.invokeValues == 0 {
		return nil, ErrManagedUnsupported
	}
	for _, t := range append([]Type{signature.Result}, signature.Args...) {
		if t.ValueType != nil && t.ValueType.owner != r {
			return nil, fmt.Errorf("abibridge: Swift value type belongs to another runtime")
		}
	}
	plan, err := nativePrepare(r.prepare, wire)
	if err != nil {
		return nil, err
	}
	c := &Call{owner: r, plan: plan, signature: signature}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		nativeRelease(r.release, plan)
		return nil, ErrClosed
	}
	r.interfaces[c] = true
	return c, nil
}

// Invoke calls a thin Swift function with native-endian, compiler-confirmed
// storage bytes. Inputs are borrowed and copied for this call; the returned
// bytes are independent. Bytes must describe trivial values. Native pointers
// within them need caller-owned leases for the whole invocation. This method
// never interprets reference-bearing Swift values as transferable byte arrays.
func (c *Call) Invoke(function *dylib.Symbol, arguments ...[]byte) ([]byte, error) {
	if c == nil || c.owner == nil {
		return nil, ErrClosed
	}
	r := c.owner
	if err := r.begin(); err != nil {
		return nil, err
	}
	defer r.end()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.plan == 0 {
		return nil, ErrClosed
	}
	if c.signature.managed() {
		return nil, fmt.Errorf("abibridge: managed signatures require InvokeManaged")
	}
	if len(arguments) != len(c.signature.Args) {
		return nil, fmt.Errorf("abibridge: got %d arguments, want %d", len(arguments), len(c.signature.Args))
	}
	var packed []byte
	for i, argument := range arguments {
		if len(argument) != int(c.signature.Args[i].Size) {
			return nil, fmt.Errorf("abibridge: argument %d has %d bytes, want %d", i, len(argument), c.signature.Args[i].Size)
		}
		packed = append(packed, argument...)
	}
	result := make([]byte, c.signature.Result.Size)
	err := function.WithAddress(func(address uintptr) error {
		return nativeInvoke(r.invoke, c.plan, address, packed, result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Close waits for this interface's invocations, then releases the native plan.
func (c *Call) Close() error {
	if c == nil || c.owner == nil {
		return nil
	}
	r := c.owner
	// Keep membership until native release finishes. A concurrent runtime
	// snapshot waits on c.mu before it can release the facade.
	c.close()
	r.mu.Lock()
	delete(r.interfaces, c)
	r.mu.Unlock()
	return nil
}

func (c *Call) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.plan != 0 {
		nativeRelease(c.owner.release, c.plan)
		c.plan = 0
	}
}

// Close retires operations before releasing interfaces and their native code.
// Function images remain owned by their original dylib sessions.
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.session == nil {
		r.mu.Unlock()
		return nil
	}
	if r.closed {
		r.mu.Unlock()
		<-r.done
		return r.closeErr
	}
	r.closed = true
	for r.users != 0 {
		r.cond.Wait()
	}
	calls, values, types := r.interfaces, r.values, r.valueTypes
	r.interfaces, r.values, r.valueTypes = nil, nil, nil
	r.mu.Unlock()
	// Value witnesses can execute Swift deinitializers and foreign callbacks.
	// Retire first, but never hold a Go mutex across those native operations.
	for value := range values {
		value.close()
	}
	for call := range calls {
		call.close()
	}
	for valueType := range types {
		nativeRelease(r.valueOps.releaseType, valueType.plan)
		valueType.provider.Close()
	}
	err := r.session.Close()
	r.mu.Lock()
	r.closeErr = err
	close(r.done)
	r.mu.Unlock()
	return err
}
