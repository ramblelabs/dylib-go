package abi

import (
	"errors"
	"testing"
)

func TestNativeValueValidationAndUnavailable(t *testing.T) {
	cycle := TypeDesc{Type: Pointer}
	cycle.Elem = &cycle
	for _, description := range []TypeDesc{{Type: Void}, {Type: Type(255)}, {Type: Struct}, {Type: Array}, cycle} {
		if owner, err := NewNativeValue(description, Value{Type: description.Type}); err == nil || owner != nil || errors.Is(err, ErrUnavailable) {
			t.Fatalf("invalid descriptor: %v, %v", owner, err)
		}
	}
	for _, initial := range []Value{Uint32(42), AddressOf(&Value{Type: I32}), {Type: I32, Pointee: &Value{Type: I32}}} {
		description := TypeDesc{Type: initial.Type}
		if initial.Type == U32 {
			description.Type = I32
		}
		if owner, err := NewNativeValue(description, initial); err == nil || owner != nil || errors.Is(err, ErrUnavailable) {
			t.Fatal("invalid initial value:", owner, err)
		}
	}
	description := TypeDesc{Type: Struct, Fields: []Field{{Name: "pointer", Type: TypeDesc{Type: Pointer}}}}
	initial, err := StructValue(description, AddressOf(&Value{Type: I32}))
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := NewNativeValue(description, initial); err == nil || owner != nil || errors.Is(err, ErrUnavailable) {
		t.Fatal("nested temporary pointer accepted:", owner, err)
	}
	if !StorageAvailable() {
		if owner, err := NewNativeValue(TypeDesc{Type: I32}, Int32(42)); !errors.Is(err, ErrUnavailable) || owner != nil {
			t.Fatal("unavailable storage:", owner, err)
		}
	}
	var absent *NativeValue
	for _, owner := range []*NativeValue{absent, {}} {
		if _, err := owner.Acquire(); !errors.Is(err, ErrValueClosed) {
			t.Fatal("nil/zero owner acquisition:", err)
		}
		if _, err := owner.Read(); !errors.Is(err, ErrValueClosed) {
			t.Fatal("nil/zero owner read:", err)
		}
		if err := owner.Write(Int32(42)); !errors.Is(err, ErrValueClosed) {
			t.Fatal("nil/zero owner write:", err)
		}
		if _, err := owner.Layout(); !errors.Is(err, ErrValueClosed) {
			t.Fatal("nil/zero owner layout:", err)
		}
		if _, err := owner.Description(); !errors.Is(err, ErrValueClosed) {
			t.Fatal("nil/zero owner description:", err)
		}
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
	}
	var absentLease *NativeValueLease
	for _, lease := range []*NativeValueLease{absentLease, {}} {
		if pointer, err := lease.Pointer(); pointer != nil || !errors.Is(err, ErrValueClosed) {
			t.Fatal("nil/zero lease pointer:", pointer, err)
		}
		if _, err := lease.Address(); !errors.Is(err, ErrValueClosed) {
			t.Fatal("nil/zero lease address:", err)
		}
		if _, err := lease.Read(); !errors.Is(err, ErrValueClosed) {
			t.Fatal("nil/zero lease read:", err)
		}
		if err := lease.Write(Int32(42)); !errors.Is(err, ErrValueClosed) {
			t.Fatal("nil/zero lease write:", err)
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
