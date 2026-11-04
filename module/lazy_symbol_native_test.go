//go:build cgo && (linux || darwin || windows)

package module_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/compiler/llvm"
	call "github.com/ramblelabs/dylib-go/examples/call"
	example "github.com/ramblelabs/dylib-go/examples/modules"
	"github.com/ramblelabs/dylib-go/module"
)

func callLazySymbol(owner *module.LazySymbol, a, b int32) error {
	return owner.WithSymbol(func(symbol *dylib.Symbol) error {
		return symbol.WithAddress(func(address uintptr) error {
			got, err := call.Int32(address, a, b)
			if err == nil && got != a+b+2 {
				return fmt.Errorf("lazy symbol returned %d, want %d", got, a+b+2)
			}
			return err
		})
	})
}

func retireLazySymbol(t *testing.T, owner *module.LazySymbol, lease *dylib.SymbolLease, function bool) {
	t.Helper()
	done := make(chan error, 2)
	for range 2 {
		go func() { done <- owner.Close() }()
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := owner.WithSymbol(func(*dylib.Symbol) error { return nil }); errors.Is(err, dylib.ErrClosed) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("symbol owner did not retire")
		}
		runtime.Gosched()
	}
	if err := lease.WithAddress(func(address uintptr) error {
		if address == 0 {
			return fmt.Errorf("retired symbol lost native address")
		}
		if function {
			got, err := call.Int32(address, 20, 20)
			if err != nil {
				return err
			}
			if got != 42 {
				return fmt.Errorf("retired function returned %d", got)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		t.Fatal("close crossed external lease", err)
	default:
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("symbol close did not drain lease")
		}
	}
}

func TestNativePerSymbolLazySelection(t *testing.T) {
	for _, kind := range []string{"object", "archive", "library"} {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(kind+"/"+opt, func(t *testing.T) {
				providerDir, appDir := t.TempDir(), t.TempDir()
				provider := produce(t, providerDir, "provider", `static int inits; API int module_bias; __attribute__((constructor)) static void start(void){++inits;module_bias=2;} API int module_add(int a,int b){return a+b+(inits==1?0:1000);}`, kind, opt)
				consumer := produce(t, appDir, "consumer", consumerCode, "object", opt)
				mods := example.Manifest(filepath.Base(provider), filepath.Base(consumer)).Modules
				install := func() {
					writeCatalog(t, appDir, "app", mods[0])
					writeCatalog(t, providerDir, "math", mods[1], module.Module{Name: "uninstalled", Version: "1", Files: []string{"not-installed.o"}, Exports: []string{"uninstalled"}})
				}
				for _, mode := range []string{"function", "data", "missing_native", "missing_catalog", "unused", "wrong_provider", "missing_export"} {
					t.Run(mode, func(t *testing.T) {
						install()
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						req := module.SymbolRequirement{Symbol: "module_run", Version: "app/1"}
						if mode == "data" {
							req = module.SymbolRequirement{Symbol: "module_bias", Version: "math/1"}
						}
						if mode == "unused" {
							req = module.SymbolRequirement{Symbol: "uninstalled", Version: "1"}
						}
						var builds atomic.Int32
						builder := func(_ context.Context, selected module.SymbolSelection) (*module.Graph, error) {
							builds.Add(1)
							if mode == "wrong_provider" || mode == "missing_export" {
								name := "app"
								if mode == "wrong_provider" {
									name = "other"
								}
								return module.Open("", module.Manifest{Format: module.FormatVersion, Roots: []string{name}, Modules: []module.Module{{Name: name, Version: "app/1", Files: []string{provider}, Exports: []string{"module_add", "module_bias"}}}}, dylib.Options{})
							}
							if mode == "data" && (len(selected.Manifest.Modules) != 1 || selected.Manifest.Roots[0] != "math") {
								t.Error("data symbol selected unrelated app", selected)
							}
							graph, err := module.Open("", selected.Manifest, dylib.Options{})
							// The owner must keep the pinned binding across callback mutation.
							selected.Symbols[0].Provider = "changed"
							return graph, err
						}
						if mode == "missing_native" {
							if err := os.Rename(provider, provider+".held"); err != nil {
								t.Fatal(err)
							}
							defer func() {
								if _, err := os.Stat(provider + ".held"); err == nil {
									if err := os.Rename(provider+".held", provider); err != nil {
										t.Error(err)
									}
								}
							}()
						}
						if mode == "missing_catalog" || mode == "unused" {
							for _, path := range []string{filepath.Join(appDir, "app.dylib.json"), filepath.Join(providerDir, "math.dylib.json")} {
								if err := os.Remove(path); err != nil {
									t.Fatal(err)
								}
							}
						}
						owner, err := module.NewLazySymbol(ctx, req, builder, appDir, providerDir)
						if err != nil || builds.Load() != 0 {
							t.Fatal("constructor selected/loaded inputs", err, builds.Load())
						}
						t.Cleanup(func() {
							if err := owner.Close(); err != nil {
								t.Error(err)
							}
						})
						if mode == "unused" {
							if err := owner.Close(); err != nil || builds.Load() != 0 {
								t.Fatal(err, builds.Load())
							}
							return
						}
						if mode == "wrong_provider" || mode == "missing_export" {
							first := owner.WithSymbol(func(*dylib.Symbol) error { t.Error("wrong graph published selected symbol"); return nil })
							if first == nil {
								t.Fatal("selected provider/export not checked")
							}
							if next := owner.WithSymbol(func(*dylib.Symbol) error { return nil }); next != first || builds.Load() != 1 {
								t.Fatal(first, next, builds.Load())
							}
							if err := owner.Close(); err != nil {
								t.Fatal(err)
							}
							return
						}
						if mode == "missing_native" || mode == "missing_catalog" {
							first := owner.WithSymbol(func(*dylib.Symbol) error { t.Error("failed symbol admitted use"); return nil })
							if first == nil || mode == "missing_native" && !errors.Is(first, os.ErrNotExist) {
								t.Fatal(first)
							}
							wantBuilds := int32(0)
							if mode == "missing_native" {
								wantBuilds = 1
								if err := os.Rename(provider+".held", provider); err != nil {
									t.Fatal(err)
								}
							} else {
								install()
							}
							if err := owner.WithSymbol(func(*dylib.Symbol) error { return nil }); err != first || builds.Load() != wantBuilds {
								t.Fatal("failed selection/load replayed", first, err, builds.Load())
							}
							fresh, err := example.LazySymbol(context.Background(), req, dylib.Options{}, appDir, providerDir)
							if err != nil {
								t.Fatal(err)
							}
							defer fresh.Close()
							if err := callLazySymbol(fresh, 20, 20); err != nil {
								t.Fatal("fresh owner did not recover", err)
							}
							return
						}
						if mode == "function" {
							const n = 8
							results := make(chan error, n)
							for i := 0; i < n; i++ {
								go func(delta int32) { results <- callLazySymbol(owner, 20+delta, 20) }(int32(i))
							}
							for range n {
								select {
								case err := <-results:
									if err != nil {
										t.Fatal(err)
									}
								case <-time.After(5 * time.Second):
									t.Fatal("concurrent symbol uses blocked")
								}
							}
							cancel() // A successful owner remains usable after initialization's context ends.
							if err := owner.WithSymbol(func(*dylib.Symbol) error { return callLazySymbol(owner, 20, 20) }); err != nil {
								t.Fatal("loaded symbol reentry/context lifetime", err)
							}
						}
						var lease *dylib.SymbolLease
						if err := owner.WithSymbol(func(symbol *dylib.Symbol) error { var err error; lease, err = symbol.Acquire(); return err }); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { lease.Close() })
						if builds.Load() != 1 {
							t.Fatal("concurrent first use replayed builder", builds.Load())
						}
						retireLazySymbol(t, owner, lease, mode == "function")
					})
				}
			})
		}
	}
}

func TestNativePerSymbolLazyCompilation(t *testing.T) {
	llc := os.Getenv("DYLIB_LLC")
	if llc == "" {
		llc = "llc"
	}
	llc = nativeTool(t, llc)
	for _, kind := range []string{"ir", "bitcode"} {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(kind+"/"+opt, func(t *testing.T) {
				dir, storage := t.TempDir(), t.TempDir()
				provider := produceModuleIR(t, dir, "provider", `API int module_bias=2; API int module_add(int a,int b){return a+b;}`, kind, opt)
				consumer := produceModuleIR(t, dir, "consumer", consumerCode, kind, opt)
				var compiles atomic.Int32
				owner, err := example.LazyCompiledSymbol(context.Background(), module.SymbolRequirement{Symbol: "module_run", Version: "app/1"}, dylib.Options{}, func(ctx context.Context, path string) (module.InputArtifact, error) {
					compiles.Add(1)
					object, err := llvm.Compile(ctx, path, llvm.Options{Compiler: llc, TempDir: storage})
					if object == nil {
						return module.InputArtifact{}, err
					}
					return module.InputArtifact{Path: object.Path, Owner: object}, err
				}, dir)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { owner.Close() })
				if compiles.Load() != 0 || compiledStorageEntries(t, storage) != 0 {
					t.Fatal("construction compiled inputs")
				}
				// Catalogs are installed only after construction; first use selects them.
				writeCatalog(t, dir, "selected", example.Manifest(filepath.Base(provider), filepath.Base(consumer)).Modules...)
				for range 3 {
					if err := callLazySymbol(owner, 20, 20); err != nil {
						t.Fatal(err)
					}
				}
				if compiles.Load() != 2 || compiledStorageEntries(t, storage) != 2 {
					t.Fatal("selection/compilation did not run once", compiles.Load())
				}
				if err := owner.Close(); err != nil || compiledStorageEntries(t, storage) != 0 {
					t.Fatal("compiled symbol cleanup", err)
				}
			})
		}
	}
}
