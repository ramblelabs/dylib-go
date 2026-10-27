//go:build cgo

package abi

import (
	"errors"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestNativeValueRoundTrips(t *testing.T) {
	for _, value := range []Value{Int8(-128), Uint8(255), Int16(-32768), Uint16(65535), Int32(-2147483648), Uint32(^uint32(0)), Int64(-9223372036854775808), Uint64(^uint64(0)), Float32(20.5), Float64(22.5), Boolean(true), Boolean(false), Value{Type: F32, Bits: 0x80000000}, Value{Type: F32, Bits: 0x7fc00042}, Value{Type: F64, Bits: 0x8000000000000000}, Value{Type: F64, Bits: 0x7ff8000000000042}, Ptr(0)} {
		owner, err := NewNativeValue(value.Description(), value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := owner.Read()
		if err != nil || !reflect.DeepEqual(got, value) {
			t.Fatal("scalar roundtrip:", got, err, value)
		}
		owner.Close()
	}
	pair := TypeDesc{Type: Struct, Fields: []Field{{Name: "tag", Type: TypeDesc{Type: I8}}, {Name: "value", Type: TypeDesc{Type: F64}}, {Name: "truth", Type: TypeDesc{Type: Bool}}}}
	array := TypeDesc{Type: Array, Len: 2, Elem: &pair}
	description := TypeDesc{Type: Struct, Fields: []Field{{Name: "items", Type: array}, {Name: "pointer", Type: TypeDesc{Type: Pointer}}}}
	initial := Zero(description)
	initial.Aggregate.Fields[0].Aggregate.Fields[0].Aggregate.Fields = []Value{Int8(-128), Float64(20.5), Boolean(true)}
	owner, err := NewNativeValue(description, initial)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	want := initial
	got, err := owner.Read()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("nested record/array roundtrip:", got, err)
	}
	// Constructor and read metadata do not borrow mutable caller descriptions.
	description.Fields[0].Name = "changed"
	description.Fields[0].Type.Elem.Fields[0].Type.Type = U64
	initial.Aggregate.Fields[0].Aggregate.Fields[0].Aggregate.Fields[0] = Int8(7)
	got.Aggregate.Type.Fields[0].Name = "changed"
	snapshot, err := owner.Read()
	if err != nil || snapshot.Aggregate.Type.Fields[0].Name != "items" || snapshot.Aggregate.Fields[0].Aggregate.Fields[0].Aggregate.Fields[0] != Int8(-128) {
		t.Fatal("storage/metadata snapshot:", snapshot, err)
	}
	layout, err := owner.Layout()
	if err != nil {
		t.Fatal(err)
	}
	desc, err := owner.Description()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := LayoutOf(desc, CDecl)
	if err != nil || !reflect.DeepEqual(layout, expected) {
		t.Fatal("native layout:", layout, expected, err)
	}
	layout.Offsets[0], desc.Fields[0].Name = 999, "changed"
	secondLayout, _ := owner.Layout()
	secondDesc, _ := owner.Description()
	if secondLayout.Offsets[0] != 0 || secondDesc.Fields[0].Name != "items" {
		t.Fatal("metadata borrowed from public snapshots")
	}
	lease, err := owner.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	address, err := lease.Address()
	if err != nil || address == 0 || uint64(address)%expected.Alignment != 0 {
		t.Fatal("native address/alignment:", address, err)
	}
	pointer, err := lease.Pointer()
	if err != nil || uintptr(pointer) != address {
		t.Fatal("native pointer:", pointer, err)
	}
	// Self/interior pointers are owned addresses, not call-local temporary copies.
	snapshot.Aggregate.Fields[1] = Ptr(address + uintptr(expected.Offsets[1]))
	if err := lease.Write(snapshot); err != nil {
		t.Fatal(err)
	}
	got, err = lease.Read()
	if err != nil || !reflect.DeepEqual(got, snapshot) {
		t.Fatal("owned interior pointer:", got, err)
	}
	updatedAddress, _ := lease.Address()
	if updatedAddress != address {
		t.Fatal("write changed the leased address")
	}
	bad := Zero(snapshot.Description())
	bad.Aggregate.Fields[1] = AddressOf(&Value{Type: I32})
	if err := lease.Write(bad); err == nil {
		t.Fatal("stored temporary pointer accepted")
	}
	afterError, err := lease.Read()
	if err != nil || !reflect.DeepEqual(afterError, snapshot) {
		t.Fatal("rejected write changed native storage:", afterError, err)
	}
	// Arrays are storage values even though C cannot pass them by value.
	arrayValue := snapshot.Aggregate.Fields[0]
	arrayOwner, err := NewNativeValue(arrayValue.Description(), arrayValue)
	if err != nil {
		t.Fatal(err)
	}
	defer arrayOwner.Close()
	arrayRead, err := arrayOwner.Read()
	if err != nil || !reflect.DeepEqual(arrayRead, arrayValue) {
		t.Fatal("bare native array storage:", arrayRead, err)
	}
	large := TypeDesc{Type: Array, Len: 10000, Elem: &TypeDesc{Type: U64}}
	if owner, err := NewNativeValue(large, Zero(large)); err == nil {
		owner.Close()
		t.Fatal("accepted oversized storage")
	}
}

func TestNativeValueRetirementAndConcurrency(t *testing.T) {
	description := TypeDesc{Type: Struct, Fields: []Field{{Type: TypeDesc{Type: I32}}, {Type: TypeDesc{Type: I32}}}}
	owner, err := NewNativeValue(description, Zero(description))
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 32 {
				value, _ := StructValue(description, Int32(int32(i)), Int32(int32(i)))
				if err := owner.Write(value); err != nil {
					t.Error(err)
					return
				}
				got, err := owner.Read()
				if err != nil || got.Aggregate.Fields[0] != got.Aggregate.Fields[1] {
					t.Error("overlapping storage reads/writes:", got, err)
					return
				}
			}
		}()
	}
	workers.Wait()
	first, _ := owner.Acquire()
	second, _ := owner.Acquire()
	address, _ := first.Address()
	done := make(chan error, 2)
	go func() { done <- owner.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lease, err := owner.Acquire()
		if errors.Is(err, ErrValueClosed) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		lease.Close()
		if time.Now().After(deadline) {
			t.Fatal("owner did not retire")
		}
		runtime.Gosched()
	}
	go func() { done <- owner.Close() }()
	if _, err := owner.Read(); !errors.Is(err, ErrValueClosed) {
		t.Fatal("new read after retirement:", err)
	}
	if _, err := first.Read(); err != nil {
		t.Fatal("existing lease during retirement:", err)
	}
	if err := first.Write(Zero(description)); err != nil {
		t.Fatal("existing write during retirement:", err)
	}
	first.Close()
	select {
	case err := <-done:
		t.Fatal("close freed storage while another lease was active:", err)
	default:
	}
	if got, err := second.Address(); err != nil || got != address {
		t.Fatal("remaining lease changed:", got, err)
	}
	second.Close()
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("close did not complete after lease release")
		}
	}
	if _, err := second.Read(); !errors.Is(err, ErrValueClosed) {
		t.Fatal("released lease read:", err)
	}
	owner.Close()
}

func TestNativeValueAddressCleanup(t *testing.T) {
	owner, err := NewNativeValue(TypeDesc{Type: I32}, Int32(42))
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.WithAddress(nil); err == nil {
		t.Fatal("nil address user accepted")
	}
	func() {
		defer func() {
			if recover() != "expected" {
				t.Error("address adapter panic was not preserved")
			}
		}()
		owner.WithAddress(func(uintptr) error { panic("expected") })
	}()
	done := make(chan error, 1)
	go func() { done <- owner.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("panic left a native value lease active")
	}
}

func TestNativeStorageBoundary(t *testing.T) {
	description := TypeDesc{Type: Array, Len: 65536, Elem: &TypeDesc{Type: U8}}
	initial := Zero(description)
	initial.Aggregate.Fields[0] = Uint8(20)
	initial.Aggregate.Fields[65535] = Uint8(22)
	owner, err := NewNativeValue(description, initial)
	if err != nil {
		t.Fatal("64 KiB boundary", err)
	}
	defer owner.Close()
	layout, err := owner.Layout()
	if err != nil || layout.Size != 65536 || layout.Alignment != 1 || layout.Offsets[65535] != 65535 {
		t.Fatal("64 KiB layout", layout.Size, err)
	}
	got, err := owner.Read()
	if err != nil || !reflect.DeepEqual(got, initial) {
		t.Fatal("64 KiB roundtrip", err)
	}
	// A small field after a 64 KiB array also exceeds the storage limit.
	full := TypeDesc{Type: Array, Len: 32768, Elem: &TypeDesc{Type: U16}}
	oversized := TypeDesc{Type: Struct, Fields: []Field{{Type: full}, {Type: TypeDesc{Type: U8}}}}
	if _, err := LayoutOf(oversized, CDecl); err == nil {
		t.Fatal("accepted 64 KiB plus one byte")
	}
	large := TypeDesc{Type: Array, Len: 10000, Elem: &TypeDesc{Type: U64}}
	if _, err := LayoutOf(TypeDesc{Type: Pointer, Elem: &large}, CDecl); err != nil {
		t.Fatal("pointer unnecessarily laid out its annotated pointee", err)
	}
}
