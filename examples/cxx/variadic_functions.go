package cxxexample

import (
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// VariadicFunctionClasses transports native ellipsis function pointers. Their
// descriptors contain only fixed parameters, unlike concrete Method call shapes.
// Returned entries borrow their provider; no Go variadic callback is created.
func VariadicFunctionClasses() []cxx.Class {
	entry := abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}, Variadic: true, FixedArgs: 1}
	function := func(index int) []cxx.FunctionArg {
		return []cxx.FunctionArg{{Index: index, Signature: entry}}
	}
	return []cxx.Class{{Name: "variadic_functions::Holder", Delete: "varfn_delete",
		Constructors: []cxx.Constructor{{Symbol: "varfn_new", Args: []abi.Type{abi.Pointer}, FunctionArgs: function(0)}},
		Methods: []cxx.Method{
			{Name: "factory", Symbol: "varfn_factory", Static: true, Signature: abi.Signature{Result: abi.Pointer}, FunctionResult: &entry},
			{Name: "get", Symbol: "varfn_get", Const: true, Signature: abi.Signature{Result: abi.Pointer}, FunctionResult: &entry},
			{Symbol: "varfn_convert", Conversion: true, Const: true, Signature: abi.Signature{Result: abi.Pointer}, FunctionResult: &entry},
			{Name: "call", Symbol: "varfn_call", Const: true, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32, abi.I8, abi.F32}}},
			{Name: "invoke", Symbol: "varfn_invoke", Static: true, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.Pointer, abi.I32, abi.I16, abi.F32}}, FunctionArgs: function(0)},
			{Name: "tail", Symbol: "varfn_tail", Static: true, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32, abi.Pointer}, Variadic: true, FixedArgs: 1}, FunctionArgs: function(1)},
			{Name: "live", Symbol: "varfn_live", Static: true, Signature: abi.Signature{Result: abi.I32}},
		},
	}}
}
