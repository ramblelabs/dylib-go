//go:build windows

package dylib

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"unsafe"

	"github.com/ramblelabs/dylib-go/internal/native"
)

func nativeHookImports(t *testing.T, s *Session) {
	t.Helper()
	machine := uint16(0x8664)
	if runtime.GOARCH == "386" {
		machine = 0x14c
	}
	if runtime.GOARCH == "arm64" {
		machine = 0xaa64
	}
	for _, fn := range []struct {
		name  string
		bytes int
	}{{"LoadLibraryW", 4}, {"GetProcAddress", 8}} {
		name, policy := fn.name, uint16(1)
		if runtime.GOARCH == "386" {
			name = fmt.Sprintf("_%s@%d", name, fn.bytes)
			policy = 3
		}
		data := shortImport(machine, 0, policy, name, "kernel32.dll", "", 0)
		load(t, s, writeComponentObjects(t, t.TempDir(), [][]byte{data})...)
	}
}

func nativeHookFixture(t *testing.T, opt, storage string, mode uint32, export string, ordinal uint16, hooks DelayImportHooks) (*Session, string) {
	t.Helper()
	target := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH] + "-pc-windows-msvc"
	dir, dll := delayProvider(t, target, opt)
	// Keep the importing object away from the provider so failure hooks can
	// exercise an actual failed search, independently of the importer choice.
	source := filepath.Join(t.TempDir(), "consumer.c")
	if err := os.WriteFile(source, []byte("__declspec(dllimport) int add(int,int),other(int,int);\nint entry(int a,int b){return add(a,b);}\nint nested(int a,int b){return other(a,b);}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	consumer := compile(t, source, source+".obj", "--target="+target, opt)
	args := []string{opt, "--target=" + strings.TrimSuffix(target, "-pc-windows-msvc") + "-w64-windows-gnu"}
	if runtime.GOARCH == "386" {
		if root := os.Getenv("DYLIB_NATIVE_SYSROOT"); root != "" {
			args = append(args, "--sysroot="+root)
		}
	}
	if storage == "const" {
		args = append(args, "-DHOOK_STORAGE=const", "-DHOOK_CONST=1")
	}
	// The GNU target uses the installed native SDK. GNU/MSVC COFF C calls share
	// these qualified Windows ABIs, including SDK WINAPI on 386.
	if storage == "crt" {
		args = append(args, "-DHOOK_INIT_CRT=1")
	}
	hookObject := compile(t, "testdata/cofflazy/hooks.c", filepath.Join(t.TempDir(), "hooks.obj"), args...)
	parts, _ := delayFixtureParts(t, target, []delayFixtureEntry{{"add", export, ordinal}, {"other", "", 7}})
	opts := Options{DelayImports: DelayImportsLazy, LibraryPaths: []string{dir}, DelayHooks: hooks}
	if mode == 4 {
		opts.LibraryPaths = nil
	}
	s := New(opts)
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	load(t, s, consumer, hookObject)
	load(t, s, writeComponentObjects(t, t.TempDir(), parts)...)
	nativeHookImports(t, s)
	if err := s.Link("entry"); err != nil {
		t.Fatal(err)
	}
	*nativeHookWord32(t, s, "hook_mode") = mode
	path, err := s.Lookup("hook_path")
	if err != nil {
		t.Fatal(err)
	}
	wide, err := syscall.UTF16FromString(dll)
	if err != nil || len(wide) > 4096 {
		t.Fatal("native hook path", err)
	}
	copy(unsafe.Slice((*uint16)(probeNativePointer(path)), 4096), wide)
	return s, dll
}

func nativeHookWord32(t *testing.T, s *Session, name string) *uint32 {
	t.Helper()
	address, err := s.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return (*uint32)(probeNativePointer(address))
}

func nativeHookEvents(t *testing.T, s *Session) []uint32 {
	t.Helper()
	if bad := *nativeHookWord32(t, s, "hook_bad"); bad != 0 {
		t.Fatal("SDK native hook validation", bad)
	}
	n := *nativeHookWord32(t, s, "hook_steps")
	if n > 64 {
		t.Fatal("too many hook events", n)
	}
	address, err := s.Lookup("hook_sequence")
	if err != nil {
		t.Fatal(err)
	}
	return append([]uint32(nil), unsafe.Slice((*uint32)(probeNativePointer(address)), n)...)
}

func TestNativeCOFFDelayHookVariables(t *testing.T) {
	needNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		for _, storage := range []string{"mutable", "const"} {
			for _, tc := range []struct {
				name    string
				mode    uint32
				export  string
				ordinal uint16
				events  []uint32
			}{
				{"notify", 0, "add", 0, []uint32{0, 1, 2, 5}},
				{"start_bypass", 1, "missing", 0, []uint32{0, 5, 0, 5}},
				{"before_load", 2, "add", 0, []uint32{0, 1, 2, 5}},
				{"before_lookup", 3, "missing", 0, []uint32{0, 1, 2, 5}},
				{"load_failure", 4, "add", 0, []uint32{0, 1, 3, 2, 5}},
				{"lookup_failure", 5, "missing", 0, []uint32{0, 1, 2, 4, 5}},
				{"ordinal_failure", 5, "", 65535, []uint32{0, 1, 2, 4, 5}},
				{"ordinal", 0, "", 7, []uint32{0, 1, 2, 5}},
				{"nested", 6, "add", 0, []uint32{0, 0, 1, 2, 5, 2, 5}},
			} {
				t.Run(opt+"/"+storage+"/"+tc.name, func(t *testing.T) {
					var goCalls atomic.Int32
					goHooks := DelayImportHooks{Notify: func(event DelayImportEvent, _ DelayImportInfo) DelayImportResult {
						if event == DelayImportStartProcessing {
							goCalls.Add(1)
							value := make([]byte, 64<<10)
							value[0] = 42
							runtime.GC()
							runtime.KeepAlive(value)
						}
						return DelayImportResult{}
					}}
					s, dll := nativeHookFixture(t, opt, storage, tc.mode, tc.export, tc.ordinal, goHooks)
					if tc.mode == 1 {
						module, err := native.Open(dll)
						if err != nil {
							t.Fatal(err)
						}
						// Release this external loan after its consumer's image is gone.
						defer func() {
							if err := s.Close(); err != nil {
								t.Error(err)
							}
							if err := native.Close(module); err != nil {
								t.Error(err)
							}
						}()
						replacement := native.Lookup(module, "add")
						if replacement == 0 {
							t.Fatal("missing replacement")
						}
						*probeNativeWord(t, s, "hook_replacement") = replacement
					}
					call(t, s, "entry", 20, 22, 42)
					call(t, s, "entry", 20, 22, 42)
					if got := nativeHookEvents(t, s); fmt.Sprint(got) != fmt.Sprint(tc.events) {
						t.Fatal("native notification order/cache", got, tc.events)
					}
					wantCalls := int32(1)
					if tc.mode == 1 || tc.mode == 6 {
						wantCalls = 2
					}
					if goCalls.Load() != wantCalls {
						t.Fatal("Go/native hook composition", goCalls.Load(), wantCalls)
					}
					// Every ordinary success owns/reset its provider. Native Start bypasses
					// retain neither a logical target nor a native cell or module reference.
					owner := s.image.lazy.providers
					owner.mu.Lock()
					found, valid := false, false
					for entry, index := range s.image.lazy.aliases {
						if entry.public == "add" {
							slot := owner.slots[index]
							found = true
							valid = slot.address != 0 && slot.module.handle != 0
							if tc.mode == 1 {
								valid = slot.address == 0 && slot.module.handle == 0
							}
						}
					}
					owner.mu.Unlock()
					if !found || !valid {
						t.Fatal("native bypass/cache ownership", found, valid)
					}
					if err := s.UnloadDelayImports(); err != nil {
						t.Fatal(err)
					}
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestNativeCOFFDelayHookGoPriority(t *testing.T) {
	needNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		for _, tc := range []struct {
			name   string
			event  DelayImportEvent
			mode   uint32
			export string
			events []uint32
		}{
			{"start", DelayImportStartProcessing, 0, "missing", []uint32{5}},
			{"before_load", DelayImportBeforeLoadLibrary, 0, "add", []uint32{0, 2, 5}},
			{"before_lookup", DelayImportBeforeGetProcAddress, 0, "missing", []uint32{0, 1, 5}},
			{"load_failure", DelayImportLoadLibraryFailure, 4, "add", []uint32{0, 1, 2, 5}},
			{"lookup_failure", DelayImportGetProcAddressFailure, 5, "missing", []uint32{0, 1, 2, 5}},
		} {
			t.Run(opt+"/"+tc.name, func(t *testing.T) {
				var module uintptr
				var s *Session
				defer func() {
					if s != nil {
						if err := s.Close(); err != nil {
							t.Error(err)
						}
					}
					if module != 0 {
						if err := native.Close(module); err != nil {
							t.Error(err)
						}
					}
				}()
				overrides := 0
				goHook := func(event DelayImportEvent, info DelayImportInfo) DelayImportResult {
					if event != tc.event {
						return DelayImportResult{}
					}
					overrides++
					switch event {
					case DelayImportBeforeLoadLibrary, DelayImportLoadLibraryFailure:
						return DelayImportResult{Module: module}
					case DelayImportStartProcessing:
						return DelayImportResult{Address: native.Lookup(module, "add")}
					default:
						return DelayImportResult{Address: native.Lookup(info.Module, "add")}
					}
				}
				var dll string
				s, dll = nativeHookFixture(t, opt, "const", tc.mode, tc.export, 0, DelayImportHooks{Notify: goHook, Failure: goHook})
				var err error
				module, err = native.Open(dll)
				if err != nil {
					t.Fatal(err)
				}
				call(t, s, "entry", 20, 22, 42)
				call(t, s, "entry", 20, 22, 42)
				if got := nativeHookEvents(t, s); fmt.Sprint(got) != fmt.Sprint(tc.events) || overrides != 1 {
					t.Fatal("Go override/native fallback", got, tc.events, overrides)
				}
			})
		}
	}
}

func TestNativeCOFFDelayHookMutableCells(t *testing.T) {
	needNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			s, _ := nativeHookFixture(t, opt, "mutable", 0, "add", 0, DelayImportHooks{})
			for _, name := range []string{"__pfnDliNotifyHook2", "__pfnDliFailureHook2"} {
				*probeNativeWord(t, s, name) = 0
			}
			call(t, s, "entry", 20, 22, 42)
			if got := nativeHookEvents(t, s); len(got) != 0 {
				t.Fatal("null native hook", got)
			}
			if err := s.UnloadDelayImports(); err != nil {
				t.Fatal(err)
			}
			symbol, err := s.Resolve("install_hooks")
			if err != nil {
				t.Fatal(err)
			}
			if err := symbol.WithAddress(func(address uintptr) error { native.CallVoid(address); return nil }); err != nil {
				t.Fatal(err)
			}
			call(t, s, "entry", 20, 22, 42)
			if got := nativeHookEvents(t, s); fmt.Sprint(got) != "[0 1 2 5]" {
				t.Fatal("late native hook installation/reset", got)
			}
			if *nativeHookWord32(t, s, "hook_installed") != 1 {
				t.Fatal("native installer did not run")
			}
		})
	}
}

func TestNativeCOFFDelayHookInitialization(t *testing.T) {
	needNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			s, _ := nativeHookFixture(t, opt, "crt", 0, "add", 0, DelayImportHooks{})
			if *nativeHookWord32(t, s, "hook_installed") != 1 {
				t.Fatal("CRT did not initialize hook cells")
			}
			call(t, s, "entry", 20, 22, 42)
			if got := nativeHookEvents(t, s); fmt.Sprint(got) != "[0 1 2 5]" {
				t.Fatal("CRT-installed native hooks", got)
			}
		})
	}
}
