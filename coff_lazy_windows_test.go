//go:build windows

package dylib

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	examplecall "github.com/ramblelabs/dylib-go/examples/call"
	"github.com/ramblelabs/dylib-go/internal/native"
)

func TestNativeCOFFLazyImports(t *testing.T) {
	needNative(t)
	target := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH] + "-pc-windows-msvc"
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			dir, _ := delayProvider(t, target, opt)
			consumer := compile(t, "testdata/coffdelay/consumer.c", filepath.Join(dir, "consumer.obj"), "--target="+target, opt)
			dependencySource := filepath.Join(dir, "dependency.c")
			if err := os.WriteFile(dependencySource, []byte("int dependency(int a,int b){return a+b+1;}"), 0600); err != nil {
				t.Fatal(err)
			}
			dependency := compile(t, dependencySource, filepath.Join(dir, "dependency.obj"), "--target="+target, opt)
			parts, _ := delayFixtureParts(t, target, nativeDelayEntries())
			for _, kind := range []string{"archive", "objects", "cross_archives"} {
				t.Run(kind, func(t *testing.T) {
					var mu sync.Mutex
					starts, loads := 0, 0
					var problem error
					var s *Session
					hooks := DelayImportHooks{Notify: func(event DelayImportEvent, info DelayImportInfo) DelayImportResult {
						if event == DelayImportStartProcessing {
							busy := s.UnloadDelayImports()
							mu.Lock()
							starts++
							if !errors.Is(busy, ErrDelayImportsBusy) {
								problem = busy
								if problem == nil {
									problem = errors.New("hook unloaded its active native call")
								}
							}
							mu.Unlock()
						}
						if event == DelayImportBeforeLoadLibrary {
							mu.Lock()
							loads++
							mu.Unlock()
						}
						return DelayImportResult{}
					}}
					s = New(Options{DelayImports: DelayImportsLazy, LibraryPaths: []string{dir}, DelayHooks: hooks})
					defer s.Close()
					load(t, s, consumer, dependency)
					stage := t.TempDir()
					switch kind {
					case "objects":
						load(t, s, writeComponentObjects(t, stage, parts)...)
					case "archive":
						path := filepath.Join(stage, "delay.lib")
						if err := os.WriteFile(path, longImportArchive(parts), 0600); err != nil {
							t.Fatal(err)
						}
						load(t, s, path)
					case "cross_archives":
						for i, subset := range [][][]byte{parts[:1], parts[1:]} {
							path := filepath.Join(stage, []string{"head.lib", "entries.lib"}[i])
							if err := os.WriteFile(path, longImportArchive(subset), 0600); err != nil {
								t.Fatal(err)
							}
							load(t, s, path)
						}
					}
					if err := s.Link("delay_call"); err != nil {
						t.Fatal(err)
					}
					im := s.image
					if im.lazy == nil || len(s.libs) != 0 || starts != 0 || loads != 0 {
						t.Fatal("Link loaded the delayed provider", starts, loads, len(s.libs))
					}
					if runtime.GOARCH != "386" && (im.lazy.functions == nil || !im.lazy.functions.registered) {
						t.Fatal("internal resolver needs unwind registration without RegisterUnwind")
					}
					id := im.lazy.id
					publicCell, err := s.Lookup("__imp_add")
					if err != nil {
						t.Fatal(err)
					}
					publicEntry := *nativeWord(publicCell)
					for round := 1; round <= 2; round++ {
						call(t, s, "delay_call", 20, 22, 252)
						call(t, s, "delay_call", 20, 22, 252)
						mu.Lock()
						gotStarts, gotLoads, gotProblem := starts, loads, problem
						mu.Unlock()
						if gotStarts != 6*round || gotLoads != round || gotProblem != nil {
							t.Fatal("cached lazy resolution", gotStarts, gotLoads, gotProblem)
						}
						im.lazy.providers.mu.Lock()
						module := im.lazy.providers.slots[0].module.handle
						im.lazy.providers.mu.Unlock()
						counter := native.Lookup(module, "provider_calls")
						value, err := examplecall.Int32(counter, 0, 0)
						if err != nil || value != 4 {
							t.Fatal("provider load/reset state", value, err)
						}
						symbol, err := s.Resolve("delay_call")
						if err != nil {
							t.Fatal(err)
						}
						lease, err := symbol.Acquire()
						if err != nil {
							t.Fatal(err)
						}
						if err := s.UnloadDelayImports(); !errors.Is(err, ErrDelayImportsBusy) {
							t.Fatal("live lease must block unload", err)
						}
						lease.Close()
						if err := s.UnloadDelayImports(); err != nil {
							t.Fatal(err)
						}
						if *nativeWord(publicCell) != publicEntry {
							t.Fatal("public IAT alias changed on unload")
						}
					}
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					coffLazyRegistry.Lock()
					retained := coffLazyRegistry.images[id] != nil
					coffLazyRegistry.Unlock()
					if retained || im.mem != nil {
						t.Fatal("retired image kept its resolver registry/mapping")
					}
				})
			}
		})
	}
}

func TestNativeCOFFLazyFirstCallDuringClose(t *testing.T) {
	needNative(t)
	target := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH] + "-pc-windows-msvc"
	for _, opt := range []string{"-O0", "-O2"} {
		for _, keep := range []bool{false, true} {
			t.Run(opt+fmt.Sprintf("/keep_%v", keep), func(t *testing.T) {
				dir, dll := delayProvider(t, target, opt)
				source := filepath.Join(dir, "finalizer.c")
				code := `
__declspec(dllimport) int add(int,int);
extern int observed;
static void stop(void) { observed=add(20,22); }
__attribute__((used,section(".CRT$XTU"))) static void (*const fini)(void)=stop;
int entry(int a,int b) { return a+b; }
`
				if err := os.WriteFile(source, []byte(code), 0600); err != nil {
					t.Fatal(err)
				}
				consumer := compile(t, source, filepath.Join(dir, "finalizer.obj"), "--target="+target, opt)
				parts, _ := delayFixtureParts(t, target, []delayFixtureEntry{{"add", "add", 0}})
				storage, err := native.Alloc(4)
				if err != nil {
					t.Fatal(err)
				}
				defer native.Free(storage)
				var mu sync.Mutex
				var problem error
				starts := 0
				var s *Session
				hooks := DelayImportHooks{Notify: func(event DelayImportEvent, _ DelayImportInfo) DelayImportResult {
					if event == DelayImportStartProcessing {
						_, retired := s.Lookup("entry")
						mu.Lock()
						starts++
						if !errors.Is(retired, ErrClosed) {
							problem = errors.New("ordinary session lookup remained available during finalization")
						}
						mu.Unlock()
					}
					return DelayImportResult{}
				}}
				s = New(Options{DelayImports: DelayImportsLazy, LibraryPaths: []string{dir}, DelayHooks: hooks, KeepLibraries: keep})
				defer s.Close()
				if err := s.Define("observed", uintptr(unsafe.Pointer(&storage[0]))); err != nil {
					t.Fatal(err)
				}
				load(t, s, consumer)
				load(t, s, writeComponentObjects(t, t.TempDir(), parts)...)
				if err := s.Link("entry"); err != nil {
					t.Fatal(err)
				}
				lazy := s.image.lazy
				if lazy == nil || starts != 0 {
					t.Fatal("provider resolved before finalization", starts)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				gotStarts, gotProblem := starts, problem
				mu.Unlock()
				if le.Uint32(storage) != 42 || gotStarts != 1 || gotProblem != nil {
					t.Fatal("first resolution after session retirement", le.Uint32(storage), gotStarts, gotProblem)
				}
				module, err := native.Open(dll)
				if err != nil {
					t.Fatal(err)
				}
				defer native.Close(module)
				counter := native.Lookup(module, "provider_calls")
				if counter == 0 {
					t.Fatal("missing native provider counter")
				}
				value, err := examplecall.Int32(counter, 0, 0)
				want := int32(0)
				if keep {
					want = 1
					// This test can explicitly release the deliberately retained
					// reference after all native calls have stopped.
					lazy.providers.mu.Lock()
					retained := lazy.providers.slots[0].module.handle
					lazy.providers.mu.Unlock()
					defer native.Close(retained)
				}
				if err != nil || value != want {
					t.Fatal("provider release/KeepLibraries", value, want, err)
				}
			})
		}
	}
}

func TestNativeCOFFLazyConcurrentFirstCalls(t *testing.T) {
	needNative(t)
	target := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH] + "-pc-windows-msvc"
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			control := nativeInputSession(t, "testdata/cofflazy/probe.c", "library", opt)
			var bytes []byte
			for path := range control.paths {
				var err error
				bytes, err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "probe.dll"), bytes, 0600); err != nil {
				t.Fatal(err)
			}
			parts, _ := delayFixtureParts(t, target, []delayFixtureEntry{{"add", "scalar", 0}})
			var arrived atomic.Int32
			ready := make(chan struct{})
			var mu sync.Mutex
			var problem error
			var s *Session
			hook := func(event DelayImportEvent, _ DelayImportInfo) DelayImportResult {
				if event != DelayImportBeforeLoadLibrary {
					return DelayImportResult{}
				}
				storage := make([]byte, 64<<10)
				storage[0] = 42
				runtime.GC()
				runtime.KeepAlive(storage)
				if arrived.Add(1) == 8 {
					s.mu.Lock()
					providers := s.image.lazy.providers
					s.mu.Unlock()
					providers.mu.Lock()
					threads := make(map[uintptr]bool)
					for call := range providers.active {
						threads[call.thread] = true
					}
					providers.mu.Unlock()
					if len(threads) != 8 || threads[0] {
						mu.Lock()
						problem = fmt.Errorf("expected 8 actual native resolver threads, got %v", threads)
						mu.Unlock()
					}
					close(ready)
				}
				select {
				case <-ready:
				case <-time.After(30 * time.Second):
					mu.Lock()
					problem = errors.New("native first-call barrier timed out")
					mu.Unlock()
				}
				return DelayImportResult{}
			}
			s = New(Options{DelayImports: DelayImportsLazy, LibraryPaths: []string{dir}, DelayHooks: DelayImportHooks{Notify: hook}})
			defer s.Close()
			load(t, s, writeComponentObjects(t, t.TempDir(), parts)...)
			symbol, err := s.Resolve("add")
			if err != nil {
				t.Fatal(err)
			}
			lease, err := symbol.Acquire()
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			address, err := lease.Address()
			if err != nil {
				t.Fatal(err)
			}
			factory := probeNativeWord(t, control, "factory_entry")
			*factory = address
			defer func() { *factory = 0 }()
			call(t, control, "threads", 20, 0, 42)
			call(t, control, "threads", 20, 0, 42)
			mu.Lock()
			gotProblem := problem
			mu.Unlock()
			if gotProblem != nil || arrived.Load() != 8 {
				t.Fatal("concurrent first/cached calls", arrived.Load(), gotProblem)
			}
			lease.Close()
			if err := s.UnloadDelayImports(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeCOFFLazyFailureHooks(t *testing.T) {
	needNative(t)
	target := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH] + "-pc-windows-msvc"
	for _, opt := range []string{"-O0", "-O2"} {
		for _, failure := range []string{"module", "name", "ordinal"} {
			t.Run(opt+"/"+failure, func(t *testing.T) {
				dir, original := delayProvider(t, target, opt)
				libraryBytes, err := os.ReadFile(original)
				if err != nil {
					t.Fatal(err)
				}
				dll := filepath.Join(dir, "lzbad.dll")
				if err := os.WriteFile(dll, libraryBytes, 0600); err != nil {
					t.Fatal(err)
				}
				consumer := compile(t, "testdata/coffdelay/consumer.c", filepath.Join(dir, "consumer.obj"), "--target="+target, opt)
				dependencySource := filepath.Join(dir, "dependency.c")
				if err := os.WriteFile(dependencySource, []byte("int dependency(int a,int b){return a+b+1;}"), 0600); err != nil {
					t.Fatal(err)
				}
				dependency := compile(t, dependencySource, filepath.Join(dir, "dependency.obj"), "--target="+target, opt)
				entries := nativeDelayEntries()
				if failure == "name" {
					entries[0].name = "missing"
				} else if failure == "ordinal" {
					entries[1].ordinal = 65535
				}
				parts, _ := delayFixturePartsDLL(t, target, entries, "", "lzbad.dll")
				var mu sync.Mutex
				var problem error
				failures := 0
				hook := func(event DelayImportEvent, info DelayImportInfo) DelayImportResult {
					mu.Lock()
					failures++
					if info.DLL != "lzbad.dll" || info.Err == nil || info.NativeError == 0 {
						problem = fmt.Errorf("missing native failure identity: %+v", info)
					}
					mu.Unlock()
					if failure == "module" && event == DelayImportLoadLibraryFailure {
						module, err := native.Open(dll)
						if err != nil {
							mu.Lock()
							problem = err
							mu.Unlock()
						}
						return DelayImportResult{Module: module, OwnModule: true}
					}
					if failure == "name" && event == DelayImportGetProcAddressFailure && info.Name == "missing" {
						return DelayImportResult{Address: native.Lookup(info.Module, "add")}
					}
					if failure == "ordinal" && event == DelayImportGetProcAddressFailure && info.Ordinal == 65535 {
						return DelayImportResult{Address: native.LookupOrdinal(info.Module, 7)}
					}
					mu.Lock()
					problem = fmt.Errorf("unexpected failure event: %d %+v", event, info)
					mu.Unlock()
					return DelayImportResult{}
				}
				opts := Options{DelayImports: DelayImportsLazy, LibraryPaths: []string{dir}, DelayHooks: DelayImportHooks{Failure: hook}}
				if failure == "module" {
					opts.LibraryPaths = nil
				}
				s := New(opts)
				defer s.Close()
				load(t, s, consumer, dependency)
				load(t, s, writeComponentObjects(t, t.TempDir(), parts)...)
				call(t, s, "delay_call", 20, 22, 252)
				call(t, s, "delay_call", 20, 22, 252)
				mu.Lock()
				gotFailures, gotProblem := failures, problem
				mu.Unlock()
				if gotFailures != 1 || gotProblem != nil {
					t.Fatal("native failure recovery/cache", gotFailures, gotProblem)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestNativeCOFFLazyRecoveredModuleOwnership(t *testing.T) {
	needNative(t)
	target := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH] + "-pc-windows-msvc"
	getModule := syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleExW")
	for _, opt := range []string{"-O0", "-O2"} {
		for _, tc := range []struct {
			name, procedure  string
			mode, exceptions int32
		}{
			{"module", "scalar", 4, 1},
			{"module_then_procedure", "entry", 5, 2},
		} {
			t.Run(opt+"/"+tc.name, func(t *testing.T) {
				control := nativeInputSession(t, "testdata/cofflazy/probe.c", "library", opt)
				scalar, err := control.Lookup("scalar")
				if err != nil {
					t.Fatal(err)
				}
				*probeNativeWord(t, control, "recovery_target") = scalar
				*probeNativeWord(t, control, "recovery_module") = control.libs[0]
				parts, _ := delayFixturePartsDLL(t, target, []delayFixtureEntry{{"add", tc.procedure, 0}}, "", "prototype.dll")
				s := New(Options{DelayImports: DelayImportsLazy})
				defer s.Close()
				load(t, s, writeComponentObjects(t, t.TempDir(), parts)...)
				if err := s.Link("add"); err != nil {
					t.Fatal(err)
				}
				call(t, control, "configure_handler", tc.mode, 0, 0)
				armed := true
				defer func() {
					if armed {
						call(t, control, "configure_handler", 0, 0, 0)
					}
				}()
				call(t, s, "add", 20, 22, 42)
				call(t, s, "add", 20, 22, 42)
				counter, err := control.Lookup("native_exceptions")
				if err != nil {
					t.Fatal(err)
				}
				if value := *(*int32)(probeNativePointer(counter)); value != tc.exceptions {
					t.Fatal("native recovery stages", value, tc.exceptions)
				}
				call(t, control, "configure_handler", 0, 0, 0)
				armed = false
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				// FROM_ADDRESS | UNCHANGED_REFCOUNT safely queries the loader;
				// never call GetProcAddress on a possibly over-released handle.
				var module uintptr
				loaded, _, err := getModule.Call(6, scalar, uintptr(unsafe.Pointer(&module)))
				if loaded == 0 || module != control.libs[0] {
					t.Fatal("image released the control's original reference", module, err)
				}
				call(t, control, "scalar", 20, 22, 42)
			})
		}
	}
}

func TestNativeCOFFLazyGNUArchives(t *testing.T) {
	needNative(t)
	if runtime.GOARCH == "arm64" {
		t.Skip("no qualified GNU ARM64 delay-archive producer")
	}
	target := map[string]string{"amd64": "x86_64", "386": "i686"}[runtime.GOARCH] + "-pc-windows-msvc"
	archives := []string{"windows_" + runtime.GOARCH + ".a"}
	if runtime.GOARCH == "386" {
		archives = append(archives, "windows_386_compat.a")
	}
	for _, opt := range []string{"-O0", "-O2"} {
		for _, archive := range archives {
			t.Run(opt+"/"+archive, func(t *testing.T) {
				dir, _ := delayProvider(t, target, opt)
				source := filepath.Join(dir, "consumer.c")
				code := "__declspec(dllimport) int add(int,int),numbered(int,int);\nint entry(int a,int b){return add(a,b)+numbered(a,b);}\n"
				if err := os.WriteFile(source, []byte(code), 0600); err != nil {
					t.Fatal(err)
				}
				consumer := compile(t, source, filepath.Join(dir, "consumer.obj"), "--target="+target, opt)
				s := New(Options{DelayImports: DelayImportsLazy, LibraryPaths: []string{dir}})
				defer s.Close()
				load(t, s, consumer, filepath.Join("testdata", "coffdelay", archive))
				if err := s.Link("entry"); err != nil {
					t.Fatal(err)
				}
				if s.image.lazy == nil || len(s.libs) != 0 {
					t.Fatal("GNU delay archive was eagerly loaded")
				}
				call(t, s, "entry", 20, 22, 84)
				call(t, s, "entry", 20, 22, 84)
				if err := s.UnloadDelayImports(); err != nil {
					t.Fatal(err)
				}
				call(t, s, "entry", 20, 22, 84)
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestNativeCOFFLazyInitializer(t *testing.T) {
	needNative(t)
	target := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH] + "-pc-windows-msvc"
	for _, opt := range []string{"-O0", "-O2"} {
		for _, failed := range []bool{false, true} {
			t.Run(opt+fmt.Sprintf("/failed_%v", failed), func(t *testing.T) {
				dir, _ := delayProvider(t, target, opt)
				source := filepath.Join(dir, "initializer.c")
				code := "__declspec(dllimport) int add(int,int);\nextern int observed;\nint entry(int a,int b){return a+b;}\n"
				if failed {
					code += "static int start(void){observed=add(20,22);return 37;}\n__attribute__((used,section(\".CRT$XIU\"))) static int (*const init)(void)=start;\n"
				} else {
					code += "static void start(void){observed=add(20,22);}\n__attribute__((used,section(\".CRT$XCU\"))) static void (*const init)(void)=start;\n"
				}
				if err := os.WriteFile(source, []byte(code), 0600); err != nil {
					t.Fatal(err)
				}
				consumer := compile(t, source, filepath.Join(dir, "initializer.obj"), "--target="+target, opt)
				parts, _ := delayFixtureParts(t, target, []delayFixtureEntry{{"add", "add", 0}})
				storage, err := native.Alloc(4)
				if err != nil {
					t.Fatal(err)
				}
				defer native.Free(storage)
				var s *Session
				var mu sync.Mutex
				var problem error
				starts := 0
				hooks := DelayImportHooks{Notify: func(event DelayImportEvent, _ DelayImportInfo) DelayImportResult {
					if event != DelayImportStartProcessing {
						return DelayImportResult{}
					}
					_, ordinary := s.Lookup("entry")
					scoped := s.WithInitializingAddress("entry", func(address uintptr) error {
						value, err := examplecall.Int32(address, 20, 22)
						if err == nil && value != 42 {
							err = fmt.Errorf("nested scoped native call returned %d", value)
						}
						return err
					})
					mu.Lock()
					starts++
					if !errors.Is(ordinary, ErrInitializing) || scoped != nil {
						problem = fmt.Errorf("initializer reentry: ordinary=%v scoped=%v", ordinary, scoped)
					}
					mu.Unlock()
					return DelayImportResult{}
				}}
				s = New(Options{DelayImports: DelayImportsLazy, LibraryPaths: []string{dir}, DelayHooks: hooks})
				defer s.Close()
				if err := s.Define("observed", uintptr(unsafe.Pointer(&storage[0]))); err != nil {
					t.Fatal(err)
				}
				load(t, s, consumer)
				load(t, s, writeComponentObjects(t, t.TempDir(), parts)...)
				err = s.Link("entry")
				if failed && !errors.Is(err, ErrInitialization) || !failed && err != nil {
					t.Fatal("native initializer result", failed, err)
				}
				mu.Lock()
				gotStarts, gotProblem := starts, problem
				mu.Unlock()
				if le.Uint32(storage) != 42 || gotStarts != 1 || gotProblem != nil {
					t.Fatal("lazy initializer/scoped reentry", le.Uint32(storage), gotStarts, gotProblem)
				}
				if err := s.Close(); err != nil {
					t.Fatal("failed initializer provider cleanup", err)
				}
			})
		}
	}
}
