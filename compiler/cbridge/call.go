package cbridge

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/ramblelabs/dylib-go/abi"
)

var ErrLayout = errors.New("cbridge: incompatible C carrier layout")
var ErrStatus = errors.New("cbridge: native facade rejected call")

// Invoker calls one resolved facade export, retaining its code for the call.
// Its exact C signature is int32_t(void *target, void *output, const void *input).
// A zero target requests layout metadata; this is used by CheckLayout.
// All pointers are native memory valid only until this synchronous call returns.
type Invoker func(target uintptr, output, input unsafe.Pointer) (int32, error)

// CheckLayout queries the actual facade compiler's carrier sizes, alignments,
// offsets and array strides, including nested types. It compares them with owned
// ABI storage before allowing Call to publish input/output pointers. Requires cgo
// but not libffi. Keep the facade image alive through invoke.
func (b Binding) CheckLayout(invoke Invoker) error {
	if invoke == nil || b.words < 2 || len(b.layouts) == 0 {
		return fmt.Errorf("cbridge: invalid binding or invoker")
	}
	if err := b.target.CheckHost(); err != nil {
		return err
	}
	metadata := abi.TypeDesc{Type: abi.Array, Len: b.words, Elem: &abi.TypeDesc{Type: abi.U64}}
	return withValues([]abi.TypeDesc{metadata, {Type: abi.U64}}, []abi.Value{abi.Zero(metadata), abi.Uint64(uint64(b.words))}, func(owners []*abi.NativeValue, pointers []unsafe.Pointer) error {
		status, err := invoke(0, pointers[0], pointers[1])
		if err != nil {
			return err
		}
		if status != 0 {
			return fmt.Errorf("%w: metadata status %d", ErrLayout, status)
		}
		value, err := owners[0].Read()
		if err != nil {
			return err
		}
		words := value.Aggregate.Fields
		if words[0].Bits != layoutMagic || words[1].Bits != uint64(b.words) {
			return fmt.Errorf("%w: metadata header", ErrLayout)
		}
		i := 2
		for _, t := range b.layouts {
			layout, err := abi.LayoutOf(t.desc, abi.CDecl)
			if err != nil {
				return err
			}
			if words[i].Bits != layout.Size || words[i+1].Bits != layout.Alignment {
				return fmt.Errorf("%w: %s size/alignment", ErrLayout, t.name)
			}
			i += 2
			if t.desc.Type == abi.Struct {
				for _, offset := range layout.Offsets {
					if words[i].Bits != offset {
						return fmt.Errorf("%w: %s field offset", ErrLayout, t.name)
					}
					i++
				}
			}
			if t.desc.Type == abi.Array {
				stride := words[i].Bits
				i++
				if stride == 0 || stride > layout.Size || stride*uint64(t.desc.Len) != layout.Size {
					return fmt.Errorf("%w: %s array stride", ErrLayout, t.name)
				}
				for j, offset := range layout.Offsets {
					if offset != uint64(j)*stride {
						return fmt.Errorf("%w: %s array offset", ErrLayout, t.name)
					}
				}
			}
		}
		return nil
	})
}

// Call validates logical arguments and the actual C carrier layout, then invokes
// target through the facade. The caller must retain target's code and any native
// addresses for the invocation; invoke retains the separate facade image.
// Arguments and results are copied by field; temporary AddressOf pointees are
// rejected. Pointer parameters/pointer fields remain opaque borrowed addresses,
// never pointers to a Go record or implicit conversions of attributed pointees.
func (b Binding) Call(invoke Invoker, target uintptr, arguments ...abi.Value) (abi.Value, error) {
	if invoke == nil || b.words < 2 || target == 0 || len(arguments) != len(b.signature.Args) {
		return abi.Value{}, fmt.Errorf("cbridge: invalid binding, target, invoker or argument count")
	}
	for i, argument := range arguments {
		if err := argument.Validate(b.signature.ArgumentType(i)); err != nil {
			return abi.Value{}, err
		}
	}
	initial := abi.Zero(b.input)
	if len(arguments) != 0 {
		initial.Aggregate.Fields = append([]abi.Value(nil), arguments...)
	}
	// Validate before calling even the layout entry; no temporary pointer may
	// escape into a carrier retained during an unrelated native invocation.
	for i, argument := range initial.Aggregate.Fields {
		if err := argument.Validate(b.input.Fields[i].Type); err != nil {
			return abi.Value{}, err
		}
		if temporary(argument) {
			return abi.Value{}, fmt.Errorf("cbridge: temporary pointees are unsupported")
		}
	}
	if err := b.CheckLayout(invoke); err != nil {
		return abi.Value{}, err
	}
	var result abi.Value
	err := withValues([]abi.TypeDesc{b.input, b.output}, []abi.Value{initial, abi.Zero(b.output)}, func(owners []*abi.NativeValue, pointers []unsafe.Pointer) error {
		status, err := invoke(target, pointers[1], pointers[0])
		if err != nil {
			return err
		}
		if status != 0 {
			return fmt.Errorf("%w: status %d", ErrStatus, status)
		}
		value, err := owners[1].Read()
		if err != nil {
			return err
		}
		result = value.Aggregate.Fields[0]
		if b.signature.Result == abi.Void {
			result = abi.Value{Type: abi.Void}
		} else {
			restore(&result, b.signature.ReturnType())
		}
		return result.Validate(b.signature.ReturnType())
	})
	return result, err
}

func temporary(value abi.Value) bool {
	if value.Pointee != nil {
		return true
	}
	if value.Aggregate != nil {
		for _, child := range value.Aggregate.Fields {
			if temporary(child) {
				return true
			}
		}
	}
	return false
}

func restore(value *abi.Value, d abi.TypeDesc) {
	if value.Aggregate == nil {
		return
	}
	value.Aggregate.Type = d.Clone()
	for i := range value.Aggregate.Fields {
		var child abi.TypeDesc
		if d.Type == abi.Struct {
			child = d.Fields[i].Type
		} else {
			child = *d.Elem
		}
		restore(&value.Aggregate.Fields[i], child)
	}
}

func withValues(types []abi.TypeDesc, values []abi.Value, use func([]*abi.NativeValue, []unsafe.Pointer) error) error {
	var owners []*abi.NativeValue
	var leases []*abi.NativeValueLease
	var pointers []unsafe.Pointer
	defer func() {
		for i := len(leases) - 1; i >= 0; i-- {
			leases[i].Close()
		}
		for i := len(owners) - 1; i >= 0; i-- {
			owners[i].Close()
		}
	}()
	for i, description := range types {
		owner, err := abi.NewNativeValue(description, values[i])
		if err != nil {
			return err
		}
		owners = append(owners, owner)
		lease, err := owner.Acquire()
		if err != nil {
			return err
		}
		leases = append(leases, lease)
		pointer, err := lease.Pointer()
		if err != nil {
			return err
		}
		pointers = append(pointers, pointer)
	}
	return use(owners, pointers)
}
