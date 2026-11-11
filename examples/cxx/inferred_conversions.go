package cxxexample

import (
	"context"
	"fmt"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// InferSelectedConversions derives conversion result transports and receiver
// qualifiers. Native owner construction, destruction and base views use an
// explicit manifest. This fixture's guarded header permits combining the sources.
func InferSelectedConversions(ctx context.Context, header string, opts cxx.DiscoveryOptions) (*cxx.InferredMethodFacade, error) {
	classes := []cxx.Class{
		{Name: "inferred_conversions::Receiver", Delete: "convert_auto_delete", Constructors: []cxx.Constructor{{Symbol: "convert_auto_new", Args: []abi.Type{abi.I32}}}, Bases: []cxx.Base{{Name: "inferred_conversions::Base", Symbol: "convert_auto_base"}}, Methods: []cxx.Method{{Name: "forms_alive", Symbol: "convert_forms_alive", Static: true, Signature: abi.Signature{Result: abi.I32}}}},
		{Name: "inferred_conversions::Template", Delete: "convert_template_delete", Constructors: []cxx.Constructor{{Symbol: "convert_template_new"}}},
	}
	var selected []cxx.MethodSelection
	add := func(class, target, symbol string, overload *cxx.MethodOverload) {
		selected = append(selected, cxx.MethodSelection{Class: "inferred_conversions::" + class, ConversionType: target, Symbol: symbol, Overload: overload})
	}
	for _, prefix := range []string{"q", "n"} {
		plain, qualified := "Plain", "Qualified"
		if prefix == "n" {
			plain, qualified = "NPlain", "NQualified"
		}
		for _, class := range []string{plain, qualified} {
			classes = append(classes, cxx.Class{Name: "inferred_conversions::" + class, Delete: "convert_" + prefix + "_" + class + "_delete", Constructors: []cxx.Constructor{{Symbol: "convert_" + prefix + "_" + class + "_new", Args: []abi.Type{abi.I32}}}})
		}
		for i := 0; i < 12; i++ {
			class := plain
			if i >= 4 {
				class = qualified
			}
			add(class, "int", fmt.Sprintf("convert_auto_%s%d", prefix, i), &cxx.MethodOverload{Const: i&1 != 0, Volatile: i&2 != 0, Ref: cxx.RefQualifier(i / 4)})
		}
	}
	add("Receiver", "int", "convert_auto_int", &cxx.MethodOverload{Const: true, Ref: cxx.RefLValue})
	add("Receiver", "int", "convert_auto_take", &cxx.MethodOverload{Ref: cxx.RefRValue})
	for _, choice := range []struct{ target, symbol string }{
		{"bool", "bool"}, {"inferred_conversions::Narrow", "narrow"}, {"inferred_conversions::Wide", "wide"},
		{"inferred_conversions::Code", "code"}, {"inferred_conversions::Entry", "entry"},
		{"inferred_conversions::IntPointer", "pointer"}, {"inferred_conversions::ConstPointer", "const_pointer"},
		{"inferred_conversions::Opaque", "self"}, {"long", "long"}, {"double", "double"},
	} {
		add("Receiver", choice.target, "convert_auto_"+choice.symbol, nil)
	}
	add("Base", "int", "convert_auto_virtual", nil)
	for _, target := range []string{"int", "double"} {
		add("Template", target, "convert_template_"+target, &cxx.MethodOverload{Const: true})
	}
	for _, name := range []string{"read", "apply"} {
		selected = append(selected, cxx.MethodSelection{Class: "inferred_conversions::Receiver", Name: name, Symbol: "convert_auto_" + name})
	}
	owners, err := cxx.Generate(header, classes)
	if err != nil {
		return nil, err
	}
	result, err := cxx.InferMethods(ctx, header, selected, opts)
	if err != nil {
		return nil, err
	}
	result.Facade = &cxx.Facade{CHeader: append(owners.CHeader, result.Facade.CHeader...), CXXSource: append(owners.CXXSource, result.Facade.CXXSource...), Functions: append(owners.Functions, result.Facade.Functions...)}
	return result, nil
}
