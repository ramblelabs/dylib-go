package module_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	example "github.com/ramblelabs/dylib-go/examples/modules"
	"github.com/ramblelabs/dylib-go/module"
)

func TestDiscoverSymbolSelection(t *testing.T) {
	appDir, providerDir := t.TempDir(), t.TempDir()
	mods := example.Manifest("provider.o", "consumer.o").Modules
	writeCatalog(t, appDir, "app", mods[0])
	other := mods[1]
	other.Version = "math/2"
	other.Files = []string{"not-installed.o"}
	writeCatalog(t, providerDir, "math", mods[1], other, module.Module{Name: "unused", Version: "1", Files: []string{"absent.o"}, Imports: []module.Import{{Provider: "not-installed", Version: "1", Symbol: "unused"}}})
	requests := []module.SymbolRequirement{{Symbol: "module_run", Version: "app/1"}, {Symbol: "module_bias", Version: "math/1"}, {Symbol: "module_run", Version: "app/1"}}
	before := append([]module.SymbolRequirement(nil), requests...)
	got, err := module.DiscoverSymbols(context.Background(), requests, appDir, providerDir, appDir)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]module.SymbolRequirement(nil), requests...)
	want[0].Provider = "app"
	want[1].Provider = "math"
	want[2].Provider = "app"
	if !reflect.DeepEqual(got.Symbols, want) || !reflect.DeepEqual(requests, before) || !reflect.DeepEqual(got.Manifest.Roots, []string{"app", "math"}) || len(got.Manifest.Modules) != 2 {
		t.Fatal("symbol/provider roots and caller ownership", got, requests)
	}
	if got.Manifest.Modules[0].Files[0] != filepath.Join(appDir, "consumer.o") || got.Manifest.Modules[1].Files[0] != filepath.Join(providerDir, "provider.o") {
		t.Fatal("catalog-relative paths", got)
	}
	if err := got.Manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	again, err := module.DiscoverSymbols(context.Background(), requests, appDir, providerDir)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatal("unstable symbol selection", again, err)
	}
	got.Symbols[0].Provider = "changed"
	if !reflect.DeepEqual(requests, before) {
		t.Fatal("published bindings alias caller requests")
	}
	selected, err := example.DiscoverSymbols(context.Background(), appDir, providerDir)
	if err != nil || len(selected.Symbols) != 2 || selected.Symbols[0].Provider != "app" || selected.Symbols[1].Provider != "math" {
		t.Fatal("executed example", selected, err)
	}
}

func TestDiscoverSymbolAmbiguity(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	moduleA := module.Module{Name: "a", Version: "1", Files: []string{"not-installed.o"}, Exports: []string{"entry"}}
	moduleZ := moduleA
	moduleZ.Name = "z"
	moduleZ.Imports = []module.Import{{Provider: "missing", Version: "1", Symbol: "entry"}}
	writeCatalog(t, left, "z", moduleZ)
	writeCatalog(t, right, "a", moduleA)
	auto := []module.SymbolRequirement{{Symbol: "entry", Version: "1"}}
	var message string
	for i, dirs := range [][]string{{left, right}, {right, left}} {
		got, err := module.DiscoverSymbols(context.Background(), auto, dirs...)
		if err == nil || !reflect.DeepEqual(got, module.SymbolSelection{}) || !strings.Contains(err.Error(), `providers ["a" "z"]`) {
			t.Fatal("directory/dependency viability hid ambiguity", got, err)
		}
		if i == 0 {
			message = err.Error()
		} else if err.Error() != message {
			t.Fatal("unstable ambiguity diagnostics", message, err)
		}
	}
	got, err := module.DiscoverSymbols(context.Background(), []module.SymbolRequirement{{Symbol: "entry", Version: "1", Provider: "a"}}, left, right)
	if err != nil || len(got.Manifest.Modules) != 1 || got.Symbols[0].Provider != "a" {
		t.Fatal("explicit provider", got, err)
	}
	if got, err := module.DiscoverSymbols(context.Background(), []module.SymbolRequirement{{Symbol: "entry", Version: "1", Provider: "z"}}, left, right); err == nil || !reflect.DeepEqual(got, module.SymbolSelection{}) {
		t.Fatal("explicit invalid graph silently fell back", got, err)
	}
}

func TestDiscoverSymbolContracts(t *testing.T) {
	for name, patch := range map[string]func([]module.Module, []module.SymbolRequirement) ([]module.Module, []module.SymbolRequirement){
		"missing_export": func(ms []module.Module, rs []module.SymbolRequirement) ([]module.Module, []module.SymbolRequirement) {
			rs[0].Symbol = "missing"
			return ms, rs
		},
		"wrong_version": func(ms []module.Module, rs []module.SymbolRequirement) ([]module.Module, []module.SymbolRequirement) {
			rs[0].Version = "app/2"
			return ms, rs
		},
		"wrong_provider": func(ms []module.Module, rs []module.SymbolRequirement) ([]module.Module, []module.SymbolRequirement) {
			rs[0].Provider = "math"
			return ms, rs
		},
		"missing_dependency": func(ms []module.Module, rs []module.SymbolRequirement) ([]module.Module, []module.SymbolRequirement) {
			return ms[:1], rs
		},
		"missing_dependency_export": func(ms []module.Module, rs []module.SymbolRequirement) ([]module.Module, []module.SymbolRequirement) {
			ms[1].Exports = []string{"other"}
			return ms, rs
		},
		"conflicting_roots": func(ms []module.Module, rs []module.SymbolRequirement) ([]module.Module, []module.SymbolRequirement) {
			other := ms[1]
			other.Version = "math/2"
			return append(ms, other), append(rs, module.SymbolRequirement{Symbol: "module_bias", Version: "math/2"})
		},
		"conflicting_requested_versions": func(ms []module.Module, rs []module.SymbolRequirement) ([]module.Module, []module.SymbolRequirement) {
			other := ms[1]
			other.Version = "math/2"
			return append(ms, other), []module.SymbolRequirement{{Symbol: "module_add", Version: "math/1"}, {Symbol: "module_bias", Version: "math/2"}}
		},
		"duplicate_provider": func(ms []module.Module, rs []module.SymbolRequirement) ([]module.Module, []module.SymbolRequirement) {
			return append(ms, ms[1]), rs
		},
		"bad_unused": func(ms []module.Module, rs []module.SymbolRequirement) ([]module.Module, []module.SymbolRequirement) {
			return append(ms, module.Module{Name: "unused", Version: "1"}), rs
		},
		"cycle": func(ms []module.Module, rs []module.SymbolRequirement) ([]module.Module, []module.SymbolRequirement) {
			ms[1].Imports = []module.Import{{Provider: "app", Version: "app/1", Symbol: "module_run"}}
			return ms, rs
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			mods, requests := patch(example.Manifest("provider.o", "consumer.o").Modules, []module.SymbolRequirement{{Symbol: "module_run", Version: "app/1"}})
			writeCatalog(t, dir, "catalog", mods...)
			got, err := module.DiscoverSymbols(context.Background(), requests, dir)
			if err == nil || !reflect.DeepEqual(got, module.SymbolSelection{}) {
				t.Fatal("invalid symbol graph published", got, err)
			}
		})
	}
}

func TestDiscoverSymbolInputs(t *testing.T) {
	dir := t.TempDir()
	for name, requests := range map[string][]module.SymbolRequirement{
		"empty": nil, "too_many": make([]module.SymbolRequirement, 257),
		"empty_symbol": {{Version: "1"}}, "null_symbol": {{Symbol: "bad\x00name", Version: "1"}},
		"empty_version": {{Symbol: "entry"}}, "invalid_version": {{Symbol: "entry", Version: " bad"}},
		"invalid_provider": {{Symbol: "entry", Version: "1", Provider: "bad\x00name"}},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := module.DiscoverSymbols(context.Background(), requests, dir); err == nil || !reflect.DeepEqual(got, module.SymbolSelection{}) {
				t.Fatal(got, err)
			}
		})
	}
	request := []module.SymbolRequirement{{Symbol: "entry", Version: "1"}}
	for name, dirs := range map[string][]string{"no_directories": nil, "empty_directory": {""}, "missing_directory": {filepath.Join(dir, "missing")}, "too_many_directories": make([]string, 17), "empty_catalog": {dir}} {
		t.Run(name, func(t *testing.T) {
			if got, err := module.DiscoverSymbols(context.Background(), request, dirs...); err == nil || !reflect.DeepEqual(got, module.SymbolSelection{}) {
				t.Fatal(got, err)
			}
		})
	}
	t.Run("nil_context", func(t *testing.T) {
		if got, err := module.DiscoverSymbols(nil, request, dir); err == nil || !reflect.DeepEqual(got, module.SymbolSelection{}) {
			t.Fatal(got, err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if got, err := module.DiscoverSymbols(ctx, request, dir); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, module.SymbolSelection{}) {
			t.Fatal(got, err)
		}
	})
	t.Run("invalid_json", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "bad.dylib.json")
		if err := os.WriteFile(file, []byte("invalid"), 0600); err != nil {
			t.Fatal(err)
		}
		if got, err := module.DiscoverSymbols(context.Background(), request, filepath.Dir(file)); err == nil || !reflect.DeepEqual(got, module.SymbolSelection{}) {
			t.Fatal(got, err)
		}
	})
}

type symbolCancellation struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *symbolCancellation) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		c.cancel()
	}
	return c.Context.Err()
}
func TestDiscoverSymbolLimitsAndCancellation(t *testing.T) {
	dir := t.TempDir()
	exports := make([]string, 256)
	requests := make([]module.SymbolRequirement, 256)
	for i := range exports {
		exports[i] = fmt.Sprintf("entry_%03d", i)
		requests[i] = module.SymbolRequirement{Symbol: exports[i], Version: "1"}
	}
	writeCatalog(t, dir, "all", module.Module{Name: "provider", Version: "1", Files: []string{"not-installed.o"}, Exports: exports})
	t.Run("maximum", func(t *testing.T) {
		got, err := module.DiscoverSymbols(context.Background(), requests, dir)
		if err != nil || len(got.Symbols) != 256 || !reflect.DeepEqual(got.Manifest.Roots, []string{"provider"}) {
			t.Fatal(got, err)
		}
	})
	for _, checks := range []int{1, 12, 80, 300} {
		t.Run(fmt.Sprintf("cancel_%d", checks), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			counted := &symbolCancellation{Context: ctx, cancel: cancel, remaining: checks}
			got, err := module.DiscoverSymbols(counted, requests, dir)
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, module.SymbolSelection{}) {
				t.Fatal("cancelled discovery published partial plan", got, err)
			}
		})
	}
	t.Run("raw_linkage_names", func(t *testing.T) {
		local := t.TempDir()
		name := "?entry@Native@@YAHHH@Z"
		writeCatalog(t, local, "mangled", module.Module{Name: "native", Version: "opaque/1", Files: []string{"missing.obj"}, Exports: []string{name}})
		got, err := module.DiscoverSymbols(context.Background(), []module.SymbolRequirement{{Symbol: name, Version: "opaque/1"}}, local)
		if err != nil || got.Symbols[0].Symbol != name {
			t.Fatal("linkage name was rewritten", got, err)
		}
	})
}
