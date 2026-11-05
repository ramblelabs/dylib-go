package cxxexample

import (
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// FreeFunctions selects exact overloads and one concrete native variadic call.
// DiscoverFunctions supplies native declaration metadata; ABI and ownership
// remain explicit choices made by the application.
func FreeFunctions() []cxx.Function {
	entry := abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32, abi.I32}}
	token := cxx.ObjectType{Name: "free_catalog::Token", Kind: cxx.ObjectReference}
	constant := token
	constant.Const = true
	function := func(name, symbol string, result abi.Type, args ...abi.Type) cxx.Function {
		return cxx.Function{Name: name, Symbol: symbol, Signature: abi.Signature{Result: result, Args: args}}
	}
	sum := function("free_catalog::sum", "free_sum", abi.I32, abi.I32, abi.I8, abi.F32)
	sum.Signature.Variadic = true
	sum.Signature.FixedArgs = 1
	factory := function("free_catalog::factory", "free_factory", abi.Pointer)
	factory.FunctionResult = &entry
	invoke := function("free_catalog::invoke", "free_invoke", abi.I32, abi.Pointer, abi.I32, abi.I32)
	invoke.FunctionArgs = []cxx.FunctionArg{{Index: 0, Signature: entry}}
	borrowed := function("free_catalog::borrowed", "free_borrowed", abi.Pointer)
	borrowed.ObjectResult = &token
	read := function("free_catalog::read", "free_read", abi.I32, abi.Pointer, abi.I32)
	read.ObjectArgs = []cxx.ObjectArg{{Index: 0, Type: constant}}
	assign := function("free_catalog::assign", "free_assign", abi.Void, abi.Pointer, abi.I32)
	assign.ObjectArgs = []cxx.ObjectArg{{Index: 0, Type: cxx.ObjectType{Name: token.Name, Kind: cxx.ObjectPointer}}}
	return []cxx.Function{
		function("free_global", "free_global_add", abi.I32, abi.I32, abi.I32),
		function("free_alias::add", "free_add", abi.I32, abi.I32, abi.I32),
		function("free_catalog::add", "free_floating", abi.F64, abi.F64, abi.F64),
		function("free_catalog::internal", "free_internal", abi.I32, abi.I32),
		function("free_catalog::c_entry", "free_c", abi.I32, abi.I32),
		sum, factory, invoke, borrowed, read, assign,
	}
}
