package dylib

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/internal/native"
)

func tlsDestructorOptions(nativeMode bool) Options {
	if nativeMode {
		return Options{NativeTLS: true}
	}
	return Options{EmulatedTLS: true}
}

func needNativeCXXTLS(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("native C++ TLS requires a supported native model")
	}
	needNative(t)
}

func TestNativeCXXTLSThreadDestructors(t *testing.T) {
	needNativeCXXTLS(t)
	testTLSThreadDestructors(t, true)
}
func TestNativeCXXTLSThreadDestructorRetirement(t *testing.T) {
	needNativeCXXTLS(t)
	testTLSThreadDestructorRetirement(t, true)
}
func TestNativeCXXTLSThreadDestructorIsolation(t *testing.T) {
	needNativeCXXTLS(t)
	testTLSThreadDestructorIsolation(t, true)
}
func TestNativeCXXTLSThreadDestructorLateRegistration(t *testing.T) {
	needNativeCXXTLS(t)
	testTLSThreadDestructorLateRegistration(t, true)
}
func TestNativeCXXTLSThreadDestructorManual(t *testing.T) {
	needNativeCXXTLS(t)
	testTLSThreadDestructorManual(t, true)
}
func TestNativeCXXTLSThreadDestructorInitialization(t *testing.T) {
	needNativeCXXTLS(t)
	testTLSThreadDestructorInitialization(t, true)
}
func TestNativeCXXTLSThreadDestructorInitializerFailure(t *testing.T) {
	needNativeCXXTLS(t)
	testTLSThreadDestructorInitializerFailure(t, true)
}

func TestNativeCXXTLSActiveInitialization(t *testing.T) {
	needNativeCXXTLS(t)
	testTLSActiveInitialization(t, true)
}

func testTLSActiveInitialization(t *testing.T, nativeMode bool) {
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			object, archive, finalizer := threadDestructorInputs(t, opt, nativeMode, "-DDYLIB_TLS_DYNAMIC_INIT")
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
					closed := make(chan error, 1)
					go func() { closed <- s.Close() }()
					waitSessionRetired(t, s)
					select {
					case err := <-closed:
						t.Fatal("active native constructors lost their code lease:", err)
					default:
					}
					call(t, host, "tls_dtor_gate", 0, 0, 0)
					call(t, host, "tls_threads_release", 0, 0, 0)
					lease.Close()
					if err := <-closed; err != nil {
						t.Fatal(err)
					}
					call(t, host, "recorded_event", -1, 0, 25)
				})
			}
		})
	}
}

func TestNativeCXXTLSAcrossTargets(t *testing.T) {
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(target+"/"+opt, func(t *testing.T) {
				path := compile(t, "testdata/emutls_dtor_objects.cpp", filepath.Join(t.TempDir(), "native.o"), "--target="+target, "-std=c++17", "-ffreestanding", "-fno-emulated-tls", "-fno-exceptions", "-fno-rtti", "-DDYLIB_TLS_DYNAMIC_INIT", opt)
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				f, err := parse(path, b)
				if err != nil || len(f.info.Unsupported) != 0 {
					t.Fatal(f, err)
				}
				if needed, err := checkTLSRuntime([]*object{f.obj}, Options{NativeTLS: true}); err != nil || !needed {
					t.Fatal(needed, err)
				}
				if _, err := checkTLSRuntime([]*object{f.obj}, Options{}); err == nil {
					t.Fatal("native C++ thread runtime accepted without opt-in")
				}
				for _, s := range f.obj.symbols {
					if strings.HasPrefix(s.name, emulatedTLSPrefix) || s.name == emulatedTLSHelper {
						t.Fatal("native C++ fixture silently uses emulated TLS")
					}
				}
				var needed bool
				switch f.obj.info.Format {
				case "Mach-O":
					needed, err = checkMachOTLS([]*object{f.obj}, true)
				case "COFF":
					needed, err = checkCOFFTLS([]*object{f.obj}, true)
				default:
					needed, err = checkELFTLS([]*object{f.obj}, true)
				}
				if err != nil || !needed {
					t.Fatal(needed, err)
				}
			})
		}
	}
}

func TestTLSRuntimeModeIsolation(t *testing.T) {
	for _, format := range []string{"ELF", "Mach-O", "COFF"} {
		t.Run(format, func(t *testing.T) {
			o := &object{info: Info{Format: format}, symbols: []symbol{{name: "__cxa_thread_atexit", global: true}}}
			_, err := checkTLSRuntime([]*object{o}, Options{NativeTLS: true})
			if err != nil {
				t.Fatal("native thread-hook mode:", err)
			}
			if _, err := checkTLSRuntime([]*object{o}, Options{}); err == nil {
				t.Fatal("thread registry accepted without a TLS option")
			}
			for _, name := range []string{emulatedTLSHelper, emulatedTLSPrefix + "value", "__dyn_tls_on_demand_init"} {
				o.symbols[0].name = name
				if _, err := checkTLSRuntime([]*object{o}, Options{NativeTLS: true}); err == nil {
					t.Fatal("NativeTLS enabled an emulated runtime feature:", name)
				}
			}
		})
	}
}
