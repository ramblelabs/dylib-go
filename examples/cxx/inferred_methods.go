package cxxexample

import (
	"context"
	"fmt"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// InferSelectedMethods derives member transports and receiver qualifiers. Native
// construction, deletion and borrowed base views remain explicit application
// policy. This fixture's guarded header permits combining its two generated TUs;
// applications with unguarded headers should keep owner and method sources in
// separate translation units.
func InferSelectedMethods(ctx context.Context, header string, opts cxx.DiscoveryOptions) (*cxx.InferredMethodFacade, error) {
	owners, err := cxx.Generate(header, []cxx.Class{{Name: "inferred_members::Receiver", Delete: "auto_delete", Constructors: []cxx.Constructor{{Symbol: "auto_new", Args: []abi.Type{abi.I32}}}, Bases: []cxx.Base{{Name: "inferred_members::Base", Symbol: "auto_base"}, {Name: "inferred_members::Shared", Symbol: "auto_shared"}}}})
	if err != nil {
		return nil, err
	}
	var selected []cxx.MethodSelection
	add := func(class, name, symbol string, args []string) {
		selected = append(selected, cxx.MethodSelection{Class: "inferred_members::" + class, Name: name, Symbol: symbol, TemplateArgs: args})
	}
	for _, prefix := range []string{"q", "n"} {
		for i := 0; i < 12; i++ {
			name := fmt.Sprintf("%s%d", prefix, i)
			add("Alias", name, "auto_"+name, nil)
		}
	}
	for _, name := range []string{"mark", "read", "take", "fail", "native_long", "character", "narrow", "wide", "code", "boolean", "mixed", "self", "entry", "apply", "inherited", "shared"} {
		symbol := "auto_" + name
		if name == "shared" {
			symbol = "auto_shared_call"
		}
		add("Receiver", name, symbol, nil)
	}
	add("Base", "score", "auto_score", nil)
	add("Receiver", "operator()", "auto_operator", nil)
	add("Receiver", "plus", "auto_plus", []string{"int", "2"})
	result, err := cxx.InferMethods(ctx, header, selected, opts)
	if err != nil {
		return nil, err
	}
	result.Facade = &cxx.Facade{CHeader: append(owners.CHeader, result.Facade.CHeader...), CXXSource: append(owners.CXXSource, result.Facade.CXXSource...), Functions: append(owners.Functions, result.Facade.Functions...)}
	return result, nil
}
