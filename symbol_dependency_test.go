package dylib

import (
	"errors"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	examplecall "github.com/ramblelabs/dylib-go/examples/call"
)

func TestSymbolDependencyValidationAndRollback(t *testing.T) {
	provider := New(Options{})
	defer provider.Close()
	symbol := &Symbol{owner: provider, address: 0x1234}
	consumer := new(Session) // Retained definitions preserve the usable zero value.
	defer consumer.Close()
	for _, invalid := range []*Symbol{nil, {}} {
		if err := consumer.DefineSymbol("invalid", invalid); !errors.Is(err, ErrClosed) {
			t.Fatal("unbound provider:", err)
		}
	}
	if err := (*Session)(nil).DefineSymbol("invalid", symbol); !errors.Is(err, ErrClosed) {
		t.Fatal("nil consumer:", err)
	}
	if err := provider.DefineSymbol("self", symbol); err == nil || provider.calls != 0 {
		t.Fatal("self dependency was retained")
	}
	for _, name := range []string{"", "bad\x00name", "atexit", "__imp___cxa_atexit"} {
		if err := consumer.DefineSymbol(name, symbol); err == nil || provider.calls != 0 || len(consumer.symbolDependencies) != 0 {
			t.Fatalf("invalid definition leaked a lease: %q, %v", name, err)
		}
	}
	if err := consumer.DefineSymbol("null", &Symbol{owner: provider}); err == nil || provider.calls != 0 {
		t.Fatal("null address leaked a lease")
	}
	if err := consumer.DefineSymbol("first", symbol); err != nil {
		t.Fatal(err)
	}
	if err := consumer.DefineSymbol("second", symbol); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 2 || len(consumer.symbolDependencies) != 2 || consumer.defined["first"] != 0x1234 || consumer.retainedDefinitions["first"] != 0x1234 || consumer.retainedDefinitions["second"] != 0x1234 || len(consumer.retainedDefinitions) != 2 {
		t.Fatal("retained aliases changed the address or omitted ownership")
	}
	if err := consumer.DefineSymbol("first", symbol); err == nil || provider.calls != 2 || len(consumer.symbolDependencies) != 2 || len(consumer.retainedDefinitions) != 2 {
		t.Fatal("duplicate definition leaked a lease")
	}
	consumer.Close()
	consumer.Close()
	if provider.calls != 0 || len(consumer.symbolDependencies) != 0 {
		t.Fatal("closing an unlinked consumer retained provider leases")
	}
	if err := consumer.DefineSymbol("after_close", symbol); !errors.Is(err, ErrClosed) || provider.calls != 0 {
		t.Fatal("closed consumer leaked a lease:", err)
	}
	failed := New(Options{})
	failed.initErr = ErrInitialization
	if err := failed.DefineSymbol("failed", symbol); !errors.Is(err, ErrInitialization) || provider.calls != 0 {
		t.Fatal("failed consumer leaked a lease:", err)
	}
	failed.Close()
}

func TestSymbolDependencyRetirementAndConcurrentClose(t *testing.T) {
	provider, consumer := New(Options{}), New(Options{})
	defer provider.Close()
	defer consumer.Close()
	symbol := &Symbol{owner: provider, address: 1}
	if err := consumer.DefineSymbol("imported", symbol); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- provider.Close() }()
	waitSessionRetired(t, provider)
	if err := consumer.DefineSymbol("late", symbol); !errors.Is(err, ErrClosed) {
		t.Fatal("retiring provider accepted a new dependency:", err)
	}
	select {
	case <-closed:
		t.Fatal("provider freed code still owned by its consumer")
	default:
	}
	var closers sync.WaitGroup
	for i := 0; i < 4; i++ {
		closers.Add(1)
		go func() { defer closers.Done(); consumer.Close() }()
	}
	closers.Wait()
	if err := <-closed; err != nil || provider.calls != 0 {
		t.Fatal("provider reference was not released once:", err)
	}
}

func dependencyRead(t *testing.T, provider *Session) (*SymbolLease, func(int32, int32) (int32, error)) {
	t.Helper()
	symbol, err := provider.Resolve("recorded_event")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := symbol.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lease.Close() })
	return lease, func(a, b int32) (value int32, err error) {
		err = lease.WithAddress(func(address uintptr) error {
			value, err = examplecall.Int32(address, a, b)
			return err
		})
		return
	}
}

func TestNativeSymbolDependencyCallsAndFinalizers(t *testing.T) {
	for _, input := range []string{"object", "archive", "library"} {
		t.Run(input, func(t *testing.T) {
			provider := nativeInputSession(t, "testdata/lifecycle_host.c", input)
			observer, read := dependencyRead(t, provider)
			symbol, err := provider.Resolve("record_event")
			if err != nil {
				t.Fatal(err)
			}
			consumer := nativeInputSession(t, "testdata/lifecycle_arrays.c", "archive")
			if err := consumer.DefineSymbol("record_event", symbol); err != nil {
				t.Fatal(err)
			}
			call(t, consumer, "array_root", 20, 22, 42)
			entry, err := consumer.Resolve("array_root")
			if err != nil {
				t.Fatal(err)
			}
			if err := provider.DefineSymbol("reverse", entry); !errors.Is(err, ErrLinked) {
				t.Fatal("published provider accepted an ownership cycle")
			}
			want := []int32{3, 4}
			if runtime.GOOS == "linux" {
				want = []int32{0, 3, 4}
			}
			expectEvents(t, read, want...)
			closed := make(chan error, 1)
			go func() { closed <- provider.Close() }()
			waitSessionRetired(t, provider)
			// Calls and finalizers use a retained provider even after its retirement.
			call(t, consumer, "array_root", 20, 22, 42)
			if err := consumer.Close(); err != nil {
				t.Fatal(err)
			}
			expectEvents(t, read, append(want, 5, 6)...)
			provider.mu.Lock()
			uses := provider.calls
			provider.mu.Unlock()
			if uses != 1 {
				t.Fatalf("consumer did not release only its dependency: %d uses", uses)
			}
			observer.Close()
			if err := <-closed; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeSymbolDependencyFunctionsAndData(t *testing.T) {
	for _, input := range []string{"object", "archive", "library"} {
		t.Run(input, func(t *testing.T) {
			provider := nativeInputSession(t, "examples/dependencies/testdata/provider.c", input)
			consumer := nativeInputSession(t, "examples/dependencies/testdata/consumer.c", "object")
			for _, name := range []string{"dependency_add", "dependency_bias"} {
				symbol, err := provider.Resolve(name)
				if err != nil {
					t.Fatal(err)
				}
				if err := consumer.DefineSymbol(name, symbol); err != nil {
					t.Fatal(err)
				}
			}
			call(t, consumer, "imported_add", 20, 20, 42)
			symbol, err := consumer.Resolve("imported_add")
			if err != nil {
				t.Fatal(err)
			}
			lease, err := symbol.Acquire()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { lease.Close() })
			providersClosed, consumersClosed := make(chan error, 1), make(chan error, 1)
			go func() { providersClosed <- provider.Close() }()
			waitSessionRetired(t, provider)
			go func() { consumersClosed <- consumer.Close() }()
			waitSessionRetired(t, consumer)
			// A consumer lease transitively retains provider functions and data.
			if err := lease.WithAddress(func(address uintptr) error {
				got, err := examplecall.Int32(address, 20, 20)
				if err == nil && got != 42 {
					t.Errorf("retained imported function/data result: %d", got)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-providersClosed:
				t.Fatal("provider closed before the consumer's active lease")
			default:
			}
			lease.Close()
			if err := <-consumersClosed; err != nil {
				t.Fatal(err)
			}
			if err := <-providersClosed; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeSymbolDependencyFailedInitializationCleanup(t *testing.T) {
	provider := nativeInputSession(t, "testdata/lifecycle_host.c", "object")
	observer, read := dependencyRead(t, provider)
	symbol, err := provider.Resolve("record_event")
	if err != nil {
		t.Fatal(err)
	}
	consumer := New(Options{})
	defer consumer.Close()
	if err := consumer.DefineSymbol("record_event", symbol); err != nil {
		t.Fatal(err)
	}
	path := compile(t, "testdata/lifecycle_cinit.c", filepath.Join(t.TempDir(), "failed.o"), "-DFAIL_CODE=7")
	loadCInitializerFixture(t, consumer, path)
	if err := consumer.Link("cinit_root"); !errors.Is(err, ErrInitialization) {
		t.Fatal("native initialization result:", err)
	}
	expectEvents(t, read, 1, 2)
	closed := make(chan error, 1)
	go func() { closed <- provider.Close() }()
	waitSessionRetired(t, provider)
	if err := consumer.Close(); err != nil {
		t.Fatal(err)
	}
	expectEvents(t, read, 1, 2, 7, 8)
	observer.Close()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}
