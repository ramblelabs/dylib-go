package dylib

import (
	"fmt"
	"sync"
)

// SymbolLease retains the entire linked image, libraries and session-owned call
// plans until explicitly released. Acquire before publishing a native address,
// then unregister and join native users before Close. Do not copy it.
type SymbolLease struct {
	mu      sync.Mutex
	cond    *sync.Cond
	owner   *Session
	address uintptr
	uses    int
	closing bool
}

// Acquire retains an open symbol independently of synchronous WithAddress use.
// Session.Close rejects new acquisitions and waits for existing leases. An
// existing lease remains usable while its session retires.
func (s *Symbol) Acquire() (*SymbolLease, error) {
	if s == nil || s.owner == nil {
		return nil, ErrClosed
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	if s.owner.closed {
		return nil, ErrClosed
	}
	if s.owner.unloading {
		return nil, ErrDelayImportsBusy
	}
	s.owner.calls++
	return &SymbolLease{owner: s.owner, address: s.address}, nil
}

// Address returns the leased native address. Raw address use must stop before
// lease release; use WithAddress to guard individual adapter invocations.
func (l *SymbolLease) Address() (uintptr, error) {
	if l == nil {
		return 0, ErrClosed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owner == nil {
		return 0, ErrClosed
	}
	return l.address, nil
}

// WithAddress guards an adapter invocation, including during session retirement.
// Concurrent and reentrant invocations are supported without holding a mutex
// across use. Close this lease outside its own adapters and callbacks.
func (l *SymbolLease) WithAddress(use func(uintptr) error) error {
	if l == nil {
		return ErrClosed
	}
	l.mu.Lock()
	if l.owner == nil {
		l.mu.Unlock()
		return ErrClosed
	}
	if use == nil {
		l.mu.Unlock()
		return fmt.Errorf("dylib: nil symbol lease callback")
	}
	address := l.address
	l.uses++
	l.mu.Unlock()
	defer l.releaseUse()
	return use(address)
}

func (l *SymbolLease) releaseUse() {
	l.mu.Lock()
	l.uses--
	if l.uses == 0 && l.cond != nil {
		l.cond.Broadcast()
	}
	l.mu.Unlock()
}

// Close retires the lease, waits for its active adapters, then releases the
// session retention. It is idempotent and does not close the session. It cannot
// revoke an address retained by native code: unregister and join first.
func (l *SymbolLease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	for l.closing {
		l.waitUses()
	}
	if l.owner == nil {
		l.mu.Unlock()
		return nil
	}
	owner := l.owner
	l.owner, l.address, l.closing = nil, 0, true
	for l.uses != 0 {
		l.waitUses()
	}
	l.mu.Unlock()
	owner.releaseCall()
	l.mu.Lock()
	l.closing = false
	if l.cond != nil {
		l.cond.Broadcast()
	}
	l.mu.Unlock()
	return nil
}

func (l *SymbolLease) waitUses() {
	if l.cond == nil {
		l.cond = sync.NewCond(&l.mu)
	}
	l.cond.Wait()
}
