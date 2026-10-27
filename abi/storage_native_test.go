//go:build cgo

package abi_test

import (
	"reflect"
	"runtime"
	"strconv"
	"testing"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/abi/internal/storageprobe"
)

func storageDescription() abi.TypeDesc {
	inner := abi.TypeDesc{Type: abi.Struct, Fields: []abi.Field{{Name: "tag", Type: abi.TypeDesc{Type: abi.I8}}, {Name: "value", Type: abi.TypeDesc{Type: abi.F64}}, {Name: "truth", Type: abi.TypeDesc{Type: abi.Bool}}}}
	row := abi.TypeDesc{Type: abi.Array, Len: 3, Elem: &abi.TypeDesc{Type: abi.I16}}
	return abi.TypeDesc{Type: abi.Struct, RecordID: "storage_record", Fields: []abi.Field{
		{Name: "head", Type: abi.TypeDesc{Type: abi.U16}},
		{Name: "items", Type: abi.TypeDesc{Type: abi.Array, Len: 2, Elem: &inner}},
		{Name: "matrix", Type: abi.TypeDesc{Type: abi.Array, Len: 2, Elem: &row}},
		{Name: "pointer", Type: abi.TypeDesc{Type: abi.Pointer}},
		{Name: "tail", Type: abi.TypeDesc{Type: abi.U64}},
	}}
}

func TestNativeStorageCScalars(t *testing.T) {
	values := []abi.Value{abi.Int32(-2147483648), abi.Uint32(^uint32(0)), abi.Int64(-9223372036854775808), abi.Uint64(^uint64(0)), abi.Float32(-20.5), abi.Float64(22.5), abi.Ptr(0), abi.Int8(-128), abi.Uint8(255), abi.Int16(-32768), abi.Uint16(65535), abi.Boolean(true)}
	for _, initial := range values {
		t.Run(strconv.Itoa(int(initial.Type)), func(t *testing.T) {
			owner, err := abi.NewNativeValue(initial.Description(), initial)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			lease, err := owner.Acquire()
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			pointer, err := lease.Pointer()
			if err != nil || !storageprobe.CheckScalar(uint8(initial.Type), pointer) {
				t.Fatal("C scalar dereference", err)
			}
		})
	}
}

func TestNativeStorageCRecord(t *testing.T) {
	description := storageDescription()
	initial := abi.Zero(description)
	fields := initial.Aggregate.Fields
	fields[0] = abi.Uint16(65535)
	fields[1].Aggregate.Fields[0].Aggregate.Fields = []abi.Value{abi.Int8(-128), abi.Float64(20.5), abi.Boolean(true)}
	fields[1].Aggregate.Fields[1].Aggregate.Fields = []abi.Value{abi.Int8(127), abi.Float64(-22.5), abi.Boolean(false)}
	fields[2].Aggregate.Fields[0].Aggregate.Fields = []abi.Value{abi.Int16(-32768), abi.Int16(-1), abi.Int16(0)}
	fields[2].Aggregate.Fields[1].Aggregate.Fields = []abi.Value{abi.Int16(1), abi.Int16(2), abi.Int16(32767)}
	fields[4] = abi.Uint64(0xfedcba9876543210)
	owner, err := abi.NewNativeValue(description, initial)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	layout, err := owner.Layout()
	if err != nil {
		t.Fatal(err)
	}
	size, alignment, offsets := storageprobe.Layout()
	if layout.Size != size || layout.Alignment != alignment || !reflect.DeepEqual(layout.Offsets, offsets) {
		t.Fatalf("C sizeof/alignof/offsetof: %+v; want %d/%d/%v", layout, size, alignment, offsets)
	}
	queried, err := abi.LayoutOf(description, abi.CDecl)
	if err != nil || !reflect.DeepEqual(layout, queried) {
		t.Fatal("storage/layout backend agreement", layout, queried, err)
	}
	lease, err := owner.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	address, _ := lease.Address()
	itemsLayout, err := abi.LayoutOf(description.Fields[1].Type, abi.CDecl)
	if err != nil {
		t.Fatal(err)
	}
	fields[3] = abi.Ptr(address + uintptr(offsets[1]+itemsLayout.Offsets[1]))
	if err := owner.Write(initial); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	pointer, err := lease.Pointer()
	if err != nil || !storageprobe.Check(pointer) || !storageprobe.PaddingZero(pointer) {
		t.Fatal("C nested record dereference/padding", err)
	}
	storageprobe.PoisonPadding(pointer)
	if err := owner.Write(initial); err != nil || !storageprobe.PaddingZero(pointer) || !storageprobe.Check(pointer) {
		t.Fatal("write did not clear padding", err)
	}
	storageprobe.Update(pointer)
	// initial is no longer needed; modify it to describe the C mutation.
	want := initial
	want.Aggregate.Fields[0] = abi.Uint16(42)
	want.Aggregate.Fields[1].Aggregate.Fields[0].Aggregate.Fields[1] = abi.Float64(42.5)
	want.Aggregate.Fields[1].Aggregate.Fields[1].Aggregate.Fields[2] = abi.Boolean(true)
	want.Aggregate.Fields[2].Aggregate.Fields[1].Aggregate.Fields[2] = abi.Int16(-42)
	want.Aggregate.Fields[3], want.Aggregate.Fields[4] = abi.Ptr(0), abi.Uint64(42)
	got, err := owner.Read()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("C mutations were not read back", got, err)
	}
}
