//go:build libffi && cgo && darwin && (amd64 || arm64)

package abibridge

import (
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/ramblelabs/dylib-go/abi"
)

func init() {
	managedCallbackCases = []managedCallbackCase{
		{"destructor_callback", testManagedDestructorCallback},
		{"cleanup_reentry", testManagedCleanupReentry},
	}
}

func managedCallback(t *testing.T, facade, native string, retire bool) {
	t.Helper()
	s := load(t, native)
	r := open(t, facade)
	count := control(t, s, "fixture_value_count")
	if _, err := count(1, 0); err != nil {
		t.Fatal(err)
	}
	kind, err := r.PrepareValueType(resolve(t, s, "fixture_managed_metadata"))
	if err != nil {
		t.Fatal(err)
	}
	layout := kind.Layout()
	makeCall := prepare(t, r, Signature{Result: Type{Kind: Indirect, Size: uint32(layout.Size), Alignment: uint32(layout.Alignment), ValueType: kind}, Args: []Type{{Kind: Int64}}})
	makeSymbol := resolve(t, s, "fixture_owned_indirect_make")
	other := prepare(t, r, Signature{Result: Type{Kind: Int32}})
	emptyType, err := r.PrepareValueType(resolve(t, s, "fixture_empty_metadata"))
	if err != nil {
		t.Fatal(err)
	}
	emptyInit := prepare(t, r, Signature{Args: []Type{{Kind: Pointer}}})
	emptySymbol := resolve(t, s, "fixture_empty_initialize")
	auxiliary, err := emptyType.New(func(address uintptr) error {
		_, err := emptyInit.Invoke(emptySymbol, words(uint64(address)))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var events atomic.Int32
	callback, err := abi.NewCallback(abi.Signature{Result: abi.Void, Args: []abi.Type{abi.I32}}, func(arguments []abi.Value) (abi.Value, error) {
		if int32(arguments[0].Bits) != 1 {
			return abi.Value{}, fmt.Errorf("destroyed token count = %d", arguments[0].Bits)
		}
		events.Add(1)
		if retire {
			if _, err := r.Prepare(Signature{}); !errors.Is(err, ErrClosed) {
				return abi.Value{}, fmt.Errorf("runtime accepted work during destructor: %v", err)
			}
			// Both resources belong to this retired runtime. Cleanup must not
			// wait for Runtime.Close while it is executing this callback.
			if err := auxiliary.Close(); err != nil {
				return abi.Value{}, err
			}
			if err := other.Close(); err != nil {
				return abi.Value{}, err
			}
		}
		return abi.Value{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer callback.Close()
	lease, err := callback.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	address, err := lease.Address()
	if err != nil {
		t.Fatal(err)
	}
	observer := control(t, s, "fixture_value_observer")
	if _, err := observer(int32(address), int32(uint64(address)>>32)); err != nil {
		t.Fatal(err)
	}
	defer observer(0, 0)
	result, err := makeCall.InvokeManaged(makeSymbol, Argument{Bytes: words(42)})
	if err != nil {
		t.Fatal(err)
	}
	value := result.Value
	copy, err := value.Clone()
	if err != nil {
		t.Fatal(err)
	}
	value.Close()
	if events.Load() != 0 {
		t.Fatal("copy lost its strong native reference")
	}
	runtime.GC()
	if retire {
		done := make(chan error, 1)
		go func() { done <- r.Close() }()
		waitValueClose(t, done)
	} else {
		copy.Close()
	}
	if events.Load() != 1 || callback.Err() != nil {
		t.Fatal("native destroy witness Go reentry", events.Load(), callback.Err())
	}
	got, err := count(0, 0)
	if err != nil || got != 1 {
		t.Fatal(got, err)
	}
}
func testManagedDestructorCallback(t *testing.T, facade, native string) {
	managedCallback(t, facade, native, false)
}
func testManagedCleanupReentry(t *testing.T, facade, native string) {
	managedCallback(t, facade, native, true)
}
