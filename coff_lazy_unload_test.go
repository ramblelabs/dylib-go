package dylib

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCOFFLazyUnloadCallAndLeaseGate(t *testing.T) {
	s := New(Options{})
	symbol := &Symbol{owner: s, address: 142}
	if err := symbol.WithAddress(func(uintptr) error {
		if err := s.UnloadDelayImports(); !errors.Is(err, ErrDelayImportsBusy) {
			t.Fatal("unload during managed use", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lease, err := symbol.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UnloadDelayImports(); !errors.Is(err, ErrDelayImportsBusy) {
		t.Fatal("unload with an idle persistent lease", err)
	}
	lease.Close()
	if err := s.UnloadDelayImports(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := s.UnloadDelayImports(); !errors.Is(err, ErrClosed) {
		t.Fatal("unload reopened a retired session", err)
	}
}

func TestCOFFLazyUnloadExclusiveAndClose(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	defer resume()
	ops := coffLazyOperations{
		open:   func(string) (uintptr, error) { return 100, nil },
		lookup: func(uintptr, string, uint16) (uintptr, error) { return 142, nil },
		close:  func(uintptr) error { close(entered); <-release; return nil },
	}
	providers, _ := newCOFFLazyProviders(lazyProviderObjects(lazyProviderEntry("provider.dll", "add")), nil, nil, nil, ops, coffLazyHooks{})
	if _, code, err := providers.resolve(0, 1, coffLazyResult{}); code != 0 || err != nil {
		t.Fatal(code, err)
	}
	s := New(Options{})
	s.image = &image{lazy: &coffLazyImage{providers: providers}}
	symbol := &Symbol{owner: s, address: 142}
	unloaded := make(chan error, 1)
	go func() { unloaded <- s.UnloadDelayImports() }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("unload did not reach native cleanup")
	}
	if _, err := s.Lookup("add"); !errors.Is(err, ErrDelayImportsBusy) {
		t.Fatal("lookup during unload", err)
	}
	if err := s.Link(); !errors.Is(err, ErrDelayImportsBusy) {
		t.Fatal("link during unload", err)
	}
	if err := s.Define("other", 142); !errors.Is(err, ErrDelayImportsBusy) {
		t.Fatal("mutation during unload", err)
	}
	if err := s.Load("not-opened.o"); !errors.Is(err, ErrDelayImportsBusy) {
		t.Fatal("load during unload", err)
	}
	if err := symbol.WithAddress(func(uintptr) error { t.Fatal("entered native code during unload"); return nil }); !errors.Is(err, ErrDelayImportsBusy) {
		t.Fatal("call during unload", err)
	}
	if _, err := symbol.Acquire(); !errors.Is(err, ErrDelayImportsBusy) {
		t.Fatal("lease during unload", err)
	}
	if err := s.UnloadDelayImports(); !errors.Is(err, ErrDelayImportsBusy) {
		t.Fatal("concurrent unload", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	waitSessionRetired(t, s)
	select {
	case err := <-closed:
		t.Fatal("Close released the image during native unload", err)
	default:
	}
	resume()
	for _, result := range []<-chan error{unloaded, closed} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("unload/retirement did not finish")
		}
	}
	if err := symbol.WithAddress(func(uintptr) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatal("unload cleared concurrent retirement", err)
	}
}
