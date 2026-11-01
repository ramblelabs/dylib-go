//go:build windows

package dylib

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/ramblelabs/dylib-go/internal/native"
)

// Build the installed SDK hook in a separate provider, rather than copying a
// hook function address into Go memory. The consumer owns the actual cell.
func importedHookFixture(t *testing.T, opt, input string, kind uint16, mode uint32, exported string) (s, provider *Session) {
	t.Helper()
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH]
	target := arch + "-pc-windows-msvc"
	dir, dll := delayProvider(t, target, opt)
	args := []string{opt, "--target=" + arch + "-w64-windows-gnu"}
	if runtime.GOARCH == "386" && os.Getenv("DYLIB_NATIVE_SYSROOT") != "" {
		args = append(args, "--sysroot="+os.Getenv("DYLIB_NATIVE_SYSROOT"))
	}
	if kind == 2 {
		args = append(args, "-DHOOK_STORAGE=const", "-DHOOK_CONST=1")
	}
	if input == "weak" {
		args = append(args, "-DHOOK_ALIAS_STORAGE=1")
	}
	hook := compile(t, "testdata/cofflazy/hooks.c", filepath.Join(t.TempDir(), "hooks.obj"), args...)
	stub := filepath.Join(t.TempDir(), "nested.c")
	if err := os.WriteFile(stub, []byte("int nested(int a,int b){return a+b;}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stub = compile(t, stub, stub+".obj", "--target="+target, opt)
	names := []string{"__pfnDliNotifyHook2", "__pfnDliFailureHook2"}
	if input == "weak" {
		names = []string{"hook_notify_storage", "hook_failure_storage"}
	}
	stats := []string{"hook_mode", "hook_bad", "hook_steps", "hook_sequence", "hook_path", "hook_replacement", "hook_installed"}
	provider = New(Options{})
	t.Cleanup(func() { provider.Close() })
	if input == "object" || input == "archive" {
		paths := []string{hook, stub}
		if input == "archive" {
			archive := filepath.Join(t.TempDir(), "hooks.lib")
			command(t, "llvm-ar", append([]string{"rcs", archive}, paths...)...)
			paths = []string{archive}
		}
		load(t, provider, paths...)
		nativeHookImports(t, provider)
	} else {
		def := filepath.Join(dir, "hooks.def")
		text := "EXPORTS\ninstall_hooks\n"
		for _, name := range append(append([]string(nil), names...), stats...) {
			text += name + " DATA\n"
		}
		if err := os.WriteFile(def, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		imports := filepath.Join(t.TempDir(), "kernel.lib")
		command(t, "llvm-ar", append([]string{"rcs", imports}, writeComponentObjects(t, t.TempDir(), nativeHookImportRecords())...)...)
		hookDLL := filepath.Join(dir, "hooks.dll")
		command(t, "lld-link", "/dll", "/noentry", "/nodefaultlib", "/def:"+def, "/out:"+hookDLL, hook, stub, imports)
		if input == "library" {
			load(t, provider, hookDLL)
		}
	}
	importDLL := "probe.dll"
	if mode == 4 {
		importDLL = "dylib-go-missing-imported-hook-provider.dll"
	}
	s = New(Options{DelayImports: DelayImportsLazy, LibraryPaths: []string{dir}})
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	source := filepath.Join(t.TempDir(), "consumer.c")
	if err := os.WriteFile(source, []byte("__declspec(dllimport) int add(int,int); int entry(int a,int b){return add(a,b);}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	load(t, s, compile(t, source, source+".obj", "--target="+target, opt))
	parts, _ := delayFixturePartsDLL(t, target, []delayFixtureEntry{{"add", exported, 0}}, "", importDLL)
	load(t, s, writeComponentObjects(t, t.TempDir(), parts)...)
	if input == "object" || input == "archive" || input == "library" {
		for _, name := range append(append([]string(nil), names...), append(stats, "install_hooks")...) {
			symbol, err := provider.Resolve(name)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.DefineSymbol(name, symbol); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		machine, prefix, policy := uint16(0x8664), "", uint16(1)
		if runtime.GOARCH == "arm64" {
			machine = 0xaa64
		} else if runtime.GOARCH == "386" {
			machine, prefix, policy = 0x14c, "_", 2
		}
		for _, name := range names {
			data := shortImport(machine, kind, policy, prefix+name, "hooks.dll", "", 0)
			load(t, s, writeComponentObjects(t, t.TempDir(), [][]byte{data})...)
		}
		if input == "weak" {
			// CONST imports expose a public IAT alias as the fallback. The
			// loader must bind its target variable instead of that IAT cell.
			data, _ := coffAssembly(t, target, nativeDelayHookAliases(target, "weak_chain"))
			load(t, s, writeComponentObjects(t, t.TempDir(), [][]byte{data})...)
		}
	}
	if err := s.Link("entry"); err != nil {
		t.Fatal(err)
	}
	*nativeHookWord32(t, s, "hook_mode") = mode
	address, err := s.Lookup("hook_path")
	if err != nil {
		t.Fatal(err)
	}
	wide, err := syscall.UTF16FromString(dll)
	if err != nil || len(wide) > 4096 {
		t.Fatal("hook provider path", err)
	}
	copy(unsafe.Slice((*uint16)(probeNativePointer(address)), 4096), wide)
	return
}

func TestNativeCOFFImportedDelayHookVariables(t *testing.T) {
	needNative(t)
	getModule := syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleExW")
	for _, opt := range []string{"-O0", "-O2"} {
		for _, tc := range []struct {
			name, input, export, events string
			kind                        uint16
			mode                        uint32
		}{
			{"data", "import", "add", "[0 1 2 5]", 1, 0},
			{"const", "import", "add", "[0 1 2 5]", 2, 0},
			{"weak", "weak", "add", "[0 1 2 5]", 2, 0},
			{"load_failure", "import", "add", "[0 1 3 2 5]", 1, 4},
			{"lookup_failure", "import", "missing", "[0 1 2 4 5]", 2, 5},
		} {
			t.Run(opt+"/"+tc.name, func(t *testing.T) {
				s, _ := importedHookFixture(t, opt, tc.input, tc.kind, tc.mode, tc.export)
				handle := s.dllHandles["hooks.dll"]
				if handle == 0 {
					t.Fatal("ordinary data import did not retain its DLL")
				}
				for _, name := range []string{"__pfnDliNotifyHook2", "__pfnDliFailureHook2"} {
					cell := s.image.lazy.nativeHooks.notify
					if strings.Contains(name, "Failure") {
						cell = s.image.lazy.nativeHooks.failure
					}
					export := name
					if tc.input == "weak" {
						export = map[string]string{"__pfnDliNotifyHook2": "hook_notify_storage", "__pfnDliFailureHook2": "hook_failure_storage"}[name]
					}
					if cell != native.Lookup(handle, export) {
						t.Fatal("bound an IAT slot instead of the DLL's variable", name)
					}
				}
				for attempt := 0; attempt < 2; attempt++ {
					*nativeHookWord32(t, s, "hook_steps") = 0
					call(t, s, "entry", 20, 22, 42)
					call(t, s, "entry", 20, 22, 42)
					if got := fmt.Sprint(nativeHookEvents(t, s)); got != tc.events {
						t.Fatal("imported SDK hook/reset", got, tc.events)
					}
					if err := s.UnloadDelayImports(); err != nil {
						t.Fatal(err)
					}
					if s.dllHandles["hooks.dll"] != handle || native.Lookup(handle, "hook_mode") == 0 {
						t.Fatal("delay reset released an ordinary hook provider")
					}
				}
				cell := s.image.lazy.nativeHooks.notify
				var module uintptr
				loaded, _, err := getModule.Call(6, cell, uintptr(unsafe.Pointer(&module)))
				if loaded == 0 || module != handle {
					t.Fatal("hook provider was not retained through delay reset", module, err)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				module = 0
				loaded, _, _ = getModule.Call(6, cell, uintptr(unsafe.Pointer(&module)))
				if loaded != 0 || module != 0 {
					t.Fatal("consumer leaked its ordinary hook provider", module)
				}
			})
		}
	}
}

func TestNativeCOFFRetainedDelayHookVariables(t *testing.T) {
	needNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		for _, input := range []string{"object", "archive", "library"} {
			t.Run(opt+"/"+input, func(t *testing.T) {
				s, provider := importedHookFixture(t, opt, input, 1, 0, "add")
				closed := make(chan error, 1)
				go func() { closed <- provider.Close() }()
				waitSessionRetired(t, provider)
				select {
				case err := <-closed:
					t.Fatal("provider closed before its hook consumer", err)
				default:
				}
				call(t, s, "entry", 20, 22, 42)
				if err := s.UnloadDelayImports(); err != nil {
					t.Fatal(err)
				}
				// Read live cells after provider retirement; neither a snapshot
				// nor a new provider acquisition can satisfy this call.
				*probeNativeWord(t, s, "__pfnDliNotifyHook2") = 0
				call(t, s, "entry", 20, 22, 42)
				if got := fmt.Sprint(nativeHookEvents(t, s)); got != "[0 1 2 5]" {
					t.Fatal("retained mutable provider cell", got)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				if err := <-closed; err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
