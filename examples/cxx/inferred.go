package cxxexample

import (
	"context"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// InferSelectedFunctions leaves primitive/enum/address transport selection to
// compiler probes. Pointer storage, callback prototypes and lifetime contracts
// remain caller choices. Bind the generated C header, including status/outputs.
func InferSelectedFunctions(ctx context.Context, header string, opts cxx.DiscoveryOptions) (*cxx.InferredFacade, error) {
	names := []string{"add", "narrow", "wide", "native_long", "character", "code", "toggle", "plain", "boolean", "floating", "mixed", "checked", "constant", "pair", "echo", "store", "read", "entry", "apply"}
	selections := make([]cxx.FunctionSelection, 0, len(names)+2)
	for _, name := range names {
		selections = append(selections, cxx.FunctionSelection{Name: "inferred_api::" + name, Symbol: "auto_" + name})
	}
	selections = append(selections,
		cxx.FunctionSelection{Name: "inferred_api::Static::twice", Symbol: "auto_static"},
		cxx.FunctionSelection{Name: "inferred_api::plus", Symbol: "auto_plus", TemplateArgs: []string{"int", "2"}},
	)
	return cxx.InferFunctions(ctx, header, selections, opts)
}
