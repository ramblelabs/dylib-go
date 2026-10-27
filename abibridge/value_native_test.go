//go:build cgo && darwin && (amd64 || arm64)

package abibridge

import (
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	dylib "github.com/ramblelabs/dylib-go"
	examplecall "github.com/ramblelabs/dylib-go/examples/call"
)

func waitValueState(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("native value did not reach retirement state")
		}
		time.Sleep(time.Millisecond)
	}
}
func waitValueClose(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("native value cleanup did not finish")
	}
}

type managedCallbackCase struct {
	name string
	run  func(*testing.T, string, string)
}

var managedCallbackCases []managedCallbackCase

func testManagedValues(t *testing.T, facade, native string) {
	for _, name := range []string{"ownership", "empty", "failure", "concurrent", "value_lease", "runtime_lease", "provider_lease", "metadata_retirement", "initialization_retirement"} {
		t.Run(name, func(t *testing.T) {
			s := load(t, native)
			r := open(t, facade)
			count := control(t, s, "fixture_value_count")
			if _, err := count(1, 0); err != nil {
				t.Fatal(err)
			}
			metadata := "fixture_managed_metadata"
			which := int32(0)
			if name == "empty" {
				metadata = "fixture_empty_metadata"
				which = 1
			}
			kind, err := r.PrepareValueType(resolve(t, s, metadata))
			if err != nil {
				t.Fatal(err)
			}
			layout := kind.Layout()
			query := control(t, s, "fixture_value_layout")
			for index, actual := range []uint64{layout.Size, layout.Stride, layout.Alignment} {
				want, err := query(which, int32(index))
				if err != nil || actual != uint64(want) {
					t.Fatal("Swift metadata layout", index, actual, want, err)
				}
			}
			initialize := prepare(t, r, Signature{Result: Type{Kind: Void}, Args: []Type{{Kind: Pointer}, {Kind: Int64}}})
			initializer := resolve(t, s, "fixture_managed_initialize")
			read := prepare(t, r, Signature{Result: Type{Kind: Int64}, Args: []Type{{Kind: Pointer}}})
			reader := resolve(t, s, "fixture_managed_read")
			makeValue := func() (*Value, error) {
				return kind.New(func(address uintptr) error {
					if uint64(address)%layout.Alignment != 0 {
						return fmt.Errorf("unaligned Swift storage")
					}
					_, err := initialize.Invoke(initializer, words(uint64(address)), words(42))
					return err
				})
			}
			readAddress := func(address uintptr) error {
				output, err := read.Invoke(reader, words(uint64(address)))
				if err != nil {
					return err
				}
				if got := binary.LittleEndian.Uint64(output); got != 642 {
					return fmt.Errorf("managed value = %d", got)
				}
				return nil
			}
			assertCount := func(want int32) {
				t.Helper()
				got, err := count(0, 0)
				if err != nil || got != want {
					t.Fatal("destroyed values", got, err, want)
				}
			}
			switch name {
			case "ownership":
				value, err := makeValue()
				if err != nil {
					t.Fatal(err)
				}
				copy, err := value.Clone()
				if err != nil {
					t.Fatal(err)
				}
				if err := value.Close(); err != nil {
					t.Fatal(err)
				}
				assertCount(0)
				if err := copy.WithAddress(func(address uintptr) error {
					return copy.WithAddress(func(nested uintptr) error {
						if nested != address {
							return fmt.Errorf("reentrant address changed")
						}
						return readAddress(nested)
					})
				}); err != nil {
					t.Fatal(err)
				}
				copy.Close()
				copy.Close()
				assertCount(1)
				if _, err := copy.Clone(); !errors.Is(err, ErrClosed) {
					t.Fatal("closed clone", err)
				}
				if err := copy.WithAddress(readAddress); !errors.Is(err, ErrClosed) {
					t.Fatal("closed address", err)
				}
				if _, err := copy.Acquire(); !errors.Is(err, ErrClosed) {
					t.Fatal("closed lease", err)
				}
			case "empty":
				if layout.Size != 0 || layout.Stride == 0 {
					t.Fatal(layout)
				}
				init := prepare(t, r, Signature{Args: []Type{{Kind: Pointer}}})
				fn := resolve(t, s, "fixture_empty_initialize")
				value, err := kind.New(func(address uintptr) error { _, err := init.Invoke(fn, words(uint64(address))); return err })
				if err != nil {
					t.Fatal(err)
				}
				copy, err := value.Clone()
				if err != nil {
					t.Fatal(err)
				}
				value.Close()
				copy.Close()
				assertCount(0)
			case "failure":
				failure := errors.New("native initialization declined")
				if value, err := kind.New(func(uintptr) error { return failure }); value != nil || !errors.Is(err, failure) {
					t.Fatal(value, err)
				}
				if _, err := kind.New(nil); err == nil {
					t.Fatal("nil initializer accepted")
				}
				for _, symbol := range []string{"fixture_null_metadata", "fixture_bad_stride", "fixture_bad_extent", "fixture_bad_budget", "fixture_bad_alignment", "fixture_bad_multiple"} {
					if value, err := r.PrepareValueType(resolve(t, s, symbol)); value != nil || err == nil {
						t.Fatal(symbol, value, err)
					}
				}
				assertCount(0)
			case "concurrent":
				value, err := makeValue()
				if err != nil {
					t.Fatal(err)
				}
				var group sync.WaitGroup
				failures := make(chan error, 8)
				for worker := 0; worker < 8; worker++ {
					group.Add(1)
					go func() {
						defer group.Done()
						for i := 0; i < 20; i++ {
							copy, err := value.Clone()
							if err != nil {
								failures <- err
								return
							}
							err = copy.WithAddress(readAddress)
							copy.Close()
							if err != nil {
								failures <- err
								return
							}
						}
					}()
				}
				group.Wait()
				close(failures)
				for err := range failures {
					t.Fatal(err)
				}
				value.Close()
				assertCount(1)
			case "value_lease":
				value, err := makeValue()
				if err != nil {
					t.Fatal(err)
				}
				lease, err := value.Acquire()
				if err != nil {
					t.Fatal(err)
				}
				defer lease.Close()
				pointer, pointerErr := lease.Pointer()
				address, addressErr := lease.Address()
				if pointerErr != nil || addressErr != nil || pointer == nil || uintptr(pointer) != address {
					t.Fatal("leased native pointer", pointer, address, pointerErr, addressErr)
				}
				done := make(chan error, 1)
				go func() { done <- value.Close() }()
				waitValueState(t, func() bool { value.mu.Lock(); defer value.mu.Unlock(); return value.closing })
				if _, err := value.Acquire(); !errors.Is(err, ErrClosed) {
					t.Fatal("retired acquisition", err)
				}
				if err := lease.WithAddress(readAddress); err != nil {
					t.Fatal(err)
				}
				assertCount(0)
				entered, release := make(chan struct{}), make(chan struct{})
				adapterDone := make(chan error, 1)
				go func() {
					adapterDone <- lease.WithAddress(func(address uintptr) error {
						close(entered)
						<-release
						return readAddress(address)
					})
				}()
				select {
				case <-entered:
				case <-time.After(10 * time.Second):
					t.Fatal("leased adapter did not enter")
				}
				leaseDone := make(chan error, 1)
				go func() { leaseDone <- lease.Close() }()
				waitValueState(t, func() bool { lease.mu.Lock(); defer lease.mu.Unlock(); return lease.closing })
				if _, err := lease.Pointer(); !errors.Is(err, ErrClosed) {
					t.Fatal("retired pointer accepted", err)
				}
				assertCount(0)
				close(release)
				waitValueClose(t, adapterDone)
				waitValueClose(t, leaseDone)
				waitValueClose(t, done)
				assertCount(1)
			case "runtime_lease":
				value, err := makeValue()
				if err != nil {
					t.Fatal(err)
				}
				lease, err := value.Acquire()
				if err != nil {
					t.Fatal(err)
				}
				defer lease.Close()
				other := open(t, facade)
				otherRead := prepare(t, other, Signature{Result: Type{Kind: Int64}, Args: []Type{{Kind: Pointer}}})
				done := make(chan error, 1)
				go func() { done <- r.Close() }()
				waitValueState(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.closed })
				if _, err := value.Clone(); !errors.Is(err, ErrClosed) {
					t.Fatal("runtime retirement clone", err)
				}
				if err := lease.WithAddress(func(address uintptr) error {
					bytes, err := otherRead.Invoke(reader, words(uint64(address)))
					if err == nil && binary.LittleEndian.Uint64(bytes) != 642 {
						return fmt.Errorf("retired value changed")
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
				assertCount(0)
				lease.Close()
				waitValueClose(t, done)
				assertCount(1)
				if _, err := kind.New(func(uintptr) error { return nil }); !errors.Is(err, ErrClosed) {
					t.Fatal(err)
				}
			case "provider_lease":
				value, err := makeValue()
				if err != nil {
					t.Fatal(err)
				}
				counter, err := resolve(t, s, "fixture_value_count").Acquire()
				if err != nil {
					t.Fatal(err)
				}
				defer counter.Close()
				done := make(chan error, 1)
				go func() { done <- s.Close() }()
				// Retirement rejects fresh symbol use while metadata's lease persists.
				waitValueState(t, func() bool { _, err := s.Resolve("fixture_managed_read"); return errors.Is(err, dylib.ErrClosed) })
				copy, err := value.Clone()
				if err != nil {
					t.Fatal(err)
				}
				value.Close()
				copy.Close()
				err = counter.WithAddress(func(address uintptr) error {
					got, err := examplecall.Int32(address, 0, 0)
					if err == nil && got != 1 {
						return fmt.Errorf("destroyed count = %d", got)
					}
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				counter.Close()
				r.Close()
				waitValueClose(t, done)
			case "metadata_retirement":
				gate := control(t, s, "fixture_gate")
				if _, err := gate(2, 0); err != nil {
					t.Fatal(err)
				}
				defer gate(1, 0)
				provider := resolve(t, s, "fixture_gated_metadata")
				prepared := make(chan error, 1)
				go func() {
					kind, err := r.PrepareValueType(provider)
					if kind != nil {
						prepared <- fmt.Errorf("published metadata after retirement")
						return
					}
					prepared <- err
				}()
				waitValueState(t, func() bool {
					ready, err := gate(0, 0)
					if err != nil {
						t.Fatal(err)
					}
					return ready == 1
				})
				done := make(chan error, 1)
				go func() { done <- r.Close() }()
				waitValueState(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.closed })
				if _, err := r.PrepareValueType(provider); !errors.Is(err, ErrClosed) {
					t.Fatal("retired metadata acquisition", err)
				}
				if _, err := gate(1, 0); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-prepared:
					if !errors.Is(err, ErrClosed) {
						t.Fatal(err)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("metadata provider did not release its operation")
				}
				waitValueClose(t, done)
				assertCount(0)
			case "initialization_retirement":
				ready, release := make(chan struct{}), make(chan struct{})
				created := make(chan error, 1)
				go func() {
					value, err := kind.New(func(address uintptr) error {
						_, err := initialize.Invoke(initializer, words(uint64(address)), words(42))
						if err != nil {
							return err
						}
						close(ready)
						<-release
						return nil
					})
					if value != nil {
						created <- fmt.Errorf("published after retirement")
						return
					}
					created <- err
				}()
				select {
				case <-ready:
				case err := <-created:
					t.Fatal("initializer failed before publication gate", err)
				case <-time.After(10 * time.Second):
					t.Fatal("initializer did not reach publication gate")
				}
				done := make(chan error, 1)
				go func() { done <- r.Close() }()
				waitValueState(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.closed })
				close(release)
				if err := <-created; !errors.Is(err, ErrClosed) {
					t.Fatal(err)
				}
				waitValueClose(t, done)
				assertCount(1)
			}
		})
	}
	for _, test := range managedCallbackCases {
		t.Run(test.name, func(t *testing.T) { test.run(t, facade, native) })
	}

}

func TestNativeSwiftFacadeVersions(t *testing.T) {
	for _, version := range []int{0, 1, 2, 3, 4} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "facade.dylib")
			run(t, "xcrun", "clang", "-dynamiclib", fmt.Sprintf("-DVERSION=%d", version), "testdata/facade_version.c", "-o", path)
			r, err := Open(path)
			if version == 1 {
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				if _, err := r.PrepareValueType(nil); !errors.Is(err, ErrValueUnsupported) {
					t.Fatal(err)
				}
			} else if err == nil {
				r.Close()
				t.Fatal("incompatible or incomplete facade accepted", version)
			}
		})
	}
	for _, version := range []int{2, 3} {
		t.Run(fmt.Sprintf("%d_value_entries", version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "facade.dylib")
			run(t, "xcrun", "clang", "-dynamiclib", fmt.Sprintf("-DVERSION=%d", version), "-DCOMPLETE_VALUE_ENTRIES", "testdata/facade_version.c", "-o", path)
			r, err := Open(path)
			if version == 3 {
				if err == nil {
					r.Close()
					t.Fatal("missing managed invocation entry accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			kind := &ValueType{owner: r, layout: ValueLayout{Size: 8, Stride: 8, Alignment: 8}}
			if _, err := r.Prepare(Signature{Result: Type{Kind: Indirect, Size: 8, Alignment: 8, ValueType: kind}}); !errors.Is(err, ErrManagedUnsupported) {
				t.Fatal(err)
			}
			call := &Call{owner: r, plan: 1}
			if _, err := call.InvokeManaged(nil); !errors.Is(err, ErrManagedUnsupported) {
				t.Fatal(err)
			}
		})
	}
}
