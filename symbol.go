package dylib

import "fmt"

// Symbol is a resolved native function or data symbol owned by a Session.
// It carries no type information or calling convention. Use WithAddress for
// synchronous use, or Acquire to retain its code across separate native calls.
type Symbol struct {
	owner   *Session
	address uintptr
}

// Resolve links on first use and resolves name once. The returned handle is
// valid until Close; neither the symbol name nor its address implies a type.
func (s *Session) Resolve(name string) (*Symbol, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.lookup(name)
	if err != nil {
		return nil, err
	}
	return &Symbol{owner: s, address: p}, nil
}

// WithAddress runs use while retaining the session's code and library handles.
// Concurrent and reentrant use is supported. The callback must finish all
// address use before returning and must not close its owning session.
// Native calling conventions, types,
// pointer ownership and exception boundaries remain the adapter's responsibility.
// A nil or closed Symbol returns ErrClosed; a nil callback is an error.
func (s *Symbol) WithAddress(use func(uintptr) error) error {
	if s == nil || s.owner == nil {
		return ErrClosed
	}
	s.owner.mu.Lock()
	if s.owner.closed {
		s.owner.mu.Unlock()
		return ErrClosed
	}
	if s.owner.unloading {
		s.owner.mu.Unlock()
		return ErrDelayImportsBusy
	}
	if use == nil {
		s.owner.mu.Unlock()
		return fmt.Errorf("dylib: nil symbol callback")
	}
	s.owner.calls++
	s.owner.mu.Unlock()
	defer s.owner.releaseCall()
	return use(s.address)
}
