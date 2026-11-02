package cxxexample

import (
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// ConversionClasses selects explicit native conversion operators by result type.
// Native spellings do not supply ABI or ownership automatically. Borrowed
// references retain their original owner, while value results need destruction.
func ConversionClasses() []cxx.Class {
	read := abi.Signature{Result: abi.I32}
	entry := abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32, abi.I32}}
	convert := func(symbol string, result abi.Type, constant bool, ref cxx.RefQualifier) cxx.Method {
		return cxx.Method{Symbol: symbol, Conversion: true, Signature: abi.Signature{Result: result}, Const: constant, Ref: ref}
	}
	boolean := convert("convert_bool", abi.Bool, true, cxx.RefNone)
	lvalue := convert("convert_lvalue", abi.I32, true, cxx.RefLValue)
	rvalue := convert("convert_rvalue", abi.I32, false, cxx.RefRValue)
	floating := convert("convert_float", abi.F64, true, cxx.RefLValue)
	floating.Volatile = true
	address := convert("convert_address", abi.Pointer, false, cxx.RefLValue)
	borrowed := convert("convert_reference", abi.Pointer, false, cxx.RefLValue)
	borrowed.ObjectResult = &cxx.ObjectType{Name: "moves::Box", Kind: cxx.ObjectReference}
	moving := convert("convert_rvalue_reference", abi.Pointer, false, cxx.RefRValue)
	moving.ObjectResult = &cxx.ObjectType{Name: "moves::Box", Kind: cxx.ObjectRValueReference}
	owned := convert("convert_owned", abi.Pointer, false, cxx.RefRValue)
	owned.ObjectResult = &cxx.ObjectType{Name: "moves::Box", Kind: cxx.ObjectValue}
	function := convert("convert_entry", abi.Pointer, true, cxx.RefNone)
	function.FunctionResult = &entry
	virtual := convert("convert_virtual", abi.I32, true, cxx.RefLValue)
	return []cxx.Class{
		{Name: "conversions::Converter", Delete: "convert_delete", Constructors: []cxx.Constructor{{Symbol: "convert_new", Args: []abi.Type{abi.I32}}},
			Bases: []cxx.Base{{Name: "conversions::Base", Symbol: "convert_base"}},
			Methods: []cxx.Method{boolean, lvalue, rvalue, floating, address, borrowed, moving, owned, function,
				{Name: "invoke", Symbol: "convert_invoke", Static: true, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.Pointer, abi.I32, abi.I32}}, FunctionArgs: []cxx.FunctionArg{{Index: 0, Signature: entry}}},
				{Name: "read", Symbol: "convert_read", Static: true, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.Pointer}}, ObjectArgs: []cxx.ObjectArg{{Index: 0, Type: cxx.ObjectType{Name: "conversions::Number", Kind: cxx.ObjectPointer, Const: true}}}},
			}},
		{Name: "conversions::Base", Methods: []cxx.Method{virtual}},
		{Name: "moves::Box", Delete: "converted_delete", Methods: []cxx.Method{
			{Name: "read", Symbol: "converted_read", Const: true, Volatile: true, Signature: read},
			{Name: "live", Symbol: "converted_live", Static: true, Signature: read},
		}},
	}
}
