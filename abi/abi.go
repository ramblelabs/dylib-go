// Package abi describes C ABI signatures independently of object-file
// formats. Host C layout queries and owned storage require cgo. Dynamic calls
// and C callbacks additionally require -tags libffi. For statically known
// signatures llgo can call a typed C function pointer directly.
package abi

import (
	"errors"
	"fmt"
	"math"
)

type Type uint8

const (
	Void Type = iota
	I32
	U32
	I64
	U64
	F32
	F64
	Pointer
	I8
	U8
	I16
	U16
	Bool
	Struct
	Array // Fixed-length C array, only within aggregates or behind a pointer.
)

// Convention selects a native C calling convention, not a language object ABI.
type Convention uint8

const (
	Default Convention = iota
	CDecl
	StdCall    // Windows 386 only.
	FastCall   // Windows 386 only.
	VectorCall // Fixed Windows amd64/386 calls through compiled C bridges only.
	ThisCall   // Fixed Windows 386 calls; Args[0] is the explicit receiver pointer.
	Win64      // Fixed Microsoft x64 calls on amd64.
	SysV64     // Fixed System V AMD64 calls; Windows requires compiled C bridges.
)

// Signature describes one concrete call shape, including a variadic tail when
// Variadic is true. Args includes both the fixed prefix and this call's tail.
// It does not describe C++ object semantics or Swift ABI calls.
type Signature struct {
	Result     Type
	Args       []Type
	Convention Convention
	Variadic   bool
	FixedArgs  int
	// ArgTypes and ResultType describe aggregates and optional pointee types.
	// Scalar-only signatures may omit them.
	ArgTypes   []TypeDesc
	ResultType *TypeDesc
}

func (s Signature) Validate() error {
	if s.Convention > SysV64 {
		return fmt.Errorf("invalid calling convention %d", s.Convention)
	}
	if s.Convention == ThisCall && (len(s.Args) == 0 || s.Args[0] != Pointer) {
		return fmt.Errorf("thiscall requires an explicit pointer receiver as the first argument")
	}
	if s.Variadic {
		if s.FixedArgs < 1 || s.FixedArgs > len(s.Args) {
			return fmt.Errorf("variadic calls require 1 to len(Args) fixed arguments")
		}
		if s.Convention != Default && s.Convention != CDecl {
			return fmt.Errorf("variadic calls require the default C or cdecl convention")
		}
	} else if s.FixedArgs != 0 {
		return fmt.Errorf("FixedArgs requires a variadic signature")
	}
	if s.Result == Array {
		return fmt.Errorf("C arrays cannot be returned by value; use a struct or pointer")
	}
	if s.Result > Array {
		return fmt.Errorf("invalid result type %d", s.Result)
	}
	// libffi represents argument counts with an unsigned 32-bit integer.
	if uint64(len(s.Args)) > math.MaxUint32 {
		return fmt.Errorf("argument count exceeds the native ABI representation")
	}
	if len(s.ArgTypes) != 0 && len(s.ArgTypes) != len(s.Args) {
		return fmt.Errorf("argument descriptor count mismatch")
	}
	for i, t := range s.Args {
		if t == Array {
			return fmt.Errorf("argument %d: C array parameters decay to pointers; use a pointer descriptor", i+1)
		}
		if t == Void || t > Array {
			return fmt.Errorf("invalid argument type %d", t)
		}
		d := s.ArgumentType(i)
		if d.Type != t {
			return fmt.Errorf("argument %d descriptor type mismatch", i+1)
		}
		if err := d.Validate(); err != nil {
			return fmt.Errorf("argument %d: %w", i+1, err)
		}
	}
	d := s.ReturnType()
	if d.Type != s.Result {
		return fmt.Errorf("result descriptor type mismatch")
	}
	return d.Validate()
}

// Value carries scalar bits, logical aggregate members, or a temporary pointee.
// Numeric pointers refer to caller-managed native storage. Go objects are
// described field by field, never pinned or passed as raw Go memory.
type Value struct {
	Type      Type
	Bits      uint64
	Aggregate *Aggregate
	// Pointee is copied to native memory for the call and copied back afterward.
	// That memory must not escape the native call; Bits must be zero here.
	Pointee *Value
}

func Int8(v int8) Value       { return Value{Type: I8, Bits: uint64(uint8(v))} }
func Uint8(v uint8) Value     { return Value{Type: U8, Bits: uint64(v)} }
func Int16(v int16) Value     { return Value{Type: I16, Bits: uint64(uint16(v))} }
func Uint16(v uint16) Value   { return Value{Type: U16, Bits: uint64(v)} }
func Int32(v int32) Value     { return Value{Type: I32, Bits: uint64(uint32(v))} }
func Uint32(v uint32) Value   { return Value{Type: U32, Bits: uint64(v)} }
func Int64(v int64) Value     { return Value{Type: I64, Bits: uint64(v)} }
func Uint64(v uint64) Value   { return Value{Type: U64, Bits: v} }
func Float32(v float32) Value { return Value{Type: F32, Bits: uint64(math.Float32bits(v))} }
func Float64(v float64) Value { return Value{Type: F64, Bits: math.Float64bits(v)} }
func Ptr(v uintptr) Value     { return Value{Type: Pointer, Bits: uint64(v)} }
func Boolean(v bool) Value {
	if v {
		return Value{Type: Bool, Bits: 1}
	}
	return Value{Type: Bool}
}

var ErrUnavailable = errors.New("native ABI operation unavailable: C storage requires cgo; dynamic calls and callbacks also require -tags libffi")

// Call invokes a native function with a caller-supplied exact signature. It
// neither infers types from names nor adapts between operating-system ABIs.
// Prefer the owning Session.Bind API when using a dynamically loaded image.
func Call(address uintptr, s Signature, args ...Value) (Value, error) {
	if address == 0 {
		return Value{}, fmt.Errorf("null function address")
	}
	p, err := Prepare(s)
	if err != nil {
		return Value{}, err
	}
	defer p.Close()
	return p.Call(address, args...)
}
