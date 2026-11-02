package cxxexample

import (
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// VariadicClasses selects concrete native ellipsis calls. Each exported C entry
// has a fixed signature, including the tail before its optional output pointer.
// The native compiler promotes small integers and float32 in that tail.
func VariadicClasses() []cxx.Class {
	entry := abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32, abi.I32}}
	token := cxx.ObjectType{Name: "variadics::Token", Kind: cxx.ObjectPointer, Const: true}
	method := func(name, symbol string, result abi.Type, args ...abi.Type) cxx.Method {
		return cxx.Method{Name: name, Symbol: symbol, Signature: abi.Signature{Result: result, Args: args, Variadic: true, FixedArgs: 1}}
	}
	sum := func(symbol string, tail ...abi.Type) cxx.Method {
		m := method("sum", symbol, abi.I32, append([]abi.Type{abi.I32}, tail...)...)
		m.Const, m.Ref = true, cxx.RefLValue
		return m
	}
	empty := sum("var_empty")
	promoted := sum("var_promoted", abi.I8, abi.U8, abi.I16, abi.U16, abi.Bool)
	spill := sum("var_spill", abi.I8, abi.I8, abi.I8, abi.I8, abi.I8, abi.I8, abi.I8, abi.I8, abi.I8, abi.I8, abi.I8, abi.I8)
	floats := method("floats", "var_floats", abi.F64, abi.I32, abi.F32, abi.F32, abi.F32, abi.F32, abi.F32, abi.F32, abi.F32, abi.F32, abi.F32, abi.F32, abi.F32, abi.F32)
	floats.Static = true
	mixed := method("mixed", "var_mixed", abi.F64, abi.I32, abi.U32, abi.I64, abi.U64, abi.F32, abi.F64)
	mixed.Const, mixed.Volatile, mixed.Ref = true, true, cxx.RefLValue
	call := method("operator()", "var_call", abi.I32, abi.I32, abi.I16)
	call.Ref = cxx.RefRValue
	prefix := method("prefix", "var_prefix", abi.I32, abi.Pointer, abi.I32, abi.I8)
	prefix.Static, prefix.Signature.FixedArgs = true, 2
	prefix.ObjectArgs = []cxx.ObjectArg{{Index: 0, Type: cxx.ObjectType{Name: token.Name, Kind: cxx.ObjectReference, Const: true}}}
	pointers := method("pointers", "var_pointers", abi.I32, abi.I32, abi.Pointer, abi.Pointer)
	pointers.Static = true
	pointers.ObjectArgs = []cxx.ObjectArg{{Index: 1, Type: token}}
	pointers.FunctionArgs = []cxx.FunctionArg{{Index: 2, Signature: entry}}
	apply := method("apply", "var_apply", abi.I32, abi.Pointer, abi.I32, abi.I8, abi.I16)
	apply.Static, apply.Signature.FixedArgs = true, 2
	apply.FunctionArgs = []cxx.FunctionArg{{Index: 0, Signature: entry}}
	set := method("set", "var_set", abi.Void, abi.I32, abi.I8)
	address := method("address", "var_address", abi.Pointer, abi.I32, abi.U8)
	address.Const, address.ObjectResult = true, &token
	virtual := sum("var_virtual", abi.I16, abi.U16)
	return []cxx.Class{
		{Name: "variadics::Counter", Delete: "var_delete", Constructors: []cxx.Constructor{{Symbol: "var_new", Args: []abi.Type{abi.I32}}},
			Bases: []cxx.Base{{Name: "variadics::Base", Symbol: "var_base"}},
			Methods: []cxx.Method{empty, promoted, spill, floats, mixed, call, prefix, pointers, apply, set, address,
				{Name: "entry", Symbol: "var_entry", Static: true, Signature: abi.Signature{Result: abi.Pointer}, FunctionResult: &entry},
				{Name: "read", Symbol: "var_read", Static: true, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.Pointer}}, ObjectArgs: []cxx.ObjectArg{{Index: 0, Type: token}}},
				{Name: "live", Symbol: "var_live", Static: true, Signature: abi.Signature{Result: abi.I32}},
			}},
		{Name: "variadics::Base", Methods: []cxx.Method{virtual}},
	}
}
