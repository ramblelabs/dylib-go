//go:build cgo && (linux || darwin || windows)

package cbridge_test

import (
	"reflect"
	"testing"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cbridge"
	"github.com/ramblelabs/dylib-go/compiler/clang"
	"github.com/ramblelabs/dylib-go/examples/headerbridge"
)

func TestNativeHeaderFunctionPointers(t *testing.T) {
	compiler := clangTool(t)
	testNativeEntries(t, compiler, compiler)
}

func TestNativeHeaderPackedFunctionPointers(t *testing.T) {
	compiler := clangTool(t)
	testNativeEntries(t, compiler, compiler, "-fpack-struct=1")
}

func testNativeEntries(t *testing.T, parser, compiler string, extra ...string) {
	t.Helper()
	h, f := entryModel(t, parser, "", append(flags(), extra...)...)
	testNativeFacade(t, compiler, h, f, "testdata/entries.c", checkEntryCalls, extra...)
}

func checkEntryCalls(t *testing.T, s *dylib.Session, h *clang.Header, f *cbridge.Facade) {
	t.Helper()
	bindings := map[string]cbridge.Binding{}
	for _, b := range f.Functions {
		bindings[b.Name] = b
	}
	// Returned entries and pointer slots belong to the producer image, not to
	// the facade's temporary storage. Retain it across all subsequent calls.
	symbol, err := s.Resolve(bindings["entry_factory"].NativeSymbol)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := symbol.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	call := func(name string, args ...abi.Value) abi.Value {
		t.Helper()
		value, err := headerbridge.Call(s, bindings[name], args...)
		if err != nil {
			t.Fatal(name, err)
		}
		return value
	}
	if got := call("entry_native_control"); got != abi.Int32(42) {
		t.Fatal("independent native control", got)
	}
	record, err := h.LookupRecord("EntryPair")
	if err != nil {
		t.Fatal(err)
	}
	input := abi.Zero(record.Description)
	input.Aggregate.Fields = []abi.Value{abi.Float32(20), abi.Int32(22)}
	want := abi.Zero(record.Description)
	want.Aggregate.Fields = []abi.Value{abi.Float32(21), abi.Int32(24)}
	entry, slot := call("entry_factory"), call("entry_slot")
	factory := call("entry_higher")
	for _, address := range []abi.Value{entry, slot, factory} {
		if address.Bits == 0 {
			t.Fatal("null producer address", address)
		}
	}
	if got := call("entry_from_factory", factory); got != entry {
		t.Fatal("higher-order factory", got, entry)
	}
	for _, name := range []string{"entry_apply", "entry_indirect"} {
		address := entry
		if name == "entry_indirect" {
			address = slot
		}
		if got := call(name, address, input); !reflect.DeepEqual(got, want) {
			t.Fatal(name, got, want)
		}
		if got := call(name, abi.Ptr(0), input); !reflect.DeepEqual(got, input) {
			t.Fatal("nullable entry/slot", name, got, input)
		}
	}
	if got := call("entry_tail_apply", call("entry_tail_factory"), abi.Int32(2), abi.Int32(20), abi.Float64(20.5)); got != abi.Int32(42) {
		t.Fatal("variadic native entry", got)
	}
}
