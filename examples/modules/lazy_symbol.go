package moduleexample

import (
	"context"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/module"
)

// LazySymbol selects one declared function/data contract on first WithSymbol,
// then loads only its provider graph. Neither catalogs nor native files are read
// by construction. The context and captured options/inputs must stay valid until
// initialization. Separate owners have independent selections and graphs.
func LazySymbol(ctx context.Context, request module.SymbolRequirement, options dylib.Options, directories ...string) (*module.LazySymbol, error) {
	return module.NewLazySymbol(ctx, request, func(_ context.Context, selection module.SymbolSelection) (*module.Graph, error) {
		return module.Open("", selection.Manifest, options)
	}, directories...)
}

// LazyCompiledSymbol also defers the explicit input compiler until first use.
// Returned compiler artifacts stay owned through graph close and cleanup retry.
func LazyCompiledSymbol(ctx context.Context, request module.SymbolRequirement, options dylib.Options, compile module.InputCompiler, directories ...string) (*module.LazySymbol, error) {
	return module.NewLazySymbol(ctx, request, func(ctx context.Context, selection module.SymbolSelection) (*module.Graph, error) {
		return module.OpenCompiled(ctx, "", selection.Manifest, options, compile)
	}, directories...)
}
