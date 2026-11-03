//go:build cgo && (linux || darwin || windows)

package module_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	dylib "github.com/ramblelabs/dylib-go"
	call "github.com/ramblelabs/dylib-go/examples/call"
	example "github.com/ramblelabs/dylib-go/examples/modules"
	"github.com/ramblelabs/dylib-go/module"
)

func readLazyEntry(graph *module.Graph, a, b int32) error {
	session, err := graph.Session("app")
	if err != nil {
		return err
	}
	symbol, err := session.Resolve("module_run")
	if err != nil {
		return err
	}
	lease, err := symbol.Acquire()
	if err != nil {
		return err
	}
	defer lease.Close()
	return lease.WithAddress(func(address uintptr) error {
		got, err := call.Int32(address, a, b)
		if err == nil && got != a+b+2 {
			return fmt.Errorf("lazy call returned %d; want %d", got, a+b+2)
		}
		return err
	})
}

func TestNativeLazySymbolGraphs(t *testing.T) {
	for _, kind := range []string{"object", "archive", "library"} {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(kind+"/"+opt, func(t *testing.T) {
				providerDir, appDir := t.TempDir(), t.TempDir()
				provider := produce(t, providerDir, "provider", `static int inits; API int module_bias; __attribute__((constructor)) static void start(void){++inits;module_bias=2;} API int lazy_inits(int a,int b){return inits;} API int module_add(int a,int b){return a+b;}`, kind, opt)
				consumer := produce(t, appDir, "consumer", consumerCode, "object", opt)
				mods := example.Manifest(filepath.Base(provider), filepath.Base(consumer)).Modules
				mods[1].Exports = append(mods[1].Exports, "lazy_inits")
				writeCatalog(t, appDir, "app", mods[0])
				writeCatalog(t, providerDir, "math", mods[1])
				for _, mode := range []string{"concurrent_calls_and_leases", "sticky_failure", "unused_close"} {
					t.Run(mode, func(t *testing.T) {
						backup := provider + ".held"
						if mode != "concurrent_calls_and_leases" {
							if err := os.Rename(provider, backup); err != nil {
								t.Fatal(err)
							}
							defer func() {
								if _, err := os.Stat(backup); err == nil {
									if err := os.Rename(backup, provider); err != nil {
										t.Error(err)
									}
								}
							}()
						}
						owner, err := example.LazySymbols(context.Background(), dylib.Options{}, appDir, providerDir)
						if err != nil {
							t.Fatal("creating lazy owner read native inputs", err)
						}
						t.Cleanup(func() {
							if err := owner.Close(); err != nil {
								t.Error(err)
							}
						})
						if mode == "unused_close" {
							if err := owner.Close(); err != nil {
								t.Fatal(err)
							}
							return
						}
						if mode == "sticky_failure" {
							if err := owner.WithGraph(func(*module.Graph) error { t.Error("missing input admitted scope"); return nil }); !errors.Is(err, os.ErrNotExist) {
								t.Fatal(err)
							}
							if err := os.Rename(backup, provider); err != nil {
								t.Fatal(err)
							}
							if err := owner.WithGraph(func(*module.Graph) error { t.Error("failed initialization implicitly retried"); return nil }); !errors.Is(err, os.ErrNotExist) {
								t.Fatal("native failure was not sticky", err)
							}
							if err := owner.Close(); err != nil {
								t.Fatal(err)
							}
							fresh, err := example.LazySymbols(context.Background(), dylib.Options{}, appDir, providerDir)
							if err != nil {
								t.Fatal(err)
							}
							defer fresh.Close()
							if err := fresh.WithGraph(func(graph *module.Graph) error { return readLazyEntry(graph, 20, 20) }); err != nil {
								t.Fatal("new explicit owner could not initialize", err)
							}
							return
						}
						const n = 8
						results := make(chan error, n)
						var identity *module.Graph
						var identityMu sync.Mutex
						for i := 0; i < n; i++ {
							go func(delta int32) {
								results <- owner.WithGraph(func(graph *module.Graph) error {
									identityMu.Lock()
									if identity == nil {
										identity = graph
									}
									same := identity == graph
									identityMu.Unlock()
									if !same {
										return fmt.Errorf("concurrent first uses selected independent graphs")
									}
									return readLazyEntry(graph, 20+delta, 20)
								})
							}(int32(i))
						}
						for range n {
							select {
							case err := <-results:
								if err != nil {
									t.Fatal(err)
								}
							case <-time.After(5 * time.Second):
								t.Fatal("native first uses did not finish")
							}
						}
						if err := owner.WithGraph(func(graph *module.Graph) error {
							if err := owner.WithGraph(func(nested *module.Graph) error {
								if nested != graph {
									return fmt.Errorf("nested use changed graph")
								}
								return readLazyEntry(nested, 20, 20)
							}); err != nil {
								return err
							}
							session, err := graph.Session("math")
							if err != nil {
								return err
							}
							lease, read := nativeRead(t, session, "lazy_inits")
							defer lease.Close()
							if got := read(0, 0); got != 1 {
								return fmt.Errorf("native initialization count %d", got)
							}
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						var code, data *dylib.SymbolLease
						if err := owner.WithGraph(func(graph *module.Graph) error {
							app, err := graph.Session("app")
							if err != nil {
								return err
							}
							symbol, err := app.Resolve("module_run")
							if err != nil {
								return err
							}
							code, err = symbol.Acquire()
							if err != nil {
								return err
							}
							math, err := graph.Session("math")
							if err != nil {
								return err
							}
							symbol, err = math.Resolve("module_bias")
							if err != nil {
								return err
							}
							data, err = symbol.Acquire()
							return err
						}); err != nil {
							if code != nil {
								code.Close()
							}
							t.Fatal(err)
						}
						t.Cleanup(func() { code.Close(); data.Close() })
						closed := make(chan error, 2)
						go func() { closed <- owner.Close() }()
						go func() { closed <- owner.Close() }()
						deadline := time.Now().Add(5 * time.Second)
						for {
							if err := owner.WithGraph(func(*module.Graph) error { return nil }); errors.Is(err, dylib.ErrClosed) {
								break
							}
							if time.Now().After(deadline) {
								t.Fatal("lazy graph did not retire admission")
							}
							time.Sleep(time.Millisecond)
						}
						if err := code.WithAddress(func(address uintptr) error {
							got, err := call.Int32(address, 20, 20)
							if err == nil && got != 42 {
								return fmt.Errorf("retired lazy call %d", got)
							}
							return err
						}); err != nil {
							t.Fatal(err)
						}
						if err := data.WithAddress(func(address uintptr) error {
							if address == 0 {
								return fmt.Errorf("retired lazy data address lost")
							}
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						select {
						case err := <-closed:
							t.Fatal("lazy close crossed external leases", err)
						default:
						}
						code.Close()
						data.Close()
						for range 2 {
							select {
							case err := <-closed:
								if err != nil {
									t.Fatal(err)
								}
							case <-time.After(5 * time.Second):
								t.Fatal("lazy native cleanup did not finish")
							}
						}
					})
				}
			})
		}
	}
}
