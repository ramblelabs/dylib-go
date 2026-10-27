// Package abibridge calls explicitly described, synchronous thin Swift functions
// through an optional ABIBridge native facade. It does not infer Swift types or
// ownership from C layouts, symbol names, or Go values.
package abibridge

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var ErrUnsupported = errors.New("abibridge: native calls require macOS amd64/arm64 and cgo or llgo")
var ErrClosed = errors.New("abibridge: closed")

// Kind identifies a Swift ABI scalar component or explicit storage description.
// Integers have fixed widths; Pointer is a native address, never a Go pointer.
type Kind uint32

const (
	Void Kind = iota
	Uint8
	Int8
	Uint16
	Int16
	Uint32
	Int32
	Uint64
	Int64
	Float32
	Float64
	Pointer
	Storage
	Indirect
)

// Component is a scalar at a compiler-confirmed offset in Swift storage.
type Component struct {
	Kind   Kind
	Offset uint32
}

// Type describes Swift storage, not a C calling convention. Scalars need only
// Kind. Storage requires compiler-confirmed size, alignment, and components;
// components must also have representable ordinary C aggregate offsets.
// Indirect describes a formally indirect, trivial Swift value of explicit size
// and alignment. ValueType selects owned Swift storage instead of representation
// bytes. Its metadata layout must match this compiler-confirmed passing shape.
type Type struct {
	Kind       Kind
	Size       uint32
	Alignment  uint32
	Components []Component
	ValueType  *ValueType
}

// Signature describes fixed, guaranteed parameters and a result.
// Inout/consumed parameters, generics, contexts, errors and async suspension
// require a different native adapter. Void is valid only as the result.
type Signature struct {
	Result Type
	Args   []Type
}

const maxStorage = 1 << 20
const maxArguments = 256
const maxCallStorage = 16 << 20

func scalarLayout(kind Kind) (uint32, uint32, bool) {
	switch kind {
	case Void:
		return 0, 1, true
	case Uint8, Int8:
		return 1, 1, true
	case Uint16, Int16:
		return 2, 2, true
	case Uint32, Int32, Float32:
		return 4, 4, true
	case Uint64, Int64, Float64, Pointer:
		return 8, 8, true
	}
	return 0, 0, false
}

func normalize(t Type, result bool) (Type, error) {
	if size, alignment, scalar := scalarLayout(t.Kind); scalar {
		if !result && t.Kind == Void || len(t.Components) != 0 || t.Size != 0 && t.Size != size || t.Alignment != 0 && t.Alignment != alignment {
			return Type{}, fmt.Errorf("abibridge: invalid scalar storage")
		}
		t.Size, t.Alignment = size, alignment
		return t, nil
	}
	if (t.Kind != Storage && t.Kind != Indirect) || t.Size == 0 && (t.Kind != Indirect || t.ValueType == nil) || t.Size > maxStorage || t.Alignment == 0 || t.Alignment > 4096 || t.Alignment&(t.Alignment-1) != 0 {
		return Type{}, fmt.Errorf("abibridge: invalid Swift storage")
	}
	if t.Kind == Indirect {
		if len(t.Components) != 0 {
			return Type{}, fmt.Errorf("abibridge: indirect storage has no scalar components")
		}
		return t, nil
	}
	if len(t.Components) == 0 || len(t.Components) > 64 {
		return Type{}, fmt.Errorf("abibridge: Swift storage requires 1..64 scalar components")
	}
	var extent uint32
	for _, field := range t.Components {
		size, alignment, valid := scalarLayout(field.Kind)
		if !valid || size == 0 {
			return Type{}, fmt.Errorf("abibridge: invalid Swift scalar component")
		}
		offset := (extent + alignment - 1) &^ (alignment - 1)
		if field.Offset != offset || size > t.Size || offset > t.Size-size {
			return Type{}, fmt.Errorf("abibridge: component offset does not match its supported storage layout")
		}
		extent = offset + size
	}
	t.Components = append([]Component(nil), t.Components...)
	return t, nil
}

// Validate checks bounded storage descriptions; it cannot verify a function's
// actual Swift ABI or its declared parameter ownership.
func (s Signature) Validate() error {
	_, _, err := s.encode()
	return err
}

func (s Signature) encode() (Signature, []byte, error) {
	if len(s.Args) > maxArguments {
		return Signature{}, nil, fmt.Errorf("abibridge: too many arguments")
	}
	var err error
	s.Result, err = normalize(s.Result, true)
	if err != nil {
		return Signature{}, nil, err
	}
	s.Args = append([]Type(nil), s.Args...)
	for i := range s.Args {
		s.Args[i], err = normalize(s.Args[i], false)
		if err != nil {
			return Signature{}, nil, fmt.Errorf("argument %d: %w", i, err)
		}
	}
	words := []uint32{1, uint32(len(s.Args))}
	total := uint64(s.Result.Size)
	for _, t := range append([]Type{s.Result}, s.Args...) {
		if t.ValueType != nil && (t.Kind == Void || uint64(t.Size) != t.ValueType.layout.Size || uint64(t.Alignment) != t.ValueType.layout.Alignment) {
			return Signature{}, nil, fmt.Errorf("abibridge: Swift value metadata does not match its passing storage")
		}
		words = append(words, uint32(t.Kind), t.Size, t.Alignment, uint32(len(t.Components)))
		for _, field := range t.Components {
			words = append(words, uint32(field.Kind), field.Offset)
		}
	}
	for _, t := range s.Args {
		total += uint64(t.Size)
	}
	if total > maxCallStorage {
		return Signature{}, nil, fmt.Errorf("abibridge: call storage budget exceeded")
	}
	data := make([]byte, 4*len(words))
	for i, word := range words {
		binary.LittleEndian.PutUint32(data[4*i:], word)
	}
	return s, data, nil
}

func (s Signature) managed() bool {
	if s.Result.ValueType != nil {
		return true
	}
	for _, t := range s.Args {
		if t.ValueType != nil {
			return true
		}
	}
	return false
}
