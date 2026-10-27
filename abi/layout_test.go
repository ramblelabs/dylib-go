package abi

import (
	"errors"
	"testing"
)

func TestLayoutValidationAndUnavailable(t *testing.T) {
	cycle := TypeDesc{Type: Pointer}
	cycle.Elem = &cycle
	for _, desc := range []TypeDesc{
		{Type: Void}, {Type: Type(255)}, {Type: Struct},
		{Type: I32, Fields: []Field{{Type: TypeDesc{Type: I8}}}},
		{Type: Array, Len: 0, Elem: &TypeDesc{Type: I32}}, cycle,
	} {
		if layout, err := LayoutOf(desc, Default); err == nil || layout.Size != 0 || layout.Offsets != nil || errors.Is(err, ErrUnavailable) {
			t.Fatalf("invalid description: %+v, %v", layout, err)
		}
	}
	if _, err := LayoutOf(TypeDesc{Type: I32}, Convention(255)); err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatal("invalid convention:", err)
	}
	if !StorageAvailable() {
		if layout, err := LayoutOf(TypeDesc{Type: I32}, CDecl); !errors.Is(err, ErrUnavailable) || layout.Size != 0 || layout.Offsets != nil {
			t.Fatalf("unavailable layout: %+v, %v", layout, err)
		}
	}
}
