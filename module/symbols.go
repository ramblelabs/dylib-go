package module

import (
	"context"
	"fmt"
	"sort"
)

// SymbolRequirement selects an exact linkage name and opaque provider ABI
// version. Provider optionally names the module; when empty, exactly one
// catalog provider must export Symbol at Version. No ABI is inferred from a
// matching symbol. Different module names can use the same version text.
type SymbolRequirement struct {
	Symbol, Version string
	Provider        string
}

func (r SymbolRequirement) validate() error {
	if !textName(r.Symbol) || !contractName(r.Version) || r.Provider != "" && !contractName(r.Provider) {
		return fmt.Errorf("module: invalid symbol requirement %q", r.Symbol)
	}
	return nil
}

// SymbolSelection contains a complete, exact dependency manifest and ordered
// copies of the requests with Provider filled in. Repeated requests are retained,
// while each provider is a manifest root once. Pass Manifest to Open, then use
// Graph.Session(binding.Provider) with Resolve/Bind/caller-defined adapters.
// Symbols describe declared exports, not addresses or native signatures.
type SymbolSelection struct {
	Manifest Manifest
	Symbols  []SymbolRequirement
}

type exportContract struct{ symbol, version string }

// DiscoverSymbols selects provider roots by declared exports from explicit
// directories, using Discover's catalog, IO, validation and cancellation limits.
// It accepts 1-256 requests. A missing or ambiguous symbol contract fails before
// native loading; ambiguity is not resolved by directory order, dependency
// viability, native file availability or signature guessing. An explicit
// Provider can disambiguate exports from different modules.
//
// The result pins one exact version per module and absolute native input paths.
// Every local catalog declaration is validated, including unselected providers;
// selected dependencies retain their exact version/export/cycle checks. Discovery
// reads metadata once, never opens native inputs, and returns a zero selection on
// any error. Native initialization and failure behavior belong to a later Open;
// loading failure does not trigger provider fallback. Inputs remain caller-owned
// and must stay stable during this call; this function does not mutate requests.
func DiscoverSymbols(ctx context.Context, requests []SymbolRequirement, directories ...string) (SymbolSelection, error) {
	if err := discoveryInputs(ctx, len(requests), len(directories)); err != nil {
		return SymbolSelection{}, err
	}
	bindings := append([]SymbolRequirement(nil), requests...)
	wanted := make(map[exportContract]bool)
	for _, r := range bindings {
		if err := r.validate(); err != nil {
			return SymbolSelection{}, err
		}
		wanted[exportContract{r.Symbol, r.Version}] = true
	}
	available, err := discoverCatalogs(ctx, directories)
	if err != nil {
		return SymbolSelection{}, err
	}
	// Index only requested contracts; unrelated exports do not inflate the index.
	// Stable provider ordering also makes ambiguity diagnostics deterministic.
	keys := make([]Requirement, 0, len(available))
	for key := range available {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Name != keys[j].Name {
			return keys[i].Name < keys[j].Name
		}
		return keys[i].Version < keys[j].Version
	})
	index := make(map[exportContract][]string)
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return SymbolSelection{}, err
		}
		for _, symbol := range available[key].Exports {
			if err := ctx.Err(); err != nil {
				return SymbolSelection{}, err
			}
			contract := exportContract{symbol, key.Version}
			if wanted[contract] {
				index[contract] = append(index[contract], key.Name)
			}
		}
	}
	var roots []CompatibleRequirement
	selected := make(map[string]string)
	for i := range bindings {
		if err := ctx.Err(); err != nil {
			return SymbolSelection{}, err
		}
		r := &bindings[i]
		providers := index[exportContract{r.Symbol, r.Version}]
		if r.Provider != "" {
			var matching []string
			for _, provider := range providers {
				if provider == r.Provider {
					matching = append(matching, provider)
				}
			}
			providers = matching
		}
		if len(providers) == 0 {
			if r.Provider == "" {
				return SymbolSelection{}, fmt.Errorf("module: no provider exports %q at version %q", r.Symbol, r.Version)
			}
			return SymbolSelection{}, fmt.Errorf("module: provider %q does not export %q at version %q", r.Provider, r.Symbol, r.Version)
		}
		if len(providers) != 1 {
			return SymbolSelection{}, fmt.Errorf("module: ambiguous export %q at version %q from providers %q", r.Symbol, r.Version, providers)
		}
		r.Provider = providers[0]
		if version, exists := selected[r.Provider]; exists {
			if version != r.Version {
				return SymbolSelection{}, fmt.Errorf("module: conflicting requested versions for %q: %q and %q", r.Provider, version, r.Version)
			}
			continue
		}
		selected[r.Provider] = r.Version
		roots = append(roots, CompatibleRequirement{Name: r.Provider, Versions: []string{r.Version}})
	}
	manifest, err := selectCompatible(ctx, roots, nil, available)
	if err != nil {
		return SymbolSelection{}, err
	}
	return SymbolSelection{Manifest: manifest, Symbols: bindings}, nil
}
