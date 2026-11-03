package moduleexample

import (
	"context"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/module"
)

// LazySymbols selects declared symbol contracts now, but opens the whole selected
// graph only when WithGraph is first used. Discovery reads metadata; native files
// are read later. Keep the options/captured inputs stable until initialization.
// ctx controls discovery now, not the later Open/OS loader.
func LazySymbols(ctx context.Context, options dylib.Options, directories ...string) (*module.Lazy, error) {
	selection, err := DiscoverSymbols(ctx, directories...)
	if err != nil {
		return nil, err
	}
	return module.NewLazy(func() (*module.Graph, error) {
		return module.Open("", selection.Manifest, options)
	})
}
