package cxxexample

import (
	"context"
	"fmt"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// DiscoverHeaderHierarchy obtains the base catalog from the complete header,
// including global types and aliases in other namespaces. ABI, view export names
// and ownership are application choices; the compiler performs pointer adjustment.
func DiscoverHeaderHierarchy(ctx context.Context, header string, opts cxx.DiscoveryOptions) (*cxx.Facade, error) {
	graph, err := cxx.DiscoverHeaderHierarchy(ctx, header, "RootAlias", opts)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{"RootAlias": "alias", "header_types::Counter": "counter", "GlobalCounter": "counteralias", "GlobalPad": "padding", "branches::Left": "left", "branches::Right": "right"}
	if len(graph.Classes) != 6 || len(graph.Conversions) != 9 {
		return nil, fmt.Errorf("expected six header selections and nine native conversions")
	}
	classes := make([]cxx.Class, len(graph.Classes))
	for i, d := range graph.Classes {
		label := labels[d.Name]
		if label == "" {
			return nil, fmt.Errorf("unselected facade type %s", d.Name)
		}
		classes[i].Name = d.Name
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
		for _, r := range graph.Conversions {
			if r.Derived == d.Name {
				classes[i].Bases = append(classes[i].Bases, cxx.Base{Name: r.Base, Symbol: "hier_" + label + "_" + labels[r.Base]})
			}
		}
	}
	return cxx.Generate(header, classes)
}
