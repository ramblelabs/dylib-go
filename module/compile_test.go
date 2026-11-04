package module

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	dylib "github.com/ramblelabs/dylib-go"
)

func compilationManifest() Manifest {
	return Manifest{Format: FormatVersion, Roots: []string{"app"}, Modules: []Module{
		{Name: "app", Version: "1", Files: []string{"app.o", "second.o"}, Exports: []string{"run"}, Imports: []Import{{Provider: "math", Version: "1", Symbol: "add"}}},
		{Name: "math", Version: "1", Files: []string{"math.o"}, Exports: []string{"add"}},
		{Name: "unused", Version: "1", Files: []string{"absent.o"}},
	}}
}

func TestCompiledGraphValidatesBeforeCallbacks(t *testing.T) {
	for _, mode := range []string{"nil_context", "nil_compiler", "canceled", "invalid_unused"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			manifest := compilationManifest()
			calls := 0
			var compile InputCompiler = func(context.Context, string) (InputArtifact, error) { calls++; return InputArtifact{}, nil }
			switch mode {
			case "nil_context":
				ctx = nil
			case "nil_compiler":
				compile = nil
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			case "invalid_unused":
				manifest.Modules[2].Version = ""
			}
			g, err := OpenCompiled(ctx, t.TempDir(), manifest, dylib.Options{}, compile)
			if g != nil || err == nil || calls != 0 || mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal(g, err, calls)
			}
		})
	}
}

func TestCompiledGraphPreparationOrder(t *testing.T) {
	base := t.TempDir()
	manifest := compilationManifest()
	before := compilationManifest()
	failure := errors.New("compiler stopped")
	var files, closed []string
	g, err := OpenCompiled(context.Background(), base, manifest, dylib.Options{}, func(ctx context.Context, path string) (InputArtifact, error) {
		if !filepath.IsAbs(path) || ctx.Err() != nil {
			t.Fatal("compiler received invalid context/path", path, ctx.Err())
		}
		name := filepath.Base(path)
		files = append(files, name)
		artifact := InputArtifact{Path: path, Owner: closeFunc(func() error { closed = append(closed, name); return nil })}
		if name == "second.o" {
			return artifact, failure
		}
		return artifact, nil
	})
	if g != nil || !errors.Is(err, failure) || errors.Is(err, os.ErrNotExist) {
		t.Fatal("opened native input before preparation completed", g, err)
	}
	if !reflect.DeepEqual(files, []string{"math.o", "app.o", "second.o"}) || !reflect.DeepEqual(closed, []string{"second.o", "app.o", "math.o"}) {
		t.Fatal(files, closed)
	}
	if !reflect.DeepEqual(manifest, before) {
		t.Fatal("preparation mutated the caller's manifest", manifest)
	}
}

func TestCompiledGraphResultContracts(t *testing.T) {
	for _, mode := range []string{"empty", "relative", "compiler_error"} {
		t.Run(mode, func(t *testing.T) {
			closes := 0
			failure := errors.New("compiler failure")
			g, err := OpenCompiled(context.Background(), t.TempDir(), compilationManifest(), dylib.Options{}, func(context.Context, string) (InputArtifact, error) {
				artifact := InputArtifact{Owner: closeFunc(func() error { closes++; return nil })}
				if mode == "relative" {
					artifact.Path = "compiled.o"
				}
				if mode == "compiler_error" {
					return artifact, failure
				}
				return artifact, nil
			})
			if g != nil || err == nil || closes != 1 || mode == "compiler_error" && !errors.Is(err, failure) {
				t.Fatal(g, err, closes)
			}
		})
	}
}

func TestCompiledGraphCleanupRetry(t *testing.T) {
	for _, phase := range []string{"compile", "load", "cancel"} {
		t.Run(phase, func(t *testing.T) {
			base := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			compileFailure, cleanupFailure := errors.New("compiler failed"), errors.New("artifact busy")
			failCleanup := true
			var closed []string
			g, err := OpenCompiled(ctx, base, compilationManifest(), dylib.Options{}, func(_ context.Context, path string) (InputArtifact, error) {
				name := filepath.Base(path)
				artifact := InputArtifact{Path: path, Owner: closeFunc(func() error {
					closed = append(closed, name)
					if name == "app.o" && failCleanup {
						return cleanupFailure
					}
					return nil
				})}
				if name == "second.o" {
					if phase == "cancel" {
						cancel()
					} else if phase == "compile" {
						return artifact, compileFailure
					}
				}
				return artifact, nil
			})
			want := compileFailure
			if phase == "load" {
				want = os.ErrNotExist
			} else if phase == "cancel" {
				want = context.Canceled
			}
			if g == nil || !errors.Is(err, want) || !errors.Is(err, cleanupFailure) {
				t.Fatal("lost failure or cleanup owner", g, err)
			}
			if _, err := g.Session("app"); !errors.Is(err, dylib.ErrClosed) {
				t.Fatal("failed owner admitted native use", err)
			}
			if !reflect.DeepEqual(closed, []string{"second.o", "app.o"}) {
				t.Fatal(closed)
			}
			failCleanup = false
			if err := g.Close(); err != nil {
				t.Fatal(err)
			}
			if err := g.Close(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(closed, []string{"second.o", "app.o", "app.o", "math.o"}) {
				t.Fatal("cleanup replayed completed artifacts", closed)
			}
		})
	}
}

func TestCompiledGraphSnapshotsSelectedRecords(t *testing.T) {
	manifest := compilationManifest()
	var seen []string
	g, err := OpenCompiled(context.Background(), t.TempDir(), manifest, dylib.Options{}, func(_ context.Context, path string) (InputArtifact, error) {
		seen = append(seen, filepath.Base(path))
		if len(seen) == 1 {
			manifest.Roots[0] = "changed"
			for i := range manifest.Modules {
				manifest.Modules[i].Name = "changed"
				manifest.Modules[i].Files[0] = "changed"
				if len(manifest.Modules[i].Exports) != 0 {
					manifest.Modules[i].Exports[0] = "changed"
				}
				if len(manifest.Modules[i].Imports) != 0 {
					manifest.Modules[i].Imports[0].Provider = "changed"
				}
			}
		}
		return InputArtifact{Path: path}, nil
	})
	if g != nil || !errors.Is(err, os.ErrNotExist) || !reflect.DeepEqual(seen, []string{"math.o", "app.o", "second.o"}) {
		t.Fatal("callbacks changed the selected plan", g, err, seen)
	}
}

func TestCompiledGraphCancellationStopsPreparation(t *testing.T) {
	for _, stop := range []int{1, 3} {
		t.Run(fmt.Sprint(stop), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, closed := 0, 0
			g, err := OpenCompiled(ctx, t.TempDir(), compilationManifest(), dylib.Options{}, func(_ context.Context, path string) (InputArtifact, error) {
				calls++
				if calls == stop {
					cancel()
				}
				return InputArtifact{Path: path, Owner: closeFunc(func() error { closed++; return nil })}, nil
			})
			if g != nil || !errors.Is(err, context.Canceled) || calls != stop || closed != stop {
				t.Fatal(g, err, calls, closed)
			}
		})
	}
}

func TestCompiledGraphCompilerAbortCleanup(t *testing.T) {
	for _, mode := range []string{"panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			closed := make(chan string, 1)
			finished := make(chan error, 1)
			base := t.TempDir()
			go func() {
				defer func() {
					p := recover()
					if mode == "panic" && p != "compiler abort" || mode == "goexit" && p != nil {
						finished <- fmt.Errorf("compiler abort changed: %v", p)
						return
					}
					finished <- nil
				}()
				OpenCompiled(context.Background(), base, compilationManifest(), dylib.Options{}, func(_ context.Context, path string) (InputArtifact, error) {
					if filepath.Base(path) == "app.o" {
						if mode == "goexit" {
							runtime.Goexit()
						}
						panic("compiler abort")
					}
					return InputArtifact{Path: path, Owner: closeFunc(func() error { closed <- filepath.Base(path); return nil })}, nil
				})
				t.Error("aborted compiler returned normally")
			}()
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("compiler abort did not unwind")
			}
			select {
			case name := <-closed:
				if name != "math.o" {
					t.Fatal(name)
				}
			default:
				t.Fatal("compiler abort leaked an earlier artifact")
			}
		})
	}
}
