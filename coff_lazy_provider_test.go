package dylib

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func lazyProviderObjects(entries ...*coffImport) []*object {
	obj := &object{}
	for _, entry := range entries {
		obj.symbols = append(obj.symbols, symbol{imported: &coffImportSymbol{entry: entry}})
	}
	return []*object{obj}
}

func lazyProviderEntry(dll, name string) *coffImport {
	return &coffImport{dll: dll, name: name, public: name, delay: true}
}

func TestCOFFLazyProvidersOwnership(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "provider.dll")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	first := lazyProviderEntry("provider.dll", "add")
	alias := lazyProviderEntry("PROVIDER.DLL", "add")
	ordinal := lazyProviderEntry("provider.dll", "")
	ordinal.ordinal = 42
	ordinary := lazyProviderEntry("other.dll", "ordinary")
	ordinary.delay = false
	fixed := lazyProviderEntry("other.dll", "fixed")
	paths := []string{dir}
	borrowed := map[string]uintptr{}
	var opens, lookups, closes int
	var events []coffLazyEvent
	ops := coffLazyOperations{
		open: func(path string) (uintptr, error) {
			opens++
			if path != file {
				t.Fatalf("provider path %q, want %q", path, file)
			}
			return uintptr(100 + opens), nil
		},
		lookup: func(module uintptr, name string, ord uint16) (uintptr, error) {
			lookups++
			if module != uintptr(100+opens) || name != "add" && ord != 42 {
				t.Fatal("provider identity", module, name, ord)
			}
			return module + 42 + uintptr(ord), nil
		},
		close: func(module uintptr) error { closes++; return nil },
	}
	hooks := coffLazyHooks{notify: func(event coffLazyEvent, info coffLazyInfo) coffLazyResult {
		events = append(events, event)
		return coffLazyResult{}
	}}
	p, ids := newCOFFLazyProviders(lazyProviderObjects(first, alias, ordinal, ordinary, fixed), map[*coffImport]uintptr{fixed: 999}, paths, borrowed, ops, hooks)
	paths[0] = t.TempDir()
	borrowed["provider.dll"] = 999
	first.name = "mutated"
	if len(ids) != 3 || len(p.slots) != 2 || ids[first] != ids[alias] {
		t.Fatal("canonical aliases/fixed/ordinary imports", ids, len(p.slots))
	}
	for _, entry := range []*coffImport{first, alias, ordinal} {
		result, code, err := p.resolve(ids[entry], 1, coffLazyResult{})
		if err != nil || code != 0 || result.address == 0 {
			t.Fatal(result, code, err)
		}
	}
	if opens != 1 || lookups != 2 || closes != 0 {
		t.Fatal("provider/canonical cache", opens, lookups, closes)
	}
	want := []coffLazyEvent{coffLazyStart, coffLazyBeforeLoad, coffLazyBeforeLookup, coffLazyEnd, coffLazyStart, coffLazyBeforeLookup, coffLazyEnd}
	if !reflect.DeepEqual(events, want) {
		t.Fatal("notification order", events, want)
	}
	if err := p.unload(); err != nil {
		t.Fatal(err)
	}
	result, code, err := p.resolve(ids[first], 1, coffLazyResult{})
	if err != nil || code != 0 || result.module != 102 || opens != 2 || lookups != 3 || closes != 1 {
		t.Fatal("reload after unload", result, code, err, opens, lookups, closes)
	}
	if err := p.unload(); err != nil || closes != 2 {
		t.Fatal("final unload", err, closes)
	}
}

func TestCOFFLazyProvidersHookRecovery(t *testing.T) {
	missing := errors.New("native provider unavailable")
	for _, event := range []coffLazyEvent{coffLazyStart, coffLazyBeforeLoad, coffLazyBeforeLookup, coffLazyLoadFailure, coffLazyLookupFailure} {
		t.Run(fmt.Sprint(event), func(t *testing.T) {
			var opens, lookups, closes int
			ops := coffLazyOperations{
				open: func(string) (uintptr, error) {
					opens++
					if event == coffLazyLoadFailure {
						return 0, missing
					}
					return 100, nil
				},
				lookup: func(uintptr, string, uint16) (uintptr, error) {
					lookups++
					if event == coffLazyLookupFailure {
						return 0, missing
					}
					return 142, nil
				},
				close: func(uintptr) error { closes++; return nil },
			}
			hook := func(got coffLazyEvent, info coffLazyInfo) coffLazyResult {
				if got != event {
					return coffLazyResult{}
				}
				if got == coffLazyLoadFailure || got == coffLazyLookupFailure {
					if !errors.Is(info.err, missing) {
						t.Fatal("failure identity", info.err)
					}
				}
				if got == coffLazyBeforeLoad || got == coffLazyLoadFailure {
					return coffLazyResult{module: 100, owned: true}
				}
				return coffLazyResult{address: 142}
			}
			p, _ := newCOFFLazyProviders(lazyProviderObjects(lazyProviderEntry("provider.dll", "add")), nil, nil, nil, ops, coffLazyHooks{notify: hook, failure: hook})
			result, code, err := p.resolve(0, 7, coffLazyResult{})
			if err != nil || code != 0 || result.address != 142 {
				t.Fatal(result, code, err)
			}
			if err := p.unload(); err != nil {
				t.Fatal(err)
			}
			wantCloses := 1
			if event == coffLazyStart {
				wantCloses = 0
			}
			if closes != wantCloses {
				t.Fatal("hook/native ownership", opens, lookups, closes, wantCloses)
			}
		})
	}
}

func TestCOFFLazyProvidersParallelFirstCall(t *testing.T) {
	const count = 12
	var opened, closed atomic.Int32
	ready := make(chan struct{})
	ops := coffLazyOperations{
		open: func(string) (uintptr, error) {
			if opened.Add(1) == count {
				close(ready)
			}
			<-ready
			return 100, nil // Every result owns a separate reference.
		},
		lookup: func(uintptr, string, uint16) (uintptr, error) { return 142, nil },
		close:  func(uintptr) error { closed.Add(1); return nil },
	}
	p, _ := newCOFFLazyProviders(lazyProviderObjects(lazyProviderEntry("provider.dll", "add")), nil, nil, nil, ops, coffLazyHooks{})
	var group sync.WaitGroup
	for i := 0; i < count; i++ {
		group.Add(1)
		go func(thread uintptr) {
			defer group.Done()
			result, code, err := p.resolve(0, thread, coffLazyResult{})
			if err != nil || code != 0 || result.address != 142 || result.module != 100 {
				t.Error(result, code, err)
			}
		}(uintptr(i + 1))
	}
	group.Wait()
	if opened.Load() != count || closed.Load() != count-1 {
		t.Fatal("concurrent native references", opened.Load(), closed.Load())
	}
	if err := p.unload(); err != nil || closed.Load() != count {
		t.Fatal("canonical native reference", err, closed.Load())
	}
}

func TestCOFFLazyProvidersReentry(t *testing.T) {
	entry := lazyProviderEntry("provider.dll", "add")
	var p *coffLazyProviders
	var ends int
	hook := func(event coffLazyEvent, info coffLazyInfo) coffLazyResult {
		switch event {
		case coffLazyStart:
			result, code, err := p.resolve(0, 7, coffLazyResult{})
			if code != 87 || !errors.Is(err, errCOFFLazyRecursion) || result.address != 0 {
				t.Fatal("same-thread recursion", result, code, err)
			}
		case coffLazyEnd:
			ends++
			result, code, err := p.resolve(0, 7, coffLazyResult{})
			if err != nil || code != 0 || result.address != 142 {
				t.Fatal("end hook cached reentry", result, code, err)
			}
		}
		return coffLazyResult{}
	}
	closes := 0
	ops := coffLazyOperations{
		open:   func(string) (uintptr, error) { return 100, nil },
		lookup: func(uintptr, string, uint16) (uintptr, error) { return 142, nil },
		close: func(uintptr) error {
			closes++
			result, code, err := p.resolve(0, 7, coffLazyResult{})
			if err != nil || code != 0 || result.address != 142 {
				t.Fatal("native detach cached reentry", result, code, err)
			}
			return nil
		},
	}
	p, _ = newCOFFLazyProviders(lazyProviderObjects(entry), nil, nil, nil, ops, coffLazyHooks{notify: hook})
	if result, code, err := p.resolve(0, 7, coffLazyResult{}); code != 0 || err != nil || result.address != 142 {
		t.Fatal(result, code, err)
	}
	if err := p.unload(); err != nil || closes != 1 || ends != 1 {
		t.Fatal("detach reentry", err, closes, ends)
	}
}

func TestCOFFLazyProvidersCleanupRetry(t *testing.T) {
	transient := errors.New("native FreeLibrary failed")
	var closes, opens int
	var fail = true
	var cell uintptr
	ops := coffLazyOperations{
		open:   func(string) (uintptr, error) { opens++; return 100, nil },
		lookup: func(uintptr, string, uint16) (uintptr, error) { return 142, nil },
		close: func(uintptr) error {
			closes++
			if fail {
				return transient
			}
			return nil
		},
	}
	p, _ := newCOFFLazyProviders(lazyProviderObjects(lazyProviderEntry("provider.dll", "add")), nil, nil, nil, ops, coffLazyHooks{})
	p.reset = func(_ int, address uintptr) { cell = address }
	if _, code, err := p.resolve(0, 1, coffLazyResult{}); code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if err := p.unload(); !errors.Is(err, transient) || cell != 142 {
		t.Fatal("failed cleanup preserves target", err, cell)
	}
	if result, code, err := p.resolve(0, 1, coffLazyResult{}); code != 0 || err != nil || result.address != 142 || opens != 1 {
		t.Fatal("failed cleanup retains ownership", result, code, err, opens)
	}
	fail = false
	if err := p.unload(); err != nil || cell != 0 || closes != 2 {
		t.Fatal("successful retry", err, cell, closes)
	}
	if err := p.unload(); err != nil || closes != 2 {
		t.Fatal("successful release repeated", err, closes)
	}
}

func TestCOFFLazyProvidersFailureIdentity(t *testing.T) {
	nativeFailure := errors.New("native lookup failure")
	for _, failure := range []uint32{126, 127, 87} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			ops := coffLazyOperations{
				open: func(string) (uintptr, error) {
					if failure == 126 {
						return 0, nativeFailure
					}
					return 100, nil
				},
				lookup: func(uintptr, string, uint16) (uintptr, error) { return 0, nativeFailure },
				close:  func(uintptr) error { return nil },
			}
			var hooks coffLazyHooks
			if failure == 87 {
				hooks.notify = func(coffLazyEvent, coffLazyInfo) coffLazyResult { panic("hook failed") }
			}
			p, _ := newCOFFLazyProviders(lazyProviderObjects(lazyProviderEntry("provider.dll", "add")), nil, nil, nil, ops, hooks)
			result, code, err := p.resolve(0, 1, coffLazyResult{})
			if result.address != 0 || code != failure || err == nil || failure != 87 && !errors.Is(err, nativeFailure) {
				t.Fatal(result, code, err)
			}
			if err := p.unload(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCOFFLazyProvidersBorrowedModule(t *testing.T) {
	var closes int
	ops := coffLazyOperations{
		open: func(string) (uintptr, error) { t.Fatal("borrowed module reopened"); return 0, nil },
		lookup: func(module uintptr, _ string, _ uint16) (uintptr, error) {
			if module != 100 {
				t.Fatal("borrowed module", module)
			}
			return 142, nil
		},
		close: func(uintptr) error { closes++; return nil },
	}
	p, _ := newCOFFLazyProviders(lazyProviderObjects(lazyProviderEntry("provider.dll", "add")), nil, nil, map[string]uintptr{"provider.dll": 100}, ops, coffLazyHooks{})
	for range 2 {
		if result, code, err := p.resolve(0, 1, coffLazyResult{}); code != 0 || err != nil || result.address != 142 {
			t.Fatal(result, code, err)
		}
		if err := p.unload(); err != nil {
			t.Fatal(err)
		}
	}
	if closes != 0 {
		t.Fatal("borrowed reference released", closes)
	}
}

func TestCOFFLazyProvidersHookBorrowReload(t *testing.T) {
	var loans, closes int
	ops := coffLazyOperations{
		open: func(string) (uintptr, error) { t.Fatal("borrow hook reached loader"); return 0, nil },
		lookup: func(module uintptr, _ string, _ uint16) (uintptr, error) {
			if module != uintptr(100+loans) {
				t.Fatal("stale hook-borrowed handle", module, loans)
			}
			return module + 42, nil
		},
		close: func(uintptr) error { closes++; return nil },
	}
	hooks := coffLazyHooks{notify: func(event coffLazyEvent, _ coffLazyInfo) coffLazyResult {
		if event == coffLazyBeforeLoad {
			loans++
			return coffLazyResult{module: uintptr(100 + loans)}
		}
		return coffLazyResult{}
	}}
	p, _ := newCOFFLazyProviders(lazyProviderObjects(lazyProviderEntry("provider.dll", "add")), nil, nil, nil, ops, hooks)
	for round := 1; round <= 2; round++ {
		result, code, err := p.resolve(0, 1, coffLazyResult{})
		if code != 0 || err != nil || result.module != uintptr(100+round) || loans != round {
			t.Fatal("renewed module loan", result, code, err, loans)
		}
		if err := p.unload(); err != nil {
			t.Fatal(err)
		}
	}
	if closes != 0 {
		t.Fatal("borrowed hook references were released", closes)
	}
}

func TestCOFFLazyProvidersDetachLoadsProvider(t *testing.T) {
	var p *coffLazyProviders
	var closed []uintptr
	opened := 0
	ops := coffLazyOperations{
		open:   func(string) (uintptr, error) { opened++; return uintptr(100 + opened), nil },
		lookup: func(module uintptr, _ string, _ uint16) (uintptr, error) { return module + 42, nil },
		close: func(module uintptr) error {
			closed = append(closed, module)
			if module == 101 {
				// First resolution of another DLL during the first DLL's detach.
				result, code, err := p.resolve(1, 1, coffLazyResult{})
				if err != nil || code != 0 || result.module != 102 {
					t.Fatal("detach first resolution", result, code, err)
				}
			}
			return nil
		},
	}
	p, _ = newCOFFLazyProviders(lazyProviderObjects(lazyProviderEntry("first.dll", "add"), lazyProviderEntry("second.dll", "add")), nil, nil, nil, ops, coffLazyHooks{})
	if _, code, err := p.resolve(0, 1, coffLazyResult{}); code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if err := p.unload(); err != nil || !reflect.DeepEqual(closed, []uintptr{101, 102}) {
		t.Fatal("providers created during detach must also be drained", err, closed)
	}
}

func TestCOFFLazyProvidersDuplicateCleanupRetry(t *testing.T) {
	transient := errors.New("duplicate native reference retained")
	var failure atomic.Bool
	failure.Store(true)
	var opened, closed atomic.Int32
	ready := make(chan struct{})
	ops := coffLazyOperations{
		open: func(string) (uintptr, error) {
			if opened.Add(1) == 2 {
				close(ready)
			}
			<-ready
			return 100, nil
		},
		lookup: func(uintptr, string, uint16) (uintptr, error) { return 142, nil },
		close: func(uintptr) error {
			closed.Add(1)
			if failure.Load() {
				return transient
			}
			return nil
		},
	}
	p, _ := newCOFFLazyProviders(lazyProviderObjects(lazyProviderEntry("provider.dll", "add")), nil, nil, nil, ops, coffLazyHooks{})
	var group sync.WaitGroup
	var successes atomic.Int32
	for thread := uintptr(1); thread <= 2; thread++ {
		group.Add(1)
		go func(thread uintptr) {
			defer group.Done()
			result, code, err := p.resolve(0, thread, coffLazyResult{})
			if err == nil && code == 0 && result.address == 142 {
				successes.Add(1)
			} else {
				t.Error(result, code, err)
			}
		}(thread)
	}
	group.Wait()
	if successes.Load() != 2 || closed.Load() != 1 {
		t.Fatal(successes.Load(), closed.Load())
	}
	failure.Store(false)
	if err := p.unload(); err != nil || closed.Load() != 3 {
		t.Fatal("duplicate and canonical references released separately", err, closed.Load())
	}
	if err := p.unload(); err != nil || closed.Load() != 3 {
		t.Fatal("successful releases repeated", err, closed.Load())
	}
}

func TestCOFFLazyProvidersConsecutiveRecovery(t *testing.T) {
	var opened, closed int
	ops := coffLazyOperations{
		open: func(string) (uintptr, error) { opened++; return 0, errors.New("native module missing") },
		lookup: func(uintptr, string, uint16) (uintptr, error) {
			return 0, errors.New("native procedure missing")
		},
		close: func(uintptr) error { closed++; return nil },
	}
	p, _ := newCOFFLazyProviders(lazyProviderObjects(lazyProviderEntry("provider.dll", "missing")), nil, nil, nil, ops, coffLazyHooks{})
	if result, code, err := p.resolve(0, 1, coffLazyResult{}); code != 126 || err == nil || result.address != 0 {
		t.Fatal("missing module", result, code, err)
	}
	if result, code, err := p.resolve(0, 1, coffLazyResult{module: 100, owned: true}); code != 127 || err == nil || result.module != 100 || result.address != 0 {
		t.Fatal("module recovery followed by missing procedure", result, code, err)
	}
	if result, code, err := p.resolve(0, 1, coffLazyResult{module: 100, address: 142}); code != 0 || err != nil || result.address != 142 {
		t.Fatal("procedure recovery", result, code, err)
	}
	// A concurrent native handler may have acquired another reference before
	// noticing the logical winner. Its identical HMODULE still owns a reference.
	if result, code, err := p.resolve(0, 2, coffLazyResult{module: 100, address: 142, owned: true}); code != 0 || err != nil || result.address != 142 || closed != 1 {
		t.Fatal("competing recovered reference", result, code, err, closed)
	}
	if err := p.unload(); err != nil || closed != 2 || opened != 1 {
		t.Fatal("recovered reference ownership", err, opened, closed)
	}
}

func TestCOFFLazyProvidersInvalidHookOwnership(t *testing.T) {
	var closed int
	ops := coffLazyOperations{
		open: func(string) (uintptr, error) { t.Fatal("invalid hook reached loader"); return 0, nil },
		close: func(module uintptr) error {
			if module != 100 {
				t.Fatal("transferred module", module)
			}
			closed++
			return nil
		},
	}
	hooks := coffLazyHooks{notify: func(event coffLazyEvent, _ coffLazyInfo) coffLazyResult {
		if event == coffLazyBeforeLoad {
			return coffLazyResult{module: 100, address: 142, owned: true} // Address is invalid for this event.
		}
		return coffLazyResult{}
	}}
	p, _ := newCOFFLazyProviders(lazyProviderObjects(lazyProviderEntry("provider.dll", "add")), nil, nil, nil, ops, hooks)
	if result, code, err := p.resolve(0, 1, coffLazyResult{}); code != 87 || err == nil || result.address != 0 {
		t.Fatal("invalid hook result", result, code, err)
	}
	if err := p.unload(); err != nil || closed != 1 {
		t.Fatal("invalid result lost transferred reference", err, closed)
	}
}

func TestCOFFLazyProvidersConflictingTargetModule(t *testing.T) {
	var closed []uintptr
	ops := coffLazyOperations{close: func(module uintptr) error { closed = append(closed, module); return nil }}
	hooks := coffLazyHooks{notify: func(event coffLazyEvent, info coffLazyInfo) coffLazyResult {
		if event != coffLazyStart {
			return coffLazyResult{}
		}
		if info.name == "first" {
			return coffLazyResult{module: 100, address: 142, owned: true}
		}
		return coffLazyResult{module: 200, address: 242, owned: true}
	}}
	p, _ := newCOFFLazyProviders(lazyProviderObjects(lazyProviderEntry("provider.dll", "first"), lazyProviderEntry("provider.dll", "second")), nil, nil, nil, ops, hooks)
	if result, code, err := p.resolve(0, 1, coffLazyResult{}); code != 0 || err != nil || result.address != 142 {
		t.Fatal(result, code, err)
	}
	if result, code, err := p.resolve(1, 1, coffLazyResult{}); code != 87 || err == nil || result.address != 0 {
		t.Fatal("target outlived its competing module", result, code, err)
	}
	if err := p.unload(); err != nil || !reflect.DeepEqual(closed, []uintptr{200, 100}) {
		t.Fatal("competing/canonical reference cleanup", err, closed)
	}
}
