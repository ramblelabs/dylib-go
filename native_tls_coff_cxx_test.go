package dylib

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/internal/native"
)

func TestNativeCOFFCXXTLSMixedModes(t *testing.T) {
	testCOFFCXXTLSMixedModes(t, "gnu")
}

func testCOFFCXXTLSMixedModes(t *testing.T, nativeDialect string) {
	needCOFFNativeTLS(t)
	for _, dialect := range []string{"gnu", "msvc"} {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(dialect+"/"+opt, func(t *testing.T) {
				dir := t.TempDir()
				base := []string{"-std=c++17", "-fno-exceptions", "-fno-rtti"}
				nativeFlags := append(coffTLSFlags(nativeDialect, opt), base...)
				nativeFlags = append(nativeFlags, "-DValue=NativeValue", "-DCheck=NativeCheck", "-Dcli_cxx_tls_add=native_tls_add")
				emulatedFlags := append(coffTLSFlags(dialect, opt), base...)
				emulatedFlags = append(emulatedFlags, "-femulated-tls", "-DValue=EmulatedValue", "-DCheck=EmulatedCheck", "-Dcli_cxx_tls_add=emulated_tls_add")
				first := compile(t, "testdata/cli_tls.cpp", filepath.Join(dir, "native.o"), nativeFlags...)
				second := compile(t, "testdata/cli_tls.cpp", filepath.Join(dir, "emulated.o"), emulatedFlags...)
				archive := filepath.Join(dir, "mixed.a")
				command(t, "ar", "rcs", archive, first, second)
				for _, kind := range []string{"objects", "archive"} {
					t.Run(kind, func(t *testing.T) {
						runtime.LockOSThread()
						defer runtime.UnlockOSThread()
						inputs := []string{first, second}
						if kind == "archive" {
							inputs = []string{archive}
						}
						roots := []string{"native_tls_add", "emulated_tls_add"}
						for _, options := range []Options{{NativeTLS: true}, {EmulatedTLS: true}} {
							s := New(options)
							load(t, s, inputs...)
							if err := s.Link(roots...); err == nil || !strings.Contains(err.Error(), "Options.") {
								t.Fatal("mixed C++ TLS accepted without both options:", err)
							}
							if err := s.Close(); err != nil {
								t.Fatal(err)
							}
						}
						s := New(Options{NativeTLS: true, EmulatedTLS: true})
						defer s.Close()
						load(t, s, inputs...)
						if err := s.Link(roots...); err != nil {
							t.Fatal(err)
						}
						for _, name := range roots {
							call(t, s, name, 20, 20, 42)
							call(t, s, name, 1, 1, 44)
						}
						// Each independent global Check traps unless its TLS value is
						// destroyed exactly once before global finalization.
						if err := s.Close(); err != nil {
							t.Fatal(err)
						}
					})
				}
			})
		}
	}
}

func TestNativeCOFFCXXTLSExistingThreads(t *testing.T) {
	needCOFFNativeTLS(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			object, archive, finalizer := threadDestructorInputs(t, opt, true, "-DDYLIB_TLS_DYNAMIC_INIT")
			for _, kind := range []string{"object", "archive"} {
				t.Run(kind, func(t *testing.T) {
					host := nativeThreadLibrary(t, "testdata/emutls_dtor_host.c")
					workers := nativeThreadLibrary(t, "testdata/coff_tls_threads.c")
					start, err := workers.Resolve("tls_existing_start")
					if err != nil {
						t.Fatal(err)
					}
					run, err := workers.Resolve("tls_existing_run")
					if err != nil {
						t.Fatal(err)
					}
					if err := start.WithAddress(func(fn uintptr) error { native.CallVoid(fn); return nil }); err != nil {
						t.Fatal(err)
					}
					joined := false
					defer func() {
						if !joined {
							run.WithAddress(func(fn uintptr) error { native.CallPointer(fn, 0); return nil })
						}
					}()
					input := object
					if kind == "archive" {
						input = archive
					}
					// All native workers precede the TLS carrier and its module index.
					s := threadDestructorSession(t, host, input, finalizer, true)
					step, err := s.Resolve("tls_step")
					if err != nil {
						t.Fatal(err)
					}
					owner := s.image.coffTLS
					if err := run.WithAddress(func(fn uintptr) error {
						return step.WithAddress(func(entry uintptr) error { native.CallPointer(fn, entry); return nil })
					}); err != nil {
						t.Fatal(err)
					}
					joined = true
					call(t, workers, "tls_existing_result", 0, 0, 0)
					// Receiver values and nested registrations must remain valid during
					// FLS exit dispatch, before the OS releases the native TLS template.
					call(t, host, "recorded_event", -1, 0, 24)
					call(t, host, "tls_dtor_sum", 0, 0, 14952)
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					call(t, host, "recorded_event", -1, 0, 25)
					call(t, host, "tls_dtor_sum", 0, 0, 15852)
					if owner.handle != 0 || owner.directory != "" {
						t.Fatal("C++ thread exit retained its native TLS carrier")
					}
				})
			}
		})
	}
}
