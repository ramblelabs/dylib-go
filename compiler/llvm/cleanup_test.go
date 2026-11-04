package llvm

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/module"
)

func TestCompilerFailureRetainsCleanupOwner(t *testing.T) {
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		t.Skip("directory permission denial requires a non-root user")
	}
	for _, kind := range []string{"object", "archive", "modules", "module_graph"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			storage := filepath.Join(dir, "storage")
			if err := os.Mkdir(storage, 0700); err != nil {
				t.Fatal(err)
			}
			input := writeIR(t, dir, "x86_64-unknown-linux-gnu")
			if kind == "archive" {
				input = filepath.Join(dir, "input.a")
				ordinaryArchive(t, input, []string{"part.bc"}, [][]byte{{'B', 'C', 0xc0, 0xde, 1}})
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			marker, release := filepath.Join(dir, "marker"), filepath.Join(dir, "release")
			t.Setenv("DYLIB_LLVM_COMPILER_HELPER", "cleanup-lock")
			t.Setenv("DYLIB_LLVM_CLEANUP_MARKER", marker)
			t.Setenv("DYLIB_LLVM_CLEANUP_RELEASE", release)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			done := make(chan struct{})
			var owner io.Closer
			var failure error
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("compiler subprocess did not stop")
					return
				}
				if owner != nil {
					if err := owner.Close(); err != nil {
						t.Error(err)
					}
				}
			})
			go func() {
				defer close(done)
				opts := Options{Compiler: executable, TempDir: storage}
				switch kind {
				case "object":
					object, err := Compile(ctx, input, opts)
					failure = err
					if object != nil {
						owner = object
					}
				case "archive":
					archive, err := CompileArchive(ctx, input, opts)
					failure = err
					if archive != nil {
						owner = archive
					}
				case "modules":
					object, err := CompileModules(ctx, []string{input}, MergeOptions{Options: opts, Linker: executable})
					failure = err
					if object != nil {
						owner = object
					}
				case "module_graph":
					manifest := module.Manifest{Format: module.FormatVersion, Roots: []string{"app"}, Modules: []module.Module{{Name: "app", Version: "1", Files: []string{input}, Exports: []string{"add"}}}}
					graph, err := module.OpenCompiled(ctx, dir, manifest, dylib.Options{}, func(ctx context.Context, path string) (module.InputArtifact, error) {
						object, err := Compile(ctx, path, opts)
						if object == nil {
							return module.InputArtifact{}, err
						}
						return module.InputArtifact{Path: object.Path, Owner: object}, err
					})
					failure = err
					if graph != nil {
						owner = graph
					}
				}
			}()
			var blocked string
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if data, err := os.ReadFile(marker); err == nil {
					blocked = string(data)
					break
				}
				select {
				case <-done:
					t.Fatalf("compiler ended before cleanup lock: %v", failure)
				default:
				}
				time.Sleep(10 * time.Millisecond)
			}
			if blocked == "" {
				t.Fatal("compiler did not publish its owned storage")
			}
			var unlock func() error
			if runtime.GOOS == "windows" {
				// Ordinary Go file handles share reads/writes, but not deletion.
				file, err := os.Open(filepath.Join(blocked, "locked"))
				if err != nil {
					t.Fatal(err)
				}
				unlock = file.Close
			} else {
				if err := os.Chmod(blocked, 0); err != nil {
					t.Fatal(err)
				}
				unlock = func() error { return os.Chmod(blocked, 0700) }
			}
			defer func() {
				if unlock != nil {
					if err := unlock(); err != nil {
						t.Error(err)
					}
				}
			}()
			if err := os.WriteFile(release, nil, 0600); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("compiler failure did not finish")
			}
			var exit *exec.ExitError
			var cleanup *os.PathError
			if !errors.As(failure, &exit) || !errors.As(failure, &cleanup) || owner == nil {
				t.Fatal("lost compiler failure or cleanup owner", owner, failure)
			}
			if err := unlock(); err != nil {
				t.Fatal(err)
			}
			unlock = nil
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(storage)
			if err != nil || len(entries) != 0 {
				t.Fatal("retained compiler files leaked", entries, err)
			}
		})
	}
}
