package module_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/module"
)

func lazySymbolModule() module.Module {
	return module.Module{Name: "provider", Version: "1", Files: []string{"not-installed.o"}, Exports: []string{"entry"}}
}

func TestLazySymbolArguments(t *testing.T) {
	for _, mode := range []string{"nil_context", "canceled", "nil_builder", "no_directories", "too_many_directories", "empty_directory", "nul_directory", "empty_symbol", "nul_symbol", "empty_version", "bad_version", "bad_provider"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			dirs := []string{t.TempDir()}
			req := module.SymbolRequirement{Symbol: "entry", Version: "1"}
			var build module.SymbolGraphBuilder = func(context.Context, module.SymbolSelection) (*module.Graph, error) {
				t.Fatal("constructor invoked builder")
				return nil, nil
			}
			switch mode {
			case "nil_context":
				ctx = nil
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "nil_builder":
				build = nil
			case "no_directories":
				dirs = nil
			case "too_many_directories":
				dirs = make([]string, 17)
			case "empty_directory":
				dirs[0] = ""
			case "nul_directory":
				dirs[0] = "bad\x00dir"
			case "empty_symbol":
				req.Symbol = ""
			case "nul_symbol":
				req.Symbol = "bad\x00symbol"
			case "empty_version":
				req.Version = ""
			case "bad_version":
				req.Version = " 1"
			case "bad_provider":
				req.Provider = " provider"
			}
			owner, err := module.NewLazySymbol(ctx, req, build, dirs...)
			if owner != nil || err == nil || mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal(owner, err)
			}
		})
	}
}

func TestLazySymbolUnusedOwner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog-not-installed")
	owner, err := module.NewLazySymbol(context.Background(), module.SymbolRequirement{Symbol: "entry", Version: "1"}, func(context.Context, module.SymbolSelection) (*module.Graph, error) {
		t.Fatal("unused owner invoked builder")
		return nil, nil
	}, dir)
	if err != nil {
		t.Fatal("constructor read catalogs", err)
	}
	if err := owner.WithSymbol(nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.WithSymbol(func(*dylib.Symbol) error { t.Fatal("closed owner admitted use"); return nil }); !errors.Is(err, dylib.ErrClosed) {
		t.Fatal(err)
	}
}

func TestLazySymbolFirstUseSelection(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "installed-later")
	directories := []string{dir}
	req := module.SymbolRequirement{Symbol: "entry", Version: "1"}
	failure := errors.New("explicit builder stopped")
	calls := 0
	owner, err := module.NewLazySymbol(context.Background(), req, func(ctx context.Context, selected module.SymbolSelection) (*module.Graph, error) {
		calls++
		if ctx.Err() != nil || len(selected.Symbols) != 1 || selected.Symbols[0].Provider != "provider" || !reflect.DeepEqual(selected.Manifest.Roots, []string{"provider"}) || len(selected.Manifest.Modules) != 1 || selected.Manifest.Modules[0].Files[0] != filepath.Join(dir, "not-installed.o") {
			t.Error("wrong selected plan", selected, ctx.Err())
		}
		return nil, failure
	}, directories...)
	if err != nil || calls != 0 {
		t.Fatal(owner, err, calls)
	}
	defer owner.Close()
	directories[0], req.Symbol, req.Version = "changed", "changed", "changed"
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	writeCatalog(t, dir, "provider", lazySymbolModule(), module.Module{Name: "unused", Version: "1", Files: []string{"missing.o"}, Exports: []string{"unused"}})
	for range 3 {
		if err := owner.WithSymbol(func(*dylib.Symbol) error { t.Error("builder error admitted scope"); return nil }); !errors.Is(err, failure) {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("builder replayed", calls)
	}
}

func TestLazySymbolDiscoveryFailureIsSticky(t *testing.T) {
	for _, mode := range []string{"missing", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if mode == "ambiguous" {
				other := lazySymbolModule()
				other.Name = "other"
				writeCatalog(t, dir, "other", other)
			}
			if mode == "ambiguous" {
				writeCatalog(t, dir, "provider", lazySymbolModule())
			}
			calls := 0
			failure := errors.New("builder reached")
			build := func(context.Context, module.SymbolSelection) (*module.Graph, error) { calls++; return nil, failure }
			req := module.SymbolRequirement{Symbol: "entry", Version: "1"}
			owner, err := module.NewLazySymbol(context.Background(), req, build, dir)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			first := owner.WithSymbol(func(*dylib.Symbol) error { t.Error("invalid selection admitted use"); return nil })
			if first == nil || errors.Is(first, failure) || calls != 0 {
				t.Fatal(first, calls)
			}
			if mode == "ambiguous" {
				if err := os.Remove(filepath.Join(dir, "other.dylib.json")); err != nil {
					t.Fatal(err)
				}
			} else {
				writeCatalog(t, dir, "provider", lazySymbolModule())
			}
			if next := owner.WithSymbol(func(*dylib.Symbol) error { return nil }); next != first || calls != 0 {
				t.Fatal("selection failure retried", first, next, calls)
			}
			fresh, err := module.NewLazySymbol(context.Background(), req, build, dir)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			if err := fresh.WithSymbol(func(*dylib.Symbol) error { return nil }); !errors.Is(err, failure) || calls != 1 {
				t.Fatal("fresh owner did not select again", err, calls)
			}
		})
	}
}

func TestLazySymbolInvalidBuilderGraph(t *testing.T) {
	for _, mode := range []string{"nil", "closed"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			writeCatalog(t, dir, "provider", lazySymbolModule())
			calls := 0
			owner, err := module.NewLazySymbol(context.Background(), module.SymbolRequirement{Symbol: "entry", Version: "1"}, func(context.Context, module.SymbolSelection) (*module.Graph, error) {
				calls++
				if mode == "nil" {
					return nil, nil
				}
				graph := new(module.Graph)
				graph.Close()
				return graph, nil
			}, dir)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			for range 2 {
				if err := owner.WithSymbol(func(*dylib.Symbol) error { t.Error("invalid graph published"); return nil }); err == nil {
					t.Fatal("invalid graph accepted")
				}
			}
			if calls != 1 {
				t.Fatal(calls)
			}
		})
	}
}

func TestLazySymbolBuilderCancellation(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir, "provider", lazySymbolModule())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner, err := module.NewLazySymbol(ctx, module.SymbolRequirement{Symbol: "entry", Version: "1"}, func(context.Context, module.SymbolSelection) (*module.Graph, error) { cancel(); return nil, nil }, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := owner.WithSymbol(func(*dylib.Symbol) error { t.Error("canceled builder published"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type lazySymbolCloser func() error

func (c lazySymbolCloser) Close() error { return c() }

func TestLazySymbolFailedCandidateCleanupRetry(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir, "provider", lazySymbolModule())
	fail := true
	attempts, builds := 0, 0
	busy := errors.New("artifact busy")
	owner, err := module.NewLazySymbol(context.Background(), module.SymbolRequirement{Symbol: "entry", Version: "1"}, func(ctx context.Context, selected module.SymbolSelection) (*module.Graph, error) {
		builds++
		return module.OpenCompiled(ctx, "", selected.Manifest, dylib.Options{}, func(_ context.Context, path string) (module.InputArtifact, error) {
			return module.InputArtifact{Path: path, Owner: lazySymbolCloser(func() error {
				attempts++
				if fail {
					return busy
				}
				return nil
			})}, nil
		})
	}, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fail = false; owner.Close() })
	for range 2 {
		if err := owner.WithSymbol(func(*dylib.Symbol) error { t.Error("failed graph admitted use"); return nil }); !errors.Is(err, os.ErrNotExist) || !errors.Is(err, busy) {
			t.Fatal(err)
		}
	}
	if builds != 1 {
		t.Fatal(builds)
	}
	if err := owner.Close(); !errors.Is(err, busy) {
		t.Fatal(err)
	}
	before := attempts
	fail = false
	if err := owner.Close(); err != nil || attempts != before+1 {
		t.Fatal(err, attempts, before)
	}
	if err := owner.Close(); err != nil || attempts != before+1 {
		t.Fatal(err, attempts)
	}
}

func TestLazySymbolBuilderAbort(t *testing.T) {
	for _, mode := range []string{"panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			writeCatalog(t, dir, "provider", lazySymbolModule())
			calls := 0
			owner, err := module.NewLazySymbol(context.Background(), module.SymbolRequirement{Symbol: "entry", Version: "1"}, func(context.Context, module.SymbolSelection) (*module.Graph, error) {
				calls++
				if mode == "goexit" {
					runtime.Goexit()
				}
				panic("builder aborted")
			}, dir)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			done := make(chan any, 1)
			go func() {
				defer func() { done <- recover() }()
				owner.WithSymbol(func(*dylib.Symbol) error { return nil })
			}()
			select {
			case p := <-done:
				if mode == "panic" && p != "builder aborted" || mode == "goexit" && p != nil {
					t.Fatal(p)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("aborted builder blocked")
			}
			if err := owner.WithSymbol(func(*dylib.Symbol) error { t.Error("aborted owner admitted use"); return nil }); !errors.Is(err, module.ErrInitializationAborted) || calls != 1 {
				t.Fatal(err, calls)
			}
		})
	}
}

func TestLazySymbolNilAndZero(t *testing.T) {
	for _, owner := range []*module.LazySymbol{nil, new(module.LazySymbol)} {
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
		if err := owner.WithSymbol(func(*dylib.Symbol) error { t.Fatal("invalid owner admitted use"); return nil }); !errors.Is(err, dylib.ErrClosed) {
			t.Fatal(err)
		}
	}
}
