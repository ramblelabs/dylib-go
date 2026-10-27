package abi

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

var ErrValueClosed = errors.New("ABI native value is closed")

type valueBackend interface {
	pointer() unsafe.Pointer
	layout() Layout
	read() Value
	write(Value) error
	close()
}

// NativeValue owns stable native storage for a host C scalar, ordinary record or
// fixed array. It owns the bytes, not addresses stored in pointer fields or a
// language object's constructors/destructors. It is independent of a Session.
// Acquire a lease before publishing its address to native code. Do not copy it.
type NativeValue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	storage sync.Mutex // Serialize Go reads/writes, not external native access.
	backend valueBackend
	desc    TypeDesc
	layout  Layout
	leases  int
	closing bool
}

// NewNativeValue snapshots the descriptor and copies initial into owned native
// storage, using the same host C layout as LayoutOf(description, CDecl). It
// requires cgo; the 64 KiB storage limit applies. Pointer values
// must be caller-owned native addresses, not AddressOf temporary pointees.
// Close explicitly; no finalizer releases storage retained by native code.
func NewNativeValue(description TypeDesc, initial Value) (*NativeValue, error) {
	if err := description.Validate(); err != nil {
		return nil, err
	}
	if description.Type == Void {
		return nil, fmt.Errorf("void has no native storage")
	}
	if err := validateStoredValue(description, initial); err != nil {
		return nil, err
	}
	description = description.Clone()
	backend, err := prepareValue(description, initial)
	if err != nil {
		return nil, err
	}
	return &NativeValue{backend: backend, desc: description, layout: backend.layout()}, nil
}

func validateStoredValue(description TypeDesc, value Value) error {
	if err := value.Validate(description); err != nil {
		return err
	}
	if hasTemporaryPointees(value) {
		return fmt.Errorf("owned native values cannot contain temporary pointees")
	}
	return nil
}

// Description returns independent logical metadata while the owner is open.
func (v *NativeValue) Description() (TypeDesc, error) {
	if v == nil {
		return TypeDesc{}, ErrValueClosed
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.backend == nil {
		return TypeDesc{}, ErrValueClosed
	}
	return v.desc.Clone(), nil
}

// Layout returns independent native size, alignment and member offsets.
func (v *NativeValue) Layout() (Layout, error) {
	if v == nil {
		return Layout{}, ErrValueClosed
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.backend == nil {
		return Layout{}, ErrValueClosed
	}
	layout := v.layout
	layout.Offsets = append([]uint64(nil), layout.Offsets...)
	return layout, nil
}

// NativeValueLease keeps native storage alive until explicitly released. Remove
// registrations and join native users before Close. Address use must
// not race lease release. Synchronize C access against reads/writes yourself.
// Do not copy it.
type NativeValueLease struct {
	mu      sync.Mutex
	owner   *NativeValue
	backend valueBackend
	desc    TypeDesc
}

// Acquire creates an independent lease. Retirement rejects new acquisitions;
// existing leases remain usable while NativeValue.Close waits for them.
func (v *NativeValue) Acquire() (*NativeValueLease, error) {
	if v == nil {
		return nil, ErrValueClosed
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.backend == nil {
		return nil, ErrValueClosed
	}
	v.leases++
	return &NativeValueLease{owner: v, backend: v.backend, desc: v.desc}, nil
}

// Address returns the stable native address, valid for this lease's lifetime.
func (l *NativeValueLease) Address() (uintptr, error) {
	if l == nil {
		return 0, ErrValueClosed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owner == nil {
		return 0, ErrValueClosed
	}
	return uintptr(l.backend.pointer()), nil
}

// Pointer returns the leased native address for typed cgo/llgo adapters, without
// an integer-to-pointer conversion. Its lifetime is the same as Address.
func (l *NativeValueLease) Pointer() (unsafe.Pointer, error) {
	if l == nil {
		return nil, ErrValueClosed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owner == nil {
		return nil, ErrValueClosed
	}
	return l.backend.pointer(), nil
}

// Read copies storage into independent logical Go data. Pointer fields remain
// borrowed addresses; this does not extend their owners' lifetimes.
func (l *NativeValueLease) Read() (Value, error) {
	if l == nil {
		return Value{}, ErrValueClosed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owner == nil {
		return Value{}, ErrValueClosed
	}
	l.owner.storage.Lock()
	defer l.owner.storage.Unlock()
	value := l.backend.read()
	if err := value.Validate(l.desc); err != nil {
		return Value{}, fmt.Errorf("native value read: %w", err)
	}
	return value, nil
}

// Write replaces the value in the same allocation and clears native padding.
// It neither acquires nor frees pointers supplied by the caller.
func (l *NativeValueLease) Write(value Value) error {
	if l == nil {
		return ErrValueClosed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owner == nil {
		return ErrValueClosed
	}
	if err := validateStoredValue(l.desc, value); err != nil {
		return err
	}
	l.owner.storage.Lock()
	defer l.owner.storage.Unlock()
	return l.backend.write(value)
}

// Close releases this lease, after native users have stopped accessing storage.
// It is idempotent and does not close the owner.
func (l *NativeValueLease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owner != nil {
		owner := l.owner
		l.owner, l.backend, l.desc = nil, nil, TypeDesc{}
		owner.mu.Lock()
		owner.leases--
		if owner.leases == 0 && owner.cond != nil {
			owner.cond.Broadcast()
		}
		owner.mu.Unlock()
	}
	return nil
}

// Read acquires a short lease and returns an independent logical snapshot.
func (v *NativeValue) Read() (Value, error) {
	lease, err := v.Acquire()
	if err != nil {
		return Value{}, err
	}
	defer lease.Close()
	return lease.Read()
}

// Write acquires a short lease and replaces storage without changing its address.
func (v *NativeValue) Write(value Value) error {
	lease, err := v.Acquire()
	if err != nil {
		return err
	}
	defer lease.Close()
	return lease.Write(value)
}

// WithAddress holds a lease for synchronous native use. Native workers and
// registrations must be stopped before use returns. Do not close this owner
// inside use; Close waits for this lease and would wait for itself.
func (v *NativeValue) WithAddress(use func(uintptr) error) error {
	if use == nil {
		return fmt.Errorf("nil native value address user")
	}
	lease, err := v.Acquire()
	if err != nil {
		return err
	}
	defer lease.Close()
	address, err := lease.Address()
	if err != nil {
		return err
	}
	return use(address)
}

// Close retires the owner and waits for leases before freeing storage and its
// native layout. Existing leases can read/write during retirement. Unregister
// native users before releasing them; Close cannot revoke a retained pointer.
func (v *NativeValue) Close() error {
	if v == nil {
		return nil
	}
	v.mu.Lock()
	for v.closing {
		v.waitLeases()
	}
	if v.backend == nil {
		v.mu.Unlock()
		return nil
	}
	backend := v.backend
	v.backend, v.closing = nil, true
	for v.leases != 0 {
		v.waitLeases()
	}
	v.desc, v.layout = TypeDesc{}, Layout{}
	v.mu.Unlock()
	backend.close()
	v.mu.Lock()
	v.closing = false
	if v.cond != nil {
		v.cond.Broadcast()
	}
	v.mu.Unlock()
	return nil
}

func (v *NativeValue) waitLeases() {
	if v.cond == nil {
		v.cond = sync.NewCond(&v.mu)
	}
	v.cond.Wait()
}
