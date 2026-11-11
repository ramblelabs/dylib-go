package cxxexample

import (
	"context"
	"fmt"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// InferSelectedFunctionOverloads selects native C++ parameter types. Header
// aliases name the narrow/enum/object-pointer/function-pointer parameters.
func InferSelectedFunctionOverloads(ctx context.Context, header string, opts cxx.DiscoveryOptions) (*cxx.InferredFacade, error) {
	var selections []cxx.FunctionSelection
	add := func(name, symbol string, parameters []string) {
		selections = append(selections, cxx.FunctionSelection{
			Name:     "inferred_overloads::" + name,
			Symbol:   symbol,
			Overload: &cxx.FunctionOverload{ParameterTypes: parameters},
		})
	}
	for _, selection := range []struct {
		symbol     string
		nativeType string
	}{
		{"integer", "int"}, {"long", "long"}, {"double", "double"},
		{"narrow", "inferred_overloads::Narrow"}, {"wide", "inferred_overloads::Wide"},
		{"enum", "inferred_overloads::Code"}, {"pointer", "inferred_overloads::Pointer"},
		{"const_pointer", "inferred_overloads::ConstPointer"},
	} {
		add("pick", "pick_"+selection.symbol, []string{selection.nativeType})
	}
	add("empty", "pick_zero", nil)
	add("empty", "pick_one", []string{"int"})
	add("apply", "pick_apply", []string{"inferred_overloads::Entry", "int"})
	add("checked", "pick_checked", []string{"int"})
	add("store", "pick_store_int", []string{"int"})
	add("store", "pick_store_double", []string{"double"})
	add("generic", "pick_template", []string{"int"})
	selections[len(selections)-1].TemplateArgs = []string{"int"}
	add("generic", "pick_ordinary", []string{"double"})
	add("Receiver::static_only", "pick_static_int", []string{"int"})
	add("Receiver::static_only", "pick_static_double", []string{"double"})
	for _, name := range []string{"pair", "entry", "read"} {
		selections = append(selections, cxx.FunctionSelection{Name: "inferred_overloads::" + name, Symbol: "pick_" + name})
	}
	return cxx.InferFunctions(ctx, header, selections, opts)
}

// InferSelectedMethodOverloads selects native parameters and receiver cv/ref
// forms. Explicit construction/deletion and the base view supply owner policy.
// The guarded fixture header permits composing the generated owner/method TUs.
func InferSelectedMethodOverloads(ctx context.Context, header string, opts cxx.DiscoveryOptions) (*cxx.InferredMethodFacade, error) {
	owners, err := cxx.Generate(header, []cxx.Class{{
		Name: "inferred_overloads::Receiver", Delete: "pick_delete",
		Constructors: []cxx.Constructor{{Symbol: "pick_new", Args: []abi.Type{abi.I32}}},
		Bases:        []cxx.Base{{Name: "inferred_overloads::Base", Symbol: "pick_base"}},
	}})
	if err != nil {
		return nil, err
	}
	var selections []cxx.MethodSelection
	add := func(class, name, symbol string, parameters []string, constant, volatile bool, ref cxx.RefQualifier) {
		selections = append(selections, cxx.MethodSelection{
			Class: "inferred_overloads::" + class, Name: name, Symbol: symbol,
			Overload: &cxx.MethodOverload{ParameterTypes: parameters, Const: constant, Volatile: volatile, Ref: ref},
		})
	}
	for _, noexc := range []bool{false, true} {
		for ref := cxx.RefNone; ref <= cxx.RefRValue; ref++ {
			for cv := 0; cv < 4; cv++ {
				name := "score"
				if ref == cxx.RefNone {
					name = "plain"
				}
				prefix := "q"
				if noexc {
					name += "_noexcept"
					prefix = "n"
				}
				symbol := fmt.Sprintf("pick_%s%d", prefix, int(ref)*4+cv)
				add("Alias", name, symbol, []string{"int"}, cv&1 != 0, cv&2 != 0, ref)
			}
		}
	}
	add("Receiver", "score", "pick_score_double", []string{"double"}, true, false, cxx.RefLValue)
	add("Receiver", "zero", "pick_zero_mutable", nil, false, false, cxx.RefNone)
	add("Receiver", "zero", "pick_zero_const", nil, true, false, cxx.RefNone)
	add("Receiver", "operator()", "pick_operator_int", []string{"int"}, false, false, cxx.RefLValue)
	add("Receiver", "operator()", "pick_operator_double", []string{"double"}, true, false, cxx.RefRValue)
	add("Receiver", "plus", "pick_plus", []string{"int"}, true, false, cxx.RefLValue)
	selections[len(selections)-1].TemplateArgs = []string{"int", "2"}
	add("Receiver", "checked", "pick_checked_int", []string{"int"}, false, false, cxx.RefLValue)
	add("Receiver", "checked", "pick_checked_double", []string{"double"}, true, false, cxx.RefLValue)
	add("Receiver", "native_long", "pick_native_int", []string{"int"}, true, false, cxx.RefNone)
	add("Receiver", "native_long", "pick_native_long", []string{"long"}, true, false, cxx.RefNone)
	add("Receiver", "pointer", "pick_pointer_mutable", []string{"inferred_overloads::Pointer"}, true, false, cxx.RefNone)
	add("Receiver", "pointer", "pick_pointer_const", []string{"inferred_overloads::ConstPointer"}, true, false, cxx.RefNone)
	add("Receiver", "apply", "pick_apply_member", []string{"inferred_overloads::Entry", "int"}, true, false, cxx.RefNone)
	add("Receiver", "inherited", "pick_inherited_int", []string{"int"}, true, false, cxx.RefNone)
	add("Receiver", "inherited", "pick_inherited_double", []string{"double"}, true, false, cxx.RefNone)
	add("Base", "score", "pick_virtual_int", []string{"int"}, true, false, cxx.RefLValue)
	add("Base", "score", "pick_virtual_double", []string{"double"}, true, false, cxx.RefLValue)
	add("Receiver", "shared", "pick_shared", []string{"int"}, true, false, cxx.RefNone)
	add("Receiver", "mixed_template", "pick_mixed_ordinary", []string{"double"}, true, false, cxx.RefLValue)
	add("Receiver", "mixed_template", "pick_mixed_template", []string{"int"}, true, false, cxx.RefLValue)
	selections[len(selections)-1].TemplateArgs = []string{"int"}
	add("Receiver", "mark", "pick_mark_int", []string{"int"}, false, false, cxx.RefNone)
	add("Receiver", "mark", "pick_mark_double", []string{"double"}, false, false, cxx.RefNone)
	selections = append(selections, cxx.MethodSelection{Class: "inferred_overloads::Receiver", Name: "read", Symbol: "pick_read_member"})
	result, err := cxx.InferMethods(ctx, header, selections, opts)
	if err != nil {
		return nil, err
	}
	result.Facade = &cxx.Facade{
		CHeader:   append(owners.CHeader, result.Facade.CHeader...),
		CXXSource: append(owners.CXXSource, result.Facade.CXXSource...),
		Functions: append(owners.Functions, result.Facade.Functions...),
	}
	return result, nil
}
