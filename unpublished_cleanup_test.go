package dylib

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func unpublishedCleanupFixture(t *testing.T) (*Session, *Session, *image, func(int32, int32) (int32, error)) {
	t.Helper()
	needNative(t)
	dir := t.TempDir()
	host, _, read := lifecycleObserver(t, dir)
	provider := nativeInputSession(t, "testdata/add.c", "object")
	add, err := provider.Resolve("add")
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{})
	load(t, s, host, compile(t, "testdata/lifecycle_arrays.c", filepath.Join(dir, "arrays.o")))
	if err := s.DefineSymbol("retained_add", add); err != nil {
		t.Fatal(err)
	}
	objects, err := s.selectObjects([]string{"array_root"})
	if err != nil {
		t.Fatal(err)
	}
	defs, err := definitions(objects)
	if err != nil {
		t.Fatal(err)
	}
	im, err := newImage(objects, defs, s.external, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if im.unwind != nil {
			im.unwind.dynamic = 0
		}
		if im.mem != nil && s.failedImage != im {
			if err := im.close(); err != nil {
				t.Error(err)
			}
		}
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, provider, im, read
}

func TestNativeUnpublishedCleanupRetry(t *testing.T) {
	for _, kind := range []string{"missing_root", "registration", "retirement"} {
		t.Run(kind, func(t *testing.T) {
			s, provider, im, read := unpublishedCleanupFixture(t)
			cause := errors.New("native registration failed")
			if kind == "missing_root" {
				_, cause = im.lookup("absent_root")
			} else if kind == "retirement" {
				cause = ErrClosed
			}
			if cause == nil {
				t.Fatal("expected a primary link failure")
			}
			// This API rejects an absent invalidator on macOS and the platform
			// on Linux/Windows, without touching any actual native registration.
			// Exercise real cleanup failure without replacing production calls.
			im.unwind = &runtimeFunctions{dynamic: 1}
			s.mu.Lock()
			if kind == "retirement" {
				s.closed = true
			}
			err := s.discardUnpublishedImage(im, cause)
			s.mu.Unlock()
			if !errors.Is(err, cause) || err == cause || !strings.Contains(err.Error(), "unpublished image cleanup:") {
				t.Fatal("primary or native cleanup failure lost", err)
			}
			if s.image != nil || s.failedImage != im || s.initErr != err || im.mem == nil || len(s.libs) != 1 || len(s.symbolDependencies) != 1 || s.initializing || s.calls != 0 {
				t.Fatal("failed rollback lost ownership or published an image")
			}
			expectEvents(t, read) // Neither constructors nor finalizers can run.
			for _, operation := range []func() error{
				func() error { return s.Link("array_root") },
				func() error { return s.Define("new_symbol", 1) },
				func() error { return s.Load("unread-input.o") },
			} {
				got := operation()
				if kind == "retirement" {
					if !errors.Is(got, ErrClosed) {
						t.Fatal("retired session reopened", got)
					}
				} else if got != err {
					t.Fatal("new operation bypassed failed rollback", got)
				}
			}
			if err := s.Close(); err == nil || s.failedImage != im || im.mem == nil || len(s.libs) != 1 || len(s.symbolDependencies) != 1 {
				t.Fatal("cleanup retry discarded retained resources", err)
			}
			closed := make(chan error, 1)
			go func() { closed <- provider.Close() }()
			waitSessionRetired(t, provider)
			select {
			case <-closed:
				t.Fatal("provider released before unpublished image")
			default:
			}
			im.unwind.dynamic = 0
			if err := s.Close(); err != nil || s.failedImage != nil || im.mem != nil || len(s.libs) != 0 || len(s.symbolDependencies) != 0 {
				t.Fatal("unpublished cleanup did not finish", err)
			}
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("provider lease retained after successful cleanup")
			}
			if err := s.Close(); err != nil {
				t.Fatal("completed cleanup was not idempotent", err)
			}
			expectEvents(t, read)
		})
	}
}

func TestNativeUnpublishedCleanupAllowsLinkRetry(t *testing.T) {
	s, _, im, read := unpublishedCleanupFixture(t)
	_, cause := im.lookup("absent_root")
	if cause == nil {
		t.Fatal("expected a missing root")
	}
	s.mu.Lock()
	err := s.discardUnpublishedImage(im, cause)
	s.mu.Unlock()
	if err != cause || im.mem != nil || s.image != nil || s.failedImage != nil || s.initErr != nil || len(s.libs) != 1 || len(s.symbolDependencies) != 1 {
		t.Fatal("successful rollback changed retry behavior", err)
	}
	expectEvents(t, read)
	call(t, s, "array_root", 20, 22, 42)
	expectArrayEvents(t, read, false)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	expectArrayEvents(t, read, true)
}

func TestNativeUnpublishedCleanupMemoryRetry(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("POSIX mapper validates the original complete slice")
	}
	s, _, im, read := unpublishedCleanupFixture(t)
	mem := im.mem
	defer func() {
		if im.mem != nil {
			im.mem = mem
		}
	}()
	_, cause := im.lookup("absent_root")
	if cause == nil {
		t.Fatal("expected a missing root")
	}
	// As in the published-image retry test, the mapper rejects an incomplete
	// slice without calling munmap. The native image stays intact for retry.
	im.mem = mem[:len(mem)-1]
	s.mu.Lock()
	err := s.discardUnpublishedImage(im, cause)
	s.mu.Unlock()
	if !errors.Is(err, cause) || err == cause || s.failedImage != im || s.image != nil || im.mem == nil {
		t.Fatal("unpublished memory release lost its owner or error", err)
	}
	if err := s.Close(); err == nil || s.failedImage != im || len(s.libs) != 1 || len(s.symbolDependencies) != 1 {
		t.Fatal("failed memory retry released image dependencies", err)
	}
	im.mem = mem
	if err := s.Close(); err != nil || im.mem != nil || s.failedImage != nil || len(s.libs) != 0 || len(s.symbolDependencies) != 0 {
		t.Fatal("unpublished memory retry did not finish", err)
	}
	expectEvents(t, read)
}
