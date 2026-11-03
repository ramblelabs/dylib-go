package cxxexample

import (
	"context"
	"fmt"
	"strings"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// DiscoverHierarchy supplies the executed fixture's base-name resolution policy.
// Other headers may need accessible aliases for contextual/private/template names.
// The library traverses in Go, then the compiler validates all exported views.
func DiscoverHierarchy(ctx context.Context, header string, opts cxx.DiscoveryOptions) (*cxx.Facade, error) {
	const prefix = "hierarchy::"
	resolve := func(_ string, base cxx.BaseDeclaration) (string, error) {
		if strings.HasPrefix(base.NativeType, prefix) {
			return base.NativeType, nil
		}
		return prefix + base.NativeType, nil
	}
	graph, err := cxx.DiscoverHierarchy(ctx, header, prefix+"Alias", resolve, opts)
	if err != nil {
		return nil, err
	}
	classes := make([]cxx.Class, len(graph.Classes))
	for i, declaration := range graph.Classes {
		classes[i].Name = declaration.Name
		if declaration.Name == prefix+"Alias" {
			classes[i].Delete = "hier_delete"
			classes[i].Constructors = []cxx.Constructor{{Symbol: "hier_new", Args: []abi.Type{abi.I32}}}
		}
		if declaration.Name == prefix+"Counter" {
			classes[i].Methods = []cxx.Method{
				{Name: "score", Symbol: "hier_score", Const: true, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}},
				{Name: "live", Symbol: "hier_live", Static: true, Signature: abi.Signature{Result: abi.I32}},
			}
		}
		for _, relation := range graph.Conversions {
			if relation.Derived == declaration.Name {
				short := func(name string) string { return strings.ToLower(strings.TrimPrefix(name, prefix)) }
				classes[i].Bases = append(classes[i].Bases, cxx.Base{Name: relation.Base, Symbol: "hier_" + short(relation.Derived) + "_" + short(relation.Base)})
			}
		}
	}
	if len(classes) != 5 || len(graph.Conversions) != 6 {
		return nil, fmt.Errorf("expected five selected classes and six native conversions")
	}
	return cxx.Generate(header, classes)
}
