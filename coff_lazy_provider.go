package dylib

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Win32 failure classes consumed by the SDK VcppException boundary.
const (
	coffDelayInvalidParameter uint32 = 87
	coffDelayMissingModule    uint32 = 126
	coffDelayMissingProcedure uint32 = 127
)

// Private provider state is separate from Session: DLL detach callbacks can
// resolve imports after Close has detached the session's ordinary libraries.
// No provider lock may span a native loader call or a user hook.
type coffLazyProviders struct {
	mu      sync.Mutex
	paths   []string
	modules map[string]*coffLazyModule
	order   []*coffLazyModule
	loaded  []*coffLazyModule // Native reference acquisition order.
	slots   []*coffLazySlot
	active  map[coffLazyCall]bool
	pending []uintptr // Independently owned duplicate LoadLibrary references.
	ops     coffLazyOperations
	hooks   coffLazyHooks
	reset   func(int, uintptr) // Publish/reset a native cell, if installed.
}

type coffLazyModule struct {
	name, origin string
	handle       uintptr
	owned        bool
}

type coffLazySlot struct {
	entry   coffImport
	module  *coffLazyModule
	address uintptr
}

type coffLazyCall struct{ slot, thread uintptr }

type coffLazyOperations struct {
	open   func(string) (uintptr, error)
	lookup func(uintptr, string, uint16) (uintptr, error)
	close  func(uintptr) error
}

type coffLazyEvent = DelayImportEvent

const (
	coffLazyStart         = DelayImportStartProcessing
	coffLazyBeforeLoad    = DelayImportBeforeLoadLibrary
	coffLazyBeforeLookup  = DelayImportBeforeGetProcAddress
	coffLazyLoadFailure   = DelayImportLoadLibraryFailure
	coffLazyLookupFailure = DelayImportGetProcAddressFailure
	coffLazyEnd           = DelayImportEndProcessing
)

type coffLazyInfo struct {
	dll, symbol, name string
	ordinal           uint16
	module, address   uintptr
	err               error
}

type coffLazyResult struct {
	module, address uintptr
	owned           bool // A hook transfers one acquired native library reference.
}

type coffLazyHooks struct {
	notify, failure func(coffLazyEvent, coffLazyInfo) coffLazyResult
}

// slot identities are canonicalized by DLL/export, independently of public
// aliases. The first selected importer supplies the deterministic DLL origin.
func newCOFFLazyProviders(objects []*object, fixed map[*coffImport]uintptr, paths []string, borrowed map[string]uintptr, ops coffLazyOperations, hooks coffLazyHooks) (*coffLazyProviders, map[*coffImport]int) {
	p := &coffLazyProviders{paths: append([]string(nil), paths...), modules: make(map[string]*coffLazyModule), active: make(map[coffLazyCall]bool), ops: ops, hooks: hooks}
	aliases := make(map[*coffImport]int)
	type identity struct {
		dll, name string
		ordinal   uint16
	}
	selected := make(map[identity]int)
	for _, object := range objects {
		for _, symbol := range object.symbols {
			if symbol.imported == nil {
				continue
			}
			entry := symbol.imported.entry
			if !entry.delay || fixed[entry] != 0 || runtimeHelper(entry.public) {
				continue
			}
			key := strings.ToLower(entry.dll)
			id := identity{key, entry.name, entry.ordinal}
			index, exists := selected[id]
			if !exists {
				module := p.modules[key]
				if module == nil {
					module = &coffLazyModule{name: entry.dll, origin: object.directory, handle: borrowed[key]}
					p.modules[key] = module
					p.order = append(p.order, module)
				}
				index = len(p.slots)
				selected[id] = index
				p.slots = append(p.slots, &coffLazySlot{entry: *entry, module: module})
			}
			aliases[entry] = index
		}
	}
	return p, aliases
}

var errCOFFLazyRecursion = errors.New("recursive resolution of the same delayed import on one native thread")

// resolve runs only while the owning native image is retained. Recovery inputs
// are copied from the native helper's stack-owned DelayLoadInfo after SEH returns.
func (p *coffLazyProviders) resolve(index int, thread uintptr, recovery coffLazyResult) (result coffLazyResult, exception uint32, cause error) {
	if index < 0 || index >= len(p.slots) || thread == 0 {
		return result, coffDelayInvalidParameter, fmt.Errorf("invalid delayed import request")
	}
	slot := p.slots[index]
	call := coffLazyCall{uintptr(index), thread}
	p.mu.Lock()
	if slot.address != 0 {
		result = coffLazyResult{module: slot.module.handle, address: slot.address}
		p.mu.Unlock()
		if recovery.owned && recovery.module != 0 {
			p.releaseDuplicate(recovery.module)
		}
		return result, 0, nil
	}
	if p.active[call] {
		p.mu.Unlock()
		return result, coffDelayInvalidParameter, errCOFFLazyRecursion
	}
	p.active[call] = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.active, call)
		p.mu.Unlock()
		if value := recover(); value != nil {
			result, exception, cause = coffLazyResult{}, coffDelayInvalidParameter, fmt.Errorf("delayed import hook panic: %v", value)
		}
		if exception != 0 {
			result.address = 0
		}
	}()
	info := coffLazyInfo{dll: slot.entry.dll, symbol: slot.entry.public, name: slot.entry.name, ordinal: slot.entry.ordinal}
	if recovery.module != 0 {
		result.module, cause = p.adopt(slot.module, recovery.module, recovery.owned)
		if cause == nil && recovery.address != 0 && result.module != recovery.module {
			cause = fmt.Errorf("recovery target belongs to a competing delayed module")
		}
		if cause != nil {
			return result, coffDelayInvalidParameter, cause
		}
	}
	if recovery.address != 0 {
		result.address = recovery.address
	} else {
		start := p.notify(coffLazyStart, info)
		if start.owned && start.module == 0 {
			return result, coffDelayInvalidParameter, p.rejectHook(start, "delayed import hook transferred an empty module")
		}
		if start.module != 0 {
			result.module, cause = p.adopt(slot.module, start.module, start.owned)
			if cause == nil && start.address != 0 && result.module != start.module {
				cause = fmt.Errorf("hook target belongs to a competing delayed module")
			}
			if cause != nil {
				return result, coffDelayInvalidParameter, cause
			}
		}
		result.address = start.address
	}
	if result.address == 0 {
		p.mu.Lock()
		result.module = slot.module.handle
		p.mu.Unlock()
		if result.module == 0 {
			loaded := p.notify(coffLazyBeforeLoad, info)
			if loaded.address != 0 || loaded.owned && loaded.module == 0 {
				return result, coffDelayInvalidParameter, p.rejectHook(loaded, "invalid before-load hook result")
			}
			if loaded.module == 0 {
				path, err := importDLLPath(slot.module.name, slot.module.origin, p.paths)
				if err == nil {
					loaded.module, err = p.ops.open(path)
					loaded.owned = loaded.module != 0
				}
				if err != nil || loaded.module == 0 {
					info.err = err
					if info.err == nil {
						info.err = fmt.Errorf("native loader returned an empty module")
					}
					loaded = p.failure(coffLazyLoadFailure, info)
					if loaded.address != 0 || loaded.owned && loaded.module == 0 {
						return result, coffDelayInvalidParameter, p.rejectHook(loaded, "invalid load-failure hook result")
					}
					if loaded.module == 0 {
						return result, coffDelayMissingModule, info.err
					}
				}
			}
			result.module, cause = p.adopt(slot.module, loaded.module, loaded.owned)
			if cause != nil {
				return result, coffDelayInvalidParameter, cause
			}
		}
		info.module, info.err = result.module, nil
		lookup := p.notify(coffLazyBeforeLookup, info)
		if lookup.module != 0 || lookup.owned {
			return result, coffDelayInvalidParameter, p.rejectHook(lookup, "invalid before-lookup hook result")
		}
		result.address = lookup.address
		if result.address == 0 {
			result.address, cause = p.ops.lookup(result.module, slot.entry.name, slot.entry.ordinal)
			if cause != nil || result.address == 0 {
				info.err = cause
				if info.err == nil {
					info.err = fmt.Errorf("DLL %s does not export name=%q ordinal=%d", info.dll, info.name, info.ordinal)
				}
				lookup = p.failure(coffLazyLookupFailure, info)
				if lookup.module != 0 || lookup.owned {
					return result, coffDelayInvalidParameter, p.rejectHook(lookup, "invalid lookup-failure hook result")
				}
				result.address = lookup.address
				if result.address == 0 {
					return result, coffDelayMissingProcedure, info.err
				}
			}
		}
	}
	p.mu.Lock()
	if slot.address == 0 {
		slot.address = result.address
	}
	result.address, result.module = slot.address, slot.module.handle
	p.mu.Unlock()
	// Publish the logical winner before End: its hook may reenter this slot.
	info.module, info.address, info.err = result.module, result.address, nil
	p.notify(coffLazyEnd, info)
	return result, 0, nil
}

func (p *coffLazyProviders) notify(event coffLazyEvent, info coffLazyInfo) coffLazyResult {
	if p.hooks.notify == nil {
		return coffLazyResult{}
	}
	return p.hooks.notify(event, info)
}

func (p *coffLazyProviders) failure(event coffLazyEvent, info coffLazyInfo) coffLazyResult {
	if p.hooks.failure == nil {
		return coffLazyResult{}
	}
	return p.hooks.failure(event, info)
}

func (p *coffLazyProviders) adopt(module *coffLazyModule, handle uintptr, owned bool) (uintptr, error) {
	if handle == 0 {
		return 0, fmt.Errorf("cannot adopt an empty delayed import module")
	}
	p.mu.Lock()
	if module.handle == 0 {
		module.handle, module.owned = handle, owned
		if owned {
			p.loaded = append(p.loaded, module)
		}
		p.mu.Unlock()
		return handle, nil
	}
	winner := module.handle
	p.mu.Unlock()
	if owned {
		// Concurrent LoadLibrary successes each own a reference, even when
		// Windows returns the same numerical HMODULE for all of them.
		p.releaseDuplicate(handle)
	}
	return winner, nil
}

func (p *coffLazyProviders) releaseDuplicate(handle uintptr) {
	if err := p.ops.close(handle); err != nil {
		p.mu.Lock()
		p.pending = append(p.pending, handle)
		p.mu.Unlock()
		// A valid canonical target remains callable. Keep this extra native
		// reference for cleanup retry rather than aborting a successful call.
	}
}

func (p *coffLazyProviders) rejectHook(result coffLazyResult, message string) error {
	if result.owned && result.module != 0 {
		// Ownership has already transferred, even for a malformed result.
		p.mu.Lock()
		p.pending = append(p.pending, result.module)
		p.mu.Unlock()
	}
	return errors.New(message)
}

// unload requires application quiescence, enforced separately by Session's
// active-call/lease gate. Keep the logical target valid through FreeLibrary:
// the module remains mapped while its synchronous detach callback runs.
func (p *coffLazyProviders) unload() error {
	if p == nil {
		return nil
	}
	for {
		p.mu.Lock()
		if len(p.active) != 0 {
			p.mu.Unlock()
			return fmt.Errorf("delayed import resolution is still active")
		}
		pending := p.pending
		p.pending = nil
		p.mu.Unlock()
		remaining, err := closeLibraries(pending, p.ops.close)
		p.mu.Lock()
		p.pending = append(p.pending, remaining...)
		if err != nil {
			p.mu.Unlock()
			return err
		}
		var module *coffLazyModule
		for i := len(p.loaded) - 1; i >= 0; i-- {
			if candidate := p.loaded[i]; candidate.owned && candidate.handle != 0 {
				module = candidate
				break
			}
		}
		if module == nil && len(p.pending) == 0 {
			p.loaded = nil
			for _, borrowed := range p.order {
				p.resetModule(borrowed, true)
			}
			p.mu.Unlock()
			return nil
		}
		if module == nil {
			p.mu.Unlock()
			continue
		}
		handle := module.handle
		p.resetModule(module, false)
		p.mu.Unlock()
		if err := p.ops.close(handle); err != nil {
			p.mu.Lock()
			for index, slot := range p.slots {
				if slot.module == module && p.reset != nil {
					p.reset(index, slot.address)
				}
			}
			p.mu.Unlock()
			return err
		}
		p.mu.Lock()
		module.handle, module.owned = 0, false
		// Detach can have republished a cached native cell; reset it again.
		p.resetModule(module, true)
		p.mu.Unlock()
		// A detach callback can first-load another provider, even one already
		// processed in reverse load order. Select again until all are drained.
	}
}

// Caller holds mu. reset only stores into existing native storage; it must not
// call back into Go or acquire provider/session locks.
func (p *coffLazyProviders) resetModule(module *coffLazyModule, logical bool) {
	for index, slot := range p.slots {
		if slot.module != module {
			continue
		}
		if logical {
			slot.address = 0
		}
		if p.reset != nil {
			p.reset(index, 0)
		}
	}
}
