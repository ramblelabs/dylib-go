package cxxexample

import (
	"context"
	"fmt"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// DiscoverNamespaceHierarchy selects native bases through a public namespace
// catalog. The compiler resolves the aliases; this example still explicitly
// chooses the facade's ABI, ownership and export names.
func DiscoverNamespaceHierarchy(ctx context.Context, header string, opts cxx.DiscoveryOptions) (*cxx.Facade, error) {
	graph, err := cxx.DiscoverNamespaceHierarchy(ctx, header, "hierarchy_api::Root", []string{"hierarchy_catalog"}, opts)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{
		"hierarchy_api::Root":             "alias",
		"hierarchy_catalog::Counter":      "counter",
		"hierarchy_catalog::CounterAlias": "counteralias",
		"hierarchy_catalog::Padding":      "padding",
		"hierarchy_catalog::Left":         "left",
		"hierarchy_catalog::Right":        "right",
	}
	if len(graph.Classes) != 6 || len(graph.Conversions) != 9 {
		return nil, fmt.Errorf("expected six catalog selections and nine native conversions")
	}
	classes := make([]cxx.Class, len(graph.Classes))
	for i, declaration := range graph.Classes {
		label := labels[declaration.Name]
		if label == "" {
			return nil, fmt.Errorf("unselected facade type %s", declaration.Name)
		}
		classes[i].Name = declaration.Name
		if label == "alias" {
			classes[i].Delete = "hier_delete"
			classes[i].Constructors = []cxx.Constructor{{Symbol: "hier_new", Args: []abi.Type{abi.I32}}}
		}
		if label == "counter" {
			classes[i].Methods = []cxx.Method{
				{Name: "score", Symbol: "hier_score", Const: true, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}},
				{Name: "live", Symbol: "hier_live", Static: true, Signature: abi.Signature{Result: abi.I32}},
			}
		}
		for _, relation := range graph.Conversions {
			if relation.Derived == declaration.Name {
				classes[i].Bases = append(classes[i].Bases, cxx.Base{Name: relation.Base, Symbol: "hier_" + label + "_" + labels[relation.Base]})
			}
		}
	}
	return cxx.Generate(header, classes)
}
