package dylib

import "fmt"

// DefineSymbol registers a function/data symbol from another Session and retains
// its code until this session finishes native cleanup. Call it before linking
// the consumer. The provider is already linked by Resolve/Bind and cannot gain
// new definitions, so dependencies follow the existing two-phase model.
//
// Close consumers before their providers, or close them concurrently. Closing a
// provider first waits for its consumers; they can still call retained code.
// This owns code, not mutable data synchronization or native registrations.
func (s *Session) DefineSymbol(name string, symbol *Symbol) error {
	if s == nil || symbol == nil || symbol.owner == nil {
		return ErrClosed
	}
	if symbol.owner == s {
		return fmt.Errorf("dylib: a session cannot import its own symbol")
	}
	lease, err := symbol.Acquire()
	if err != nil {
		return err
	}
	address, err := lease.Address()
	if err != nil {
		lease.Close()
		return err
	}
	s.mu.Lock()
	err = s.define(name, address)
	if err == nil {
		s.symbolDependencies = append(s.symbolDependencies, lease)
		if s.retainedDefinitions == nil {
			s.retainedDefinitions = make(map[string]uintptr)
		}
		s.retainedDefinitions[name] = address
	}
	s.mu.Unlock()
	if err != nil {
		lease.Close()
	}
	return err
}
