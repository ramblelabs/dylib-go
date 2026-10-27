package abibridge

import (
	"context"
	"errors"
	"testing"

	builder "github.com/ramblelabs/dylib-go/compiler/abibridge"
)

func TestSwiftStorageDescriptions(t *testing.T) {
	valid := Signature{Result: Type{Kind: Storage, Size: 16, Alignment: 8, Components: []Component{{Int64, 0}, {Float64, 8}}}, Args: []Type{{Kind: Indirect, Size: 8, Alignment: 4}, {Kind: Pointer}}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := valid.encode()
	if err != nil {
		t.Fatal(err)
	}
	valid.Result.Components[0].Kind = Uint8
	valid.Args[0].Size = 100
	if snapshot.Result.Components[0].Kind != Int64 || snapshot.Args[0].Size != 8 {
		t.Fatal("signature shares mutable caller metadata")
	}
	for _, kind := range []Kind{Void, Uint8, Int8, Uint16, Int16, Uint32, Int32, Uint64, Int64, Float32, Float64, Pointer} {
		if err := (Signature{Result: Type{Kind: kind}}).Validate(); err != nil {
			t.Fatal(kind, err)
		}
	}
	for name, description := range map[string]Type{
		"unknown":             {Kind: Kind(99)},
		"scalar_size":         {Kind: Int32, Size: 8},
		"scalar_alignment":    {Kind: Float64, Alignment: 4},
		"scalar_components":   {Kind: Int32, Components: []Component{{Int32, 0}}},
		"empty":               {Kind: Storage, Alignment: 1},
		"too_large":           {Kind: Indirect, Size: maxStorage + 1, Alignment: 8},
		"invalid_alignment":   {Kind: Indirect, Size: 8, Alignment: 3},
		"large_alignment":     {Kind: Indirect, Size: 8, Alignment: 8192},
		"indirect_components": {Kind: Indirect, Size: 8, Alignment: 8, Components: []Component{{Int64, 0}}},
		"missing_components":  {Kind: Storage, Size: 8, Alignment: 8},
		"void_component":      {Kind: Storage, Size: 8, Alignment: 8, Components: []Component{{Void, 0}}},
		"nested_component":    {Kind: Storage, Size: 8, Alignment: 8, Components: []Component{{Storage, 0}}},
		"wrong_offset":        {Kind: Storage, Size: 16, Alignment: 8, Components: []Component{{Int32, 0}, {Float64, 4}}},
		"overflow":            {Kind: Storage, Size: 8, Alignment: 8, Components: []Component{{Int64, 0}, {Int64, 8}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := (Signature{Result: description}).Validate(); err == nil {
				t.Fatal("invalid Swift storage accepted")
			}
		})
	}
	if err := (Signature{Args: []Type{{Kind: Void}}}).Validate(); err == nil {
		t.Fatal("void parameter accepted")
	}
	if err := (Signature{Args: make([]Type, maxArguments+1)}).Validate(); err == nil {
		t.Fatal("argument budget ignored")
	}
	args := make([]Type, 17)
	for i := range args {
		args[i] = Type{Kind: Indirect, Size: maxStorage, Alignment: 8}
	}
	if err := (Signature{Args: args}).Validate(); err == nil {
		t.Fatal("storage budget ignored")
	}
}

func TestSwiftClosedAndUnavailable(t *testing.T) {
	var r *Runtime
	var c *Call
	if _, err := r.Prepare(Signature{}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err := c.Invoke(nil); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err := c.InvokeManaged(nil); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.PrepareValueType(nil); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	var kind *ValueType
	var value *Value
	var lease *ValueLease
	if _, err := kind.New(nil); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err := value.Clone(); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := value.WithAddress(nil); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err := value.Acquire(); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err := lease.Pointer(); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err := lease.Address(); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := lease.WithAddress(nil); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := value.Close(); err != nil {
		t.Fatal(err)
	}
	if nativeAvailable() {
		if _, err := Open(t.TempDir() + "/missing.dylib"); err == nil {
			t.Fatal("missing native facade accepted")
		}
	} else if _, err := Open("missing"); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := builder.Build(context.Background(), builder.Options{}); err == nil {
		t.Fatal("missing sources accepted")
	}
}

func TestSwiftManagedStorageDescriptions(t *testing.T) {
	kind := &ValueType{layout: ValueLayout{Size: 16, Stride: 16, Alignment: 8}}
	shape := Type{Kind: Storage, Size: 16, Alignment: 8, Components: []Component{{Uint64, 0}, {Uint64, 8}}, ValueType: kind}
	if err := (Signature{Result: shape, Args: []Type{shape}}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, description := range []Type{
		{Kind: Storage, Size: 8, Alignment: 8, Components: []Component{{Uint64, 0}}, ValueType: kind},
		{Kind: Indirect, Size: 16, Alignment: 4, ValueType: kind},
		{Kind: Pointer, ValueType: kind},
		{Kind: Void, ValueType: &ValueType{layout: ValueLayout{Alignment: 1}}},
	} {
		if err := (Signature{Result: description}).Validate(); err == nil {
			t.Fatal("managed metadata mismatch accepted")
		}
	}
	empty := &ValueType{layout: ValueLayout{Stride: 1, Alignment: 1}}
	if err := (Signature{Result: Type{Kind: Indirect, Alignment: 1, ValueType: empty}}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Signature{Result: Type{Kind: Indirect, Alignment: 1}}).Validate(); err == nil {
		t.Fatal("empty trivial storage accepted")
	}
}
