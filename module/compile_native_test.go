//go:build cgo && (linux || darwin || windows)

package module_test

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/compiler/llvm"
	example "github.com/ramblelabs/dylib-go/examples/modules"
	"github.com/ramblelabs/dylib-go/module"
)

func produceModuleIR(t *testing.T, dir, name, code, kind, opt string) string {
	t.Helper()
	clang := os.Getenv("DYLIB_LLVM_CLANG")
	if clang == "" {
		clang = os.Getenv("CLANG")
	}
	if clang == "" {
		clang = "clang"
	}
	source := filepath.Join(dir, name+".c")
	if err := os.WriteFile(source, []byte(exportMacro+code), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, name+".bc")
	args := []string{"-c", "-emit-llvm", opt, "-fno-stack-protector"}
	if kind == "ir" {
		output = filepath.Join(dir, name+".ll")
		args = append(args, "-S")
	}
	if runtime.GOARCH == "386" {
		args = append(args, "-m32")
	}
	if runtime.GOOS == "windows" {
		arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH]
		args = append(args, "--target="+arch+"-pc-windows-msvc", "-ffreestanding")
	} else {
		args = append(args, "-fPIC")
	}
	args = append(args, source, "-o", output)
	if data, err := exec.Command(nativeTool(t, clang), args...).CombinedOutput(); err != nil {
		t.Fatalf("module IR producer: %v\n%s", err, data)
	}
	return output
}

type compiledDirectory struct {
	path   string
	closes *atomic.Int32
}

func (d compiledDirectory) Close() error {
	d.closes.Add(1)
	return os.RemoveAll(d.path)
}

func TestNativeCompiledLibraryOwners(t *testing.T) {
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			dir, storage := t.TempDir(), t.TempDir()
			provider := produce(t, dir, "provider", `API int module_bias=2; API int module_add(int a,int b){return a+b;}`, "library", opt)
			consumer := produce(t, dir, "consumer", consumerCode, "object", opt)
			manifest := example.Manifest(provider, consumer)
			var closes atomic.Int32
			graph, err := module.OpenCompiled(context.Background(), dir, manifest, dylib.Options{}, func(_ context.Context, path string) (module.InputArtifact, error) {
				folder, err := os.MkdirTemp(storage, "prepared-")
				if err != nil {
					return module.InputArtifact{}, err
				}
				artifact := module.InputArtifact{Path: filepath.Join(folder, filepath.Base(path)), Owner: compiledDirectory{folder, &closes}}
				data, err := os.ReadFile(path)
				if err == nil {
					err = os.WriteFile(artifact.Path, data, 0600)
				}
				return artifact, err
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := graph.Close(); err != nil {
					t.Error(err)
				}
			})
			session, err := graph.Session("app")
			if err != nil {
				t.Fatal(err)
			}
			lease, read := nativeRead(t, session, "module_run")
			if got := read(20, 20); got != 42 {
				t.Fatal(got)
			}
			done := make(chan error, 1)
			go func() { done <- graph.Close() }()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := graph.Session("app"); errors.Is(err, dylib.ErrClosed) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("compiled library graph did not retire")
				}
				runtime.Gosched()
			}
			if got := read(20, 20); got != 42 || closes.Load() != 0 || compiledStorageEntries(t, storage) != 2 {
				t.Fatal("library artifacts crossed a native lease", got, closes.Load())
			}
			lease.Close()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal("delete library only after unloading its native handle", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("compiled library cleanup did not finish")
			}
			if err := graph.Close(); err != nil || closes.Load() != 2 || compiledStorageEntries(t, storage) != 0 {
				t.Fatal("library cleanup repeated or leaked", err, closes.Load())
			}
		})
	}
}

type compiledArtifactCloser struct {
	io.Closer
	fail     *bool
	busy     error
	attempts int
}

func (c *compiledArtifactCloser) Close() error {
	c.attempts++
	if c.fail != nil && *c.fail {
		return c.busy
	}
	return c.Closer.Close()
}

func compiledStorageEntries(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestNativeCompiledModuleGraphs(t *testing.T) {
	llc := os.Getenv("DYLIB_LLC")
	if llc == "" {
		llc = "llc"
	}
	llc = nativeTool(t, llc)
	for _, kind := range []string{"ir", "bitcode", "archive"} {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(kind+"/"+opt, func(t *testing.T) {
				dir := t.TempDir()
				provider := produceModuleIR(t, dir, "provider", `API int module_bias; __attribute__((constructor)) static void start(void){module_bias=2;} API int module_add(int a,int b){return a+b;}`, kind, opt)
				consumer := produceModuleIR(t, dir, "consumer", consumerCode, kind, opt)
				if kind == "archive" {
					unused := produce(t, dir, "unused", `extern int absent(int,int); int unused(int a,int b){return absent(a,b);}`, "object", opt)
					archive := filepath.Join(dir, "provider.a")
					ar := os.Getenv("DYLIB_LLVM_AR")
					if ar == "" {
						ar = filepath.Join(filepath.Dir(llc), "llvm-ar")
					}
					if data, err := exec.Command(nativeTool(t, ar), "rc", archive, provider, unused).CombinedOutput(); err != nil {
						t.Fatalf("module bitcode archive: %v\n%s", err, data)
					}
					provider = archive
				}
				for _, mode := range []string{"leases", "load_failure", "cleanup_retry"} {
					t.Run(mode, func(t *testing.T) {
						storage := t.TempDir()
						compiler := llvm.Options{Compiler: llc, TempDir: storage}
						manifest := example.Manifest(filepath.Base(provider), filepath.Base(consumer))
						manifest.Modules = append(manifest.Modules, module.Module{Name: "unused", Version: "1", Files: []string{"not-installed.bc"}})
						if mode == "load_failure" {
							manifest.Modules[0].Exports = []string{"not_exported"}
						}
						fail := mode == "cleanup_retry"
						busy := errors.New("compiled artifact busy")
						var wrappers []*compiledArtifactCloser
						compile := func(ctx context.Context, path string) (module.InputArtifact, error) {
							var artifact module.InputArtifact
							var err error
							if filepath.Ext(path) == ".a" {
								var archive *llvm.Archive
								archive, err = llvm.CompileArchive(ctx, path, compiler)
								if archive != nil {
									artifact = module.InputArtifact{Path: archive.Path, Owner: archive}
								}
							} else {
								var object *llvm.Object
								object, err = llvm.Compile(ctx, path, compiler)
								if object != nil {
									artifact = module.InputArtifact{Path: object.Path, Owner: object}
								}
							}
							if mode == "cleanup_retry" && artifact.Owner != nil {
								wrapper := &compiledArtifactCloser{Closer: artifact.Owner, busy: busy}
								if len(wrappers) == 0 {
									wrapper.fail = &fail
								}
								wrappers = append(wrappers, wrapper)
								artifact.Owner = wrapper
							}
							return artifact, err
						}
						var graph *module.Graph
						var err error
						if mode == "leases" && kind != "archive" {
							graph, err = example.OpenLLVM(context.Background(), dir, manifest, dylib.Options{}, compiler)
						} else {
							graph, err = module.OpenCompiled(context.Background(), dir, manifest, dylib.Options{}, compile)
						}
						if mode == "load_failure" {
							if graph != nil || err == nil || compiledStorageEntries(t, storage) != 0 {
								t.Fatal("native failure leaked compiled inputs", graph, err)
							}
							return
						}
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() {
							fail = false
							if err := graph.Close(); err != nil {
								t.Error(err)
							}
						})
						if compiledStorageEntries(t, storage) != 2 {
							t.Fatal("compiled storage not retained", compiledStorageEntries(t, storage))
						}
						session, err := graph.Session("app")
						if err != nil {
							t.Fatal(err)
						}
						lease, read := nativeRead(t, session, "module_run")
						if got := read(20, 20); got != 42 {
							t.Fatal(got)
						}
						if mode == "cleanup_retry" {
							lease.Close()
							if err := graph.Close(); !errors.Is(err, busy) || compiledStorageEntries(t, storage) != 1 {
								t.Fatal("failed artifact cleanup lost ownership", err)
							}
							if _, err := graph.Session("app"); !errors.Is(err, dylib.ErrClosed) {
								t.Fatal(err)
							}
							fail = false
							if err := graph.Close(); err != nil || wrappers[0].attempts != 2 || wrappers[1].attempts != 1 {
								t.Fatal(err, wrappers[0].attempts, wrappers[1].attempts)
							}
						} else {
							done := make(chan error, 1)
							go func() { done <- graph.Close() }()
							deadline := time.Now().Add(5 * time.Second)
							for {
								if _, err := graph.Session("app"); errors.Is(err, dylib.ErrClosed) {
									break
								}
								if time.Now().After(deadline) {
									t.Fatal("compiled graph did not retire")
								}
								runtime.Gosched()
							}
							if got := read(20, 20); got != 42 || compiledStorageEntries(t, storage) != 2 {
								t.Fatal("retirement lost code or compiler inputs", got)
							}
							lease.Close()
							select {
							case err := <-done:
								if err != nil {
									t.Fatal(err)
								}
							case <-time.After(5 * time.Second):
								t.Fatal("compiled cleanup did not finish")
							}
						}
						if n := compiledStorageEntries(t, storage); n != 0 {
							t.Fatalf("%d compiler directories leaked", n)
						}
					})
				}
			})
		}
	}
}
