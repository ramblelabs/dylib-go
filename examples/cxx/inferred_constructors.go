package cxxexample

import (
	"context"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// InferSelectedConstructors derives C carriers for native argument expressions.
// Constructor overloads, defaults, conversions and templates remain C++ rules.
// Destruction/read helpers express the native ownership contract explicitly.
// The guarded fixture header permits composing these generated sources.
func InferSelectedConstructors(ctx context.Context, header string, opts cxx.DiscoveryOptions) (*cxx.InferredConstructorFacade, error) {
	var selections []cxx.ConstructorSelection
	add := func(class, symbol string, args ...string) {
		selections = append(selections, cxx.ConstructorSelection{Class: "inferred_constructors::" + class, Symbol: "ctor_" + symbol, ArgumentTypes: args})
	}
	add("Receiver", "zero")
	for _, choice := range []struct{ symbol, native string }{
		{"int", "int"}, {"long", "long"}, {"narrow", "inferred_constructors::Narrow"},
		{"wide", "inferred_constructors::Wide"}, {"code", "inferred_constructors::Code"},
		{"bool", "bool"}, {"float", "float"}, {"double", "double"},
		{"pointer", "inferred_constructors::Pointer"}, {"const_pointer", "inferred_constructors::ConstPointer"},
		{"global_pointer", "Pointer"},
	} {
		add("Receiver", choice.symbol, choice.native)
	}
	add("Receiver", "entry", "inferred_constructors::Entry", "int")
	add("Defaulted", "default_zero")
	add("Defaulted", "default_one", "int")
	add("Defaulted", "default_two", "int", "int")
	add("Deduced", "deduced_int", "int")
	add("Deduced", "deduced_double", "double")
	add("Converted", "converted", "int")
	add("Qualified", "qualified", "Constant")
	add("BoxAlias", "box", "int")
	add("NullNew", "null_new")
	add("ThrowNew", "throw_new")
	var owners []cxx.Class
	for _, class := range []string{"Receiver", "Defaulted", "Deduced", "Converted", "Qualified", "BoxAlias", "NullNew", "ThrowNew"} {
		prefix := "ctor_" + class
		owners = append(owners, cxx.Class{Name: "inferred_constructors::" + class, Delete: prefix + "_delete", Methods: []cxx.Method{{Name: "read", Symbol: prefix + "_read", Const: true, Signature: abi.Signature{Result: abi.I32}}}})
	}
	for _, name := range []string{"pair", "entry", "live", "count"} {
		result := abi.I32
		var signature *abi.Signature
		var object *cxx.ObjectType
		if name == "pair" || name == "entry" {
			result = abi.Pointer
		}
		if name == "entry" {
			signature = &abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}
		}
		if name == "pair" {
			object = &cxx.ObjectType{Name: "inferred_constructors::Pair", Kind: cxx.ObjectPointer}
		}
		symbol := "ctor_" + name
		if name == "entry" {
			symbol = "ctor_native_entry"
		}
		owners[0].Methods = append(owners[0].Methods, cxx.Method{Name: name, Symbol: symbol, Static: true, Signature: abi.Signature{Result: result}, FunctionResult: signature, ObjectResult: object})
	}
	ownerFacade, err := cxx.Generate(header, owners)
	if err != nil {
		return nil, err
	}
	result, err := cxx.InferConstructors(ctx, header, selections, opts)
	if err != nil {
		return nil, err
	}
	result.Facade = &cxx.Facade{CHeader: append(ownerFacade.CHeader, result.Facade.CHeader...), CXXSource: append(ownerFacade.CXXSource, result.Facade.CXXSource...), Functions: append(ownerFacade.Functions, result.Facade.Functions...)}
	return result, nil
}
