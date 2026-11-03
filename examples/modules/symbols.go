package moduleexample

import (
	"context"

	"github.com/ramblelabs/dylib-go/module"
)

// DiscoverSymbols locates one function and one data export without naming their
// owning modules. Contracts remain explicit; Open loads the resulting graph and
// its normal leases retain native code/data. Catalog ambiguity must be resolved
// by the caller rather than by trying native providers in order.
func DiscoverSymbols(ctx context.Context, directories ...string) (module.SymbolSelection, error) {
	return module.DiscoverSymbols(ctx, []module.SymbolRequirement{
		{Symbol: "module_run", Version: "app/1"},
		{Symbol: "module_bias", Version: "math/1"},
	}, directories...)
}
