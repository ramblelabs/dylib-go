package dylib

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"unsafe"

	"github.com/ramblelabs/dylib-go/internal/native"
)

// Exercise the native runtime with each host's C ABI as well as the real MSVC
// object tests below. No Windows image execution is claimed on other hosts.
func TestNativeMSVCEmulatedTLSHelpers(t *testing.T) {
	for _, mode := range []string{"emulated", "native", "mixed"} {
		t.Run(mode, func(t *testing.T) { testMSVCTLSHelpers(t, mode != "native", mode != "emulated") })
	}
}

func testMSVCTLSHelpers(t *testing.T, emulated, nativeMode bool) {
	needNative(t)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	s := New(Options{})
	defer s.Close()
	data, err := native.Alloc(os.Getpagesize())
	if err != nil {
		t.Fatal(err)
	}
	defer native.Free(data)
	code, err := native.Alloc(os.Getpagesize())
	if err != nil {
		t.Fatal(err)
	}
	defer native.Free(code)
	owner, err := native.NewEmulatedTLS()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { owner.DrainThreadDestructors(); owner.Close() }()
	for i, helper := range []struct {
		name   string
		target uintptr
		args   int
	}{
		{"tls_runtime_init", native.EmulatedTLSMSVCInitHelper(), 0},
		{"tls_runtime_plain", native.EmulatedTLSMSVCExitHelper(), 1},
		{"tls_runtime_argument", native.EmulatedTLSThreadExitHelper(), 3},
	} {
		thunk, err := lifecycleThunk(runtime.GOARCH, runtime.GOOS, owner.Context(), helper.target, helper.args)
		if err != nil {
			t.Fatal(err)
		}
		offset := i * 128
		copy(code[offset:], thunk)
		if err := s.Define(helper.name, uintptr(unsafe.Pointer(&code[offset]))); err != nil {
			t.Fatal(err)
		}
	}
	native.ClearCache(code)
	if err := native.Protect(code, true, false); err != nil {
		t.Fatal(err)
	}
	word := uint64(unsafe.Sizeof(uintptr(0)))
	descriptor := &image{mem: data, pointerSize: word}
	putTLSWord(descriptor, 0, 1)
	putTLSWord(descriptor, word, 1)
	guard := uintptr(unsafe.Pointer(unsafe.SliceData(data)))
	if emulated {
		if err := owner.Add(guard); err != nil {
			t.Fatal(err)
		}
	} else {
		guard = 0
	}
	input := compile(t, "testdata/emutls_msvc_runtime.c", filepath.Join(t.TempDir(), "runtime.o"))
	load(t, s, input)
	if nativeMode {
		getter, err := s.Resolve("tls_native_guard")
		if err != nil {
			t.Fatal(err)
		}
		if err := getter.WithAddress(owner.SetNativeThreadGuard); err != nil {
			t.Fatal(err)
		}
	}
	constructor, err := s.Resolve("tls_constructor")
	if err != nil {
		t.Fatal(err)
	}
	if err := constructor.WithAddress(func(address uintptr) error { return owner.SetThreadInitializers(guard, []uintptr{address}) }); err != nil {
		t.Fatal(err)
	}
	call(t, s, "tls_use", 0, 0, 4)
	wantLive := 0
	if emulated {
		wantLive = 1
	}
	if owner.LiveValues() != wantLive {
		t.Fatal("thread guard was not allocated once")
	}
	if pending, drained := owner.DrainThreadDestructors(); pending != 0 || !drained {
		t.Fatal(pending, drained)
	}
	want := int32(4231)
	if runtime.GOOS == "windows" && runtime.GOARCH == "386" {
		want = 42531
	}
	call(t, s, "tls_events", 0, 0, want)
	if pending, drained := owner.DrainThreadDestructors(); pending != 0 || drained {
		t.Fatal("destructors executed twice", pending, drained)
	}
}

func TestMSVCEmulatedTLSGuardValidation(t *testing.T) {
	for _, word := range []uint64{4, 8} {
		for _, tc := range []struct {
			name   string
			change func(*image)
		}{
			{"size", func(im *image) { putTLSWord(im, 128, 2) }},
			{"alignment", func(im *image) { putTLSWord(im, 128+word, 2) }},
			{"object", func(im *image) { putTLSWord(im, 128+2*word, 1) }},
			{"template", func(im *image) { putTLSWord(im, 128+3*word, 1) }},
			{"truncated", func(im *image) { im.common[emulatedTLSMSVCGuard] = commonAllocation{512, 4 * word} }},
		} {
			t.Run(strconv.FormatUint(word, 10)+"/"+tc.name, func(t *testing.T) {
				im := tlsValidationImage(word)
				im.common = map[string]commonAllocation{emulatedTLSMSVCGuard: {128, 4 * word}}
				putTLSWord(im, 128, 1)
				putTLSWord(im, 128+word, 1)
				if values, err := im.emulatedTLSDescriptors(); err != nil || len(values) != 2 {
					t.Fatal(values, err)
				}
				tc.change(im)
				if _, err := im.emulatedTLSDescriptors(); err == nil {
					t.Fatal("invalid private guard accepted")
				}
			})
		}
	}
}

func TestMSVCThreadInitializerValidation(t *testing.T) {
	for _, word := range []uint64{4, 8} {
		for _, name := range []string{"valid", "null", "data", "unaligned", "budget"} {
			if word == 4 && name == "unaligned" {
				continue // i386 permits unaligned instruction addresses.
			}
			t.Run(strconv.FormatUint(word, 10)+"/"+name, func(t *testing.T) {
				count := uint64(1)
				if name == "budget" {
					count = 4097
				}
				arch := "arm64"
				if word == 4 {
					arch = "386"
				}
				o := &object{info: Info{Format: "COFF", OS: "windows", Arch: arch}, sections: []*section{nil,
					{name: ".CRT$XDU", size: count * word, lifecycle: lifecycleThreadInit},
					{name: ".text", offset: 40000, size: 8, exec: true},
				}}
				im := &image{base: 0x10000, mem: make([]byte, 65536), objects: []*object{o}, pointerSize: word}
				address := uint64(im.base) + 40000
				if name == "null" {
					address = 0
				}
				if name == "data" {
					address = uint64(im.base)
				}
				if name == "unaligned" {
					address++
				}
				for i := uint64(0); i < count; i++ {
					putTLSWord(im, i*word, address)
				}
				err := im.prepareLifecycle()
				if name == "valid" || name == "null" {
					if err != nil {
						t.Fatal(err)
					}
					want := 0
					if name == "valid" {
						want = 1
					}
					if len(im.initializers) != 0 || len(im.finalizers) != 0 || len(im.threadInitializers) != want {
						t.Fatal("thread table was not separated from global lifecycle")
					}
				} else if err == nil {
					t.Fatal("invalid thread initializer accepted")
				}
			})
		}
	}
}

func TestMSVCEmulatedTLSRuntimeValidation(t *testing.T) {
	for _, name := range []string{emulatedTLSMSVCInit, emulatedTLSMSVCExit} {
		for _, system := range []string{"windows", "linux", "darwin"} {
			o := &object{info: Info{OS: system}, symbols: []symbol{{name: name, global: true}}}
			needed, err := checkTLSRuntime([]*object{o}, Options{EmulatedTLS: true})
			if system != "windows" {
				if err == nil {
					t.Fatal("MSVC helper accepted on", system)
				}
				continue
			}
			if err != nil || !needed {
				t.Fatal(needed, err)
			}
			o.sections = []*section{nil, {name: ".text", size: 16, exec: true}}
			o.symbols = append(o.symbols, symbol{name: "replacement", section: 1, global: true})
			o.symbols[0].alias = &weakAlias{target: 1}
			if _, err := checkTLSRuntime([]*object{o}, Options{EmulatedTLS: true}); err == nil {
				t.Fatal("weak runtime override accepted")
			}
			o.symbols[0].alias = nil
			o.symbols[0].section = 1
			if _, err := checkTLSRuntime([]*object{o}, Options{EmulatedTLS: true}); err == nil {
				t.Fatal("input runtime override accepted")
			}
			o.symbols[0].section = 0
			o.symbols = append(o.symbols, symbol{name: emulatedTLSMSVCGuard, section: 1, global: true})
			if _, err := checkTLSRuntime([]*object{o}, Options{EmulatedTLS: true}); err == nil {
				t.Fatal("input guard override accepted")
			}
		}
	}
	o := &object{info: Info{OS: "windows"}, sections: []*section{nil, {lifecycle: lifecycleThreadInit}}}
	if _, err := checkTLSRuntime([]*object{o}, Options{EmulatedTLS: false}); err == nil {
		t.Fatal("thread table accepted without opt-in")
	}
	o.symbols = []symbol{{name: "__imp_" + emulatedTLSMSVCGuard, global: true}}
	if _, err := checkTLSRuntime([]*object{o}, Options{EmulatedTLS: true}); err == nil {
		t.Fatal("imported runtime descriptor accepted")
	}
}

func needMSVCTLS(t *testing.T) {
	t.Helper()
	needNative(t)
	if runtime.GOOS != "windows" {
		t.Skip("native Windows MSVC emulated TLS runtime")
	}
	t.Setenv("DYLIB_TEST_TLS_ABI", "msvc")
}

func TestNativeMSVCEmulatedTLS(t *testing.T) {
	needMSVCTLS(t)
	for _, tc := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"destructors", TestNativeEmulatedTLSThreadDestructors},
		{"retirement", TestNativeEmulatedTLSThreadDestructorRetirement},
		{"isolation", TestNativeEmulatedTLSThreadDestructorIsolation},
		{"late_registration", TestNativeEmulatedTLSThreadDestructorLateRegistration},
		{"manual", TestNativeEmulatedTLSThreadDestructorManual},
		{"initialization", TestNativeEmulatedTLSThreadDestructorInitialization},
		{"failed_initialization", TestNativeEmulatedTLSThreadDestructorInitializerFailure},
	} {
		t.Run(tc.name, tc.run)
	}
}

func TestMSVCEmulatedTLSFormats(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		for _, fixture := range []struct{ name, file string }{
			{"objects", "testdata/emutls_msvc_objects.cpp"},
			{"first", "testdata/emutls_msvc_first.cpp"},
			{"second", "testdata/emutls_msvc_second.cpp"},
			{"snapshot", "testdata/emutls_msvc_snapshot.c"},
			{"invalid_table", "testdata/emutls_msvc_bad.c"},
		} {
			for _, optimization := range []string{"-O0", "-O2"} {
				t.Run(target+"/"+fixture.name+"/"+optimization, func(t *testing.T) {
					flags := []string{"--target=" + target, "-ffreestanding", "-femulated-tls", optimization}
					if filepath.Ext(fixture.file) == ".cpp" {
						flags = append(flags, "-std=c++17", "-fno-exceptions", "-fno-rtti")
					}
					path := compile(t, fixture.file, filepath.Join(t.TempDir(), "tls.o"), flags...)
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					file, err := parse(path, data)
					if err != nil {
						t.Fatal(err)
					}
					if len(file.info.Unsupported) != 0 {
						t.Fatal(file.info.Unsupported)
					}
					if needed, err := checkTLSRuntime([]*object{file.obj}, Options{EmulatedTLS: true}); err != nil || !needed {
						t.Fatal(needed, err)
					}
					if _, err := checkTLSRuntime([]*object{file.obj}, Options{EmulatedTLS: false}); err == nil {
						t.Fatal("thread runtime accepted without opt-in")
					}
					found := false
					for _, sec := range file.obj.sections {
						if sec != nil && sec.name == ".CRT$XDU" {
							found = sec.lifecycle == lifecycleThreadInit
						}
					}
					if !found {
						t.Fatal("missing separately classified thread initializer table")
					}
				})
			}
		}
	}
}

func TestNativeMSVCEmulatedTLSMultipleObjects(t *testing.T) {
	needMSVCTLS(t)
	testMSVCTLSMultipleObjects(t, false)
}

func testMSVCTLSMultipleObjects(t *testing.T, nativeMode bool) {
	for _, optimization := range []string{"-O0", "-O2"} {
		t.Run(optimization, func(t *testing.T) {
			dir := t.TempDir()
			tlsFlag := "-femulated-tls"
			if nativeMode {
				tlsFlag = "-fno-emulated-tls"
			}
			flags := []string{"-std=c++17", tlsFlag, "-fno-exceptions", "-fno-rtti", optimization}
			first := compile(t, "testdata/emutls_msvc_first.cpp", filepath.Join(dir, "first.o"), flags...)
			second := compile(t, "testdata/emutls_msvc_second.cpp", filepath.Join(dir, "second.o"), flags...)
			bad := compile(t, "testdata/emutls_msvc_bad.c", filepath.Join(dir, "unused.o"))
			archive := filepath.Join(dir, "objects.a")
			command(t, "ar", "rcs", archive, first, second, bad)
			finalizer := compile(t, "testdata/emutls_dtor_finalizer.c", filepath.Join(dir, "finalizer.o"))
			for _, kind := range []string{"objects", "reversed_objects", "archive"} {
				for _, threads := range []bool{false, true} {
					name := kind + "/current_thread"
					if threads {
						name = kind + "/foreign_threads"
					}
					t.Run(name, func(t *testing.T) {
						runtime.LockOSThread()
						defer runtime.UnlockOSThread()
						host := nativeThreadLibrary(t, "testdata/emutls_dtor_host.c")
						input := first
						if kind == "archive" {
							input = archive
						} else if kind == "reversed_objects" {
							input = second
						}
						s := threadDestructorSession(t, host, input, finalizer, nativeMode)
						if kind == "reversed_objects" {
							load(t, s, first)
						} else if kind == "objects" {
							load(t, s, second)
						}
						if kind == "archive" {
							if err := s.Link("tls_bad_unused"); err == nil {
								t.Fatal("invalid selected thread initializer accepted")
							}
							call(t, host, "recorded_event", -1, 0, 0)
						}
						if err := s.Link("tls_step"); err != nil {
							t.Fatal(err)
						}
						call(t, host, "recorded_event", -1, 0, 0)
						if threads {
							step, err := s.Resolve("tls_step")
							if err != nil {
								t.Fatal(err)
							}
							start, err := host.Resolve("tls_threads")
							if err != nil {
								t.Fatal(err)
							}
							if err := step.WithAddress(func(entry uintptr) error {
								return start.WithAddress(func(run uintptr) error { native.CallPointer(run, entry); return nil })
							}); err != nil {
								t.Fatal(err)
							}
							call(t, host, "tls_thread_result", 0, 0, 0)
							call(t, host, "recorded_event", -1, 0, 16)
							call(t, host, "tls_dtor_sum", 0, 0, 1276)
						} else {
							call(t, s, "tls_step", 20, 22, 49)
							call(t, s, "tls_step", 20, 22, 50)
							read := func(a, b int32) (int32, error) { return callInt32(host, "recorded_event", a, b) }
							expectEvents(t, read, 102, 101)
						}
						if err := s.Close(); err != nil {
							t.Fatal(err)
						}
						if threads {
							call(t, host, "tls_dtor_sum", 0, 0, 2176)
						} else {
							read := func(a, b int32) (int32, error) { return callInt32(host, "recorded_event", a, b) }
							expectEvents(t, read, 102, 101, 9, 9, 900)
						}
					})
				}
			}
		})
	}
}

func TestNativeMSVCEmulatedTLSSnapshot(t *testing.T) {
	needMSVCTLS(t)
	testMSVCTLSSnapshot(t, false)
}

func testMSVCTLSSnapshot(t *testing.T, nativeMode bool) {
	for _, optimization := range []string{"-O0", "-O2"} {
		t.Run(optimization, func(t *testing.T) {
			dir := t.TempDir()
			flags := []string{optimization}
			if nativeMode {
				flags = append(flags, "-DDYLIB_NATIVE_MSVC_TLS")
			}
			object := compile(t, "testdata/emutls_msvc_snapshot.c", filepath.Join(dir, "snapshot.o"), flags...)
			archive := filepath.Join(dir, "snapshot.a")
			command(t, "ar", "rcs", archive, object)
			for _, kind := range []string{"object", "archive"} {
				t.Run(kind, func(t *testing.T) {
					runtime.LockOSThread()
					defer runtime.UnlockOSThread()
					s := New(tlsDestructorOptions(nativeMode))
					defer s.Close()
					input := object
					if kind == "archive" {
						input = archive
					}
					load(t, s, input)
					if err := s.Link("tls_snapshot_step"); err != nil {
						t.Fatal(err)
					}
					call(t, s, "tls_snapshot_value", 0, 0, 0)
					call(t, s, "tls_snapshot_step", 20, 22, 43)
					call(t, s, "tls_snapshot_step", 20, 22, 43)
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestNativeMSVCEmulatedTLSActiveInitialization(t *testing.T) {
	needMSVCTLS(t)
	testMSVCTLSActiveInitialization(t, false)
}

func testMSVCTLSActiveInitialization(t *testing.T, nativeMode bool) {
	for _, optimization := range []string{"-O0", "-O2"} {
		t.Run(optimization, func(t *testing.T) {
			object, archive, finalizer := threadDestructorInputs(t, optimization, nativeMode, "-DDYLIB_TLS_DYNAMIC_INIT")
			for _, kind := range []string{"object", "archive"} {
				t.Run(kind, func(t *testing.T) {
					host := nativeThreadLibrary(t, "testdata/emutls_dtor_host.c")
					input := object
					if kind == "archive" {
						input = archive
					}
					s := threadDestructorSession(t, host, input, finalizer, nativeMode)
					step, err := s.Resolve("tls_step")
					if err != nil {
						t.Fatal(err)
					}
					lease, err := step.Acquire()
					if err != nil {
						t.Fatal(err)
					}
					defer lease.Close()
					entry, err := lease.Address()
					if err != nil {
						t.Fatal(err)
					}
					call(t, host, "tls_dtor_gate", 1, 0, 0)
					start, err := host.Resolve("tls_threads_pause")
					if err != nil {
						t.Fatal(err)
					}
					if err := start.WithAddress(func(run uintptr) error { native.CallPointer(run, entry); return nil }); err != nil {
						t.Fatal(err)
					}
					defer func() { callInt32(host, "tls_dtor_gate", 0, 0); callInt32(host, "tls_threads_release", 0, 0) }()
					waitThreadDestructorCount(t, host, "tls_dtor_entered", 4)
					lease.Close()
					if err := s.Close(); !errors.Is(err, ErrPendingThreadDestructors) {
						t.Fatal("active constructors lost image ownership", err)
					}
					call(t, host, "tls_dtor_gate", 0, 0, 0)
					call(t, host, "tls_threads_release", 0, 0, 0)
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					call(t, host, "recorded_event", -1, 0, 25)
				})
			}
		})
	}
}
