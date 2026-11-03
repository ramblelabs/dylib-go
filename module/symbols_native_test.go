//go:build cgo && (linux || darwin || windows)

package module_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	dylib "github.com/ramblelabs/dylib-go"
	example "github.com/ramblelabs/dylib-go/examples/modules"
	"github.com/ramblelabs/dylib-go/module"
)

func TestNativeSymbolModuleCalls(t *testing.T) {
	for _, kind := range []string{"object", "archive", "library"} {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(kind+"/"+opt, func(t *testing.T) {
				providerDir, appDir := t.TempDir(), t.TempDir()
				provider := produce(t, providerDir, "provider", `API int module_bias=2; API int module_add(int a,int b){return a+b;}`, kind, opt)
				consumer := produce(t, appDir, "consumer", consumerCode, "object", opt)
				mods := example.Manifest(filepath.Base(provider), filepath.Base(consumer)).Modules
				writeCatalog(t, appDir, "app", mods[0])
				unused := mods[1]
				unused.Version = "math/2"
				unused.Files = []string{"not-installed.o"}
				writeCatalog(t, providerDir, "math", mods[1], unused)
				for _, mode := range []string{"automatic", "explicit"} {
					t.Run(mode, func(t *testing.T) {
						var selected module.SymbolSelection
						var err error
						if mode == "automatic" {
							selected, err = example.DiscoverSymbols(context.Background(), appDir, providerDir)
						} else {
							selected, err = module.DiscoverSymbols(context.Background(), []module.SymbolRequirement{{Symbol: "module_run", Version: "app/1", Provider: "app"}, {Symbol: "module_bias", Version: "math/1", Provider: "math"}}, appDir, providerDir)
						}
						if err != nil || !reflect.DeepEqual(selected.Manifest.Roots, []string{"app", "math"}) || len(selected.Manifest.Modules) != 2 || selected.Symbols[0].Provider != "app" || selected.Symbols[1].Provider != "math" {
							t.Fatal("wrong symbol/provider plan", selected, err)
						}
						graph, err := module.Open("", selected.Manifest, dylib.Options{})
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() {
							if err := graph.Close(); err != nil {
								t.Error(err)
							}
						})
						app, err := graph.Session(selected.Symbols[0].Provider)
						if err != nil {
							t.Fatal(err)
						}
						lease, read := nativeRead(t, app, selected.Symbols[0].Symbol)
						math, err := graph.Session(selected.Symbols[1].Provider)
						if err != nil {
							t.Fatal(err)
						}
						symbol, err := math.Resolve(selected.Symbols[1].Symbol)
						if err != nil {
							t.Fatal(err)
						}
						data, err := symbol.Acquire()
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { data.Close() })
						for i := int32(0); i < 25; i++ {
							if got := read(20+i, 20); got != 42+i {
								t.Fatal("selected function/data provider", got)
							}
						}
						closed := make(chan error, 2)
						go func() { closed <- graph.Close() }()
						go func() { closed <- graph.Close() }()
						deadline := time.Now().Add(5 * time.Second)
						for {
							if _, err := graph.Session("app"); errors.Is(err, dylib.ErrClosed) {
								break
							}
							if time.Now().After(deadline) {
								t.Fatal("graph lookup did not retire")
							}
							time.Sleep(time.Millisecond)
						}
						if got := read(20, 20); got != 42 {
							t.Fatal("code lease lost imported data during retirement", got)
						}
						select {
						case err := <-closed:
							t.Fatal("graph closed over selected leases", err)
						default:
						}
						lease.Close()
						for {
							if _, err := math.Resolve("module_bias"); errors.Is(err, dylib.ErrClosed) {
								break
							}
							if time.Now().After(deadline) {
								t.Fatal("provider did not retire after consumer lease release")
							}
							time.Sleep(time.Millisecond)
						}
						if err := data.WithAddress(func(address uintptr) error {
							if address == 0 {
								return fmt.Errorf("selected data lease lost its native address")
							}
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						data.Close()
						for range 2 {
							select {
							case err := <-closed:
								if err != nil {
									t.Fatal(err)
								}
							case <-time.After(5 * time.Second):
								t.Fatal("selected graph close did not finish")
							}
						}
					})
				}
			})
		}
	}
}

func TestNativeSymbolModuleFailures(t *testing.T) {
	for _, kind := range []string{"object", "archive", "library"} {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(kind+"/"+opt, func(t *testing.T) {
				dir := t.TempDir()
				file := produce(t, dir, "provider", `API int entry(int a,int b){return a+b;}`, kind, opt)
				for _, mode := range []string{"missing_input", "stale_export"} {
					t.Run(mode, func(t *testing.T) {
						mod := module.Module{Name: "selected", Version: "1", Files: []string{filepath.Base(file)}, Exports: []string{"entry"}}
						if mode == "missing_input" {
							mod.Files = []string{"missing-native-input.o"}
						} else {
							mod.Exports = []string{"not_a_native_export"}
						}
						other := mod
						other.Name = "other"
						other.Files = []string{filepath.Base(file)}
						writeCatalog(t, dir, "providers", mod, other)
						plan, err := module.DiscoverSymbols(context.Background(), []module.SymbolRequirement{{Symbol: mod.Exports[0], Version: "1", Provider: "selected"}}, dir)
						if err != nil || len(plan.Manifest.Modules) != 1 || plan.Symbols[0].Provider != "selected" {
							t.Fatal("metadata discovery inspected native inputs", plan, err)
						}
						graph, err := module.Open("", plan.Manifest, dylib.Options{})
						if graph != nil {
							graph.Close()
							t.Fatal("native failure selected fallback or leaked ownership")
						}
						if err == nil || mode == "missing_input" && !errors.Is(err, os.ErrNotExist) {
							t.Fatal("selected native failure was hidden", err)
						}
					})
				}
			})
		}
	}
}
