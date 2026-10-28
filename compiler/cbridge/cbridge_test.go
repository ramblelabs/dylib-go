package cbridge

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"unsafe"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/clang"
)

func scalarHeader() *clang.Header {
	return &clang.Header{Target: clang.Target{Triple: "x86_64-unknown-linux-gnu", OS: "linux", Arch: "amd64", PointerSize: 8}, Functions: []clang.Declaration{{Name: "add", Symbol: "add", Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32, abi.I32}}}}}
}

func TestGenerateContracts(t *testing.T) {
	h := scalarHeader()
	requests := []Function{{Name: "add", Symbol: "bridge_add"}}
	f, err := Generate("native.h", h, requests)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Functions) != 1 || f.Functions[0].NativeSymbol != "add" || f.Functions[0].Symbol != "bridge_add" {
		t.Fatal("binding metadata", f.Functions)
	}
	if bytes.Contains(f.CHeader, []byte("native.h")) || !bytes.Contains(f.CSource, []byte("__typeof__(&add)")) {
		t.Fatal("original header escaped the native compiler boundary")
	}
	fresh, err := Generate("native.h", h, requests)
	if err != nil || !bytes.Equal(f.CSource, fresh.CSource) {
		t.Fatal("nondeterministic generation", err)
	}
	input := f.Functions[0].Input()
	input.Fields[0].Type.Type = abi.U64
	signature := f.Functions[0].Signature()
	signature.Args[0] = abi.U64
	if f.Functions[0].Input().Fields[0].Type.Type != abi.I32 || f.Functions[0].Signature().Args[0] != abi.I32 {
		t.Fatal("mutable public metadata borrowed")
	}
	h.Functions[0].Signature.Args[0] = abi.U64
	if f.Functions[0].Signature().Args[0] != abi.I32 {
		t.Fatal("borrowed declaration metadata")
	}
	other, err := Generate("native.h", scalarHeader(), []Function{{Name: "add", Symbol: "other_add"}})
	if err != nil {
		t.Fatal(err)
	}
	if f.Functions[0].layouts[0].name == other.Functions[0].layouts[0].name {
		t.Fatal("facade type namespaces collide")
	}
	for _, path := range []string{"", "bad\".h", "bad\\.h", "bad\n.h", "bad\x00.h"} {
		if _, err := Generate(path, scalarHeader(), requests); err == nil {
			t.Fatal("invalid header accepted", path)
		}
	}
	for _, functions := range [][]Function{nil, {{Name: "missing", Symbol: "bridge"}}, {{Name: "add", Symbol: "add"}}, {{Name: "add", Symbol: "bad-name"}}, {{Name: "add", Symbol: "bridge"}, {Name: "add", Symbol: "bridge"}}} {
		if _, err := Generate("native.h", scalarHeader(), functions); err == nil {
			t.Fatal("invalid functions accepted", functions)
		}
	}
	if _, err := Generate("native.h", nil, requests); err == nil {
		t.Fatal("nil declarations accepted")
	}
}

func TestGenerateLimitsAndInvalidDescriptions(t *testing.T) {
	for _, modify := range []func(*clang.Header){
		func(h *clang.Header) {
			h.Functions[0].Signature.Args = make([]abi.Type, 65)
			for i := range h.Functions[0].Signature.Args {
				h.Functions[0].Signature.Args[i] = abi.I32
			}
		},
		func(h *clang.Header) { h.Functions[0].Signature.Args = []abi.Type{abi.Void} },
		func(h *clang.Header) { h.Functions[0].Signature.Result = abi.Struct },
		func(h *clang.Header) {
			d := abi.TypeDesc{Type: abi.Pointer}
			d.Elem = &d
			h.Functions[0].Signature.Args = []abi.Type{abi.Pointer}
			h.Functions[0].Signature.ArgTypes = []abi.TypeDesc{d}
		},
	} {
		h := scalarHeader()
		modify(h)
		if _, err := Generate("native.h", h, []Function{{Name: "add", Symbol: "bridge"}}); err == nil {
			t.Fatal("invalid description accepted")
		}
	}
	requests := make([]Function, 257)
	if _, err := Generate("native.h", scalarHeader(), requests); err == nil {
		t.Fatal("export limit ignored")
	}
	if _, err := Generate(strings.Repeat("x", 4097), scalarHeader(), []Function{{Name: "add", Symbol: "bridge"}}); err == nil {
		t.Fatal("header limit ignored")
	}
	h := scalarHeader()
	h.Functions[0].Signature.Args = []abi.Type{abi.Pointer}
	h.Functions[0].FunctionPointers = []clang.FunctionPointer{{Position: 0, Signature: abi.Signature{Result: abi.I32}}}
	if _, err := Generate("native.h", h, []Function{{Name: "add", Symbol: "bridge"}}); err == nil || !strings.Contains(err.Error(), "function-pointer") {
		t.Fatal("function-pointer boundary needs a separate adapter", err)
	}
}

func TestBindingValidationBeforeNativeUse(t *testing.T) {
	f, err := Generate("native.h", scalarHeader(), []Function{{Name: "add", Symbol: "bridge"}})
	if err != nil {
		t.Fatal(err)
	}
	invoked := false
	invoke := func(uintptr, unsafe.Pointer, unsafe.Pointer) (int32, error) { invoked = true; return 0, nil }
	for _, arguments := range [][]abi.Value{nil, {abi.Int32(1)}, {abi.Int32(1), abi.Uint32(2)}} {
		if _, err := f.Functions[0].Call(invoke, 1, arguments...); err == nil || invoked {
			t.Fatal("invalid arguments reached native code", err)
		}
	}
	if _, err := f.Functions[0].Call(invoke, 0, abi.Int32(1), abi.Int32(2)); err == nil || invoked {
		t.Fatal("null target accepted")
	}
	if err := (Binding{}).CheckLayout(invoke); err == nil || invoked {
		t.Fatal("zero binding accepted")
	}
	if err := f.Functions[0].CheckLayout(nil); err == nil {
		t.Fatal("nil invoker accepted")
	}
	// A foreign target must fail before native allocation or invocation, including
	// builds without cgo. Its target is deliberately forged for this guard.
	b := f.Functions[0]
	b.target = clang.Target{}
	if err := b.CheckLayout(invoke); err == nil || errors.Is(err, abi.ErrUnavailable) || invoked {
		t.Fatal("foreign target guard", err)
	}
	h := scalarHeader()
	h.Functions[0].Signature.Args = []abi.Type{abi.Pointer}
	h.Functions[0].Signature.ArgTypes = []abi.TypeDesc{{Type: abi.Pointer, Elem: &abi.TypeDesc{Type: abi.I32}}}
	f, err = Generate("native.h", h, []Function{{Name: "add", Symbol: "bridge"}})
	if err != nil {
		t.Fatal(err)
	}
	value := abi.Int32(42)
	if _, err := f.Functions[0].Call(invoke, 1, abi.AddressOf(&value)); err == nil || !strings.Contains(err.Error(), "temporary") || invoked {
		t.Fatal("temporary pointer reached the layout query", err)
	}
}
