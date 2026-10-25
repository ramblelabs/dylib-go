package dylib

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ramblelabs/dylib-go/internal/native"
)

func threadDestructorInputs(t *testing.T, optimization string, nativeMode bool, extra ...string) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	tlsFlag := "-femulated-tls"
	if nativeMode {
		tlsFlag = "-fno-emulated-tls"
	}
	flags := []string{"-std=c++17", tlsFlag, "-fno-exceptions", "-fno-rtti", optimization}
	flags = append(flags, extra...)
	source := "testdata/emutls_dtor_objects.cpp"
	if runtime.GOOS == "windows" {
		triple := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH]
		abi := "-w64-windows-gnu"
		if os.Getenv("DYLIB_TEST_TLS_ABI") == "msvc" {
			abi, source = "-pc-windows-msvc", "testdata/emutls_msvc_objects.cpp"
		}
		flags = append(flags, "--target="+triple+abi)
	}
	if os.Getenv("DYLIB_TEST_TLS_LOCAL") == "1" {
		source = "testdata/tls_local_objects.cpp"
	}
	cc := compiler()
	if runtime.GOOS == "darwin" && os.Getenv("DYLIB_LLVM_CLANG") != "" {
		// Apple Clang 17 mixes native and emulated TLS in C++ -O0 output.
		// Use the qualified producer without changing other native fixtures.
		cc = os.Getenv("DYLIB_LLVM_CLANG")
	}
	object := compileWith(t, cc, source, filepath.Join(dir, "objects.o"), flags...)
	archive := filepath.Join(dir, "objects.a")
	if nativeMode {
		poison := compile(t, "testdata/tls_unowned_thread_exit.c", filepath.Join(dir, "poison.o"))
		command(t, "ar", "rcs", archive, poison, object)
	} else {
		command(t, "ar", "rcs", archive, object)
	}
	finalizer := compile(t, "testdata/emutls_dtor_finalizer.c", filepath.Join(dir, "finalizer.o"), optimization)
	return object, archive, finalizer
}

func threadDestructorSession(t *testing.T, host *Session, input, finalizer string, nativeMode bool) *Session {
	t.Helper()
	s := New(tlsDestructorOptions(nativeMode))
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	event, err := host.Resolve("record_event")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DefineSymbol("record_event", event); err != nil {
		t.Fatal(err)
	}
	load(t, s, finalizer, input)
	return s
}

func TestNativeEmulatedTLSThreadDestructors(t *testing.T) {
	needNative(t)
	testTLSThreadDestructors(t, false)
}

func testTLSThreadDestructors(t *testing.T, nativeMode bool) {
	for _, optimization := range []string{"-O0", "-O2"} {
		t.Run(optimization, func(t *testing.T) {
			object, archive, finalizer := threadDestructorInputs(t, optimization, nativeMode)
			for _, kind := range []string{"object", "archive"} {
				t.Run(kind, func(t *testing.T) {
					runtime.LockOSThread()
					defer runtime.UnlockOSThread()
					host := nativeThreadLibrary(t, "testdata/emutls_dtor_host.c")
					input := object
					if kind == "archive" {
						input = archive
					}
					s := threadDestructorSession(t, host, input, finalizer, nativeMode)
					call(t, s, "tls_step", 20, 22, 49)
					call(t, host, "recorded_event", -1, 0, 0)
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					read := func(a, b int32) (int32, error) { return callInt32(host, "recorded_event", a, b) }
					expectEvents(t, read, 9, 704, 8, 704, 900)
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					expectEvents(t, read, 9, 704, 8, 704, 900)
				})
			}
		})
	}
}

func TestNativeEmulatedTLSThreadDestructorRetirement(t *testing.T) {
	needNative(t)
	testTLSThreadDestructorRetirement(t, false)
}

func testTLSThreadDestructorRetirement(t *testing.T, nativeMode bool) {
	for _, optimization := range []string{"-O0", "-O2"} {
		t.Run(optimization, func(t *testing.T) {
			object, archive, finalizer := threadDestructorInputs(t, optimization, nativeMode)
			for _, kind := range []string{"object", "archive"} {
				for _, mode := range []string{"pending", "active"} {
					t.Run(kind+"/"+mode, func(t *testing.T) {
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
						start, err := host.Resolve("tls_threads_pause")
						if err != nil {
							t.Fatal(err)
						}
						if err := start.WithAddress(func(address uintptr) error { native.CallPointer(address, entry); return nil }); err != nil {
							t.Fatal(err)
						}
						joined := false
						defer func() {
							if !joined {
								callInt32(host, "tls_dtor_gate", 0, 0)
								callInt32(host, "tls_threads_release", 0, 0)
							}
						}()
						waitThreadDestructorCount(t, host, "tls_threads_ready", 4)
						lease.Close()
						var done chan error
						if mode == "active" {
							call(t, host, "tls_dtor_gate", 1, 0, 0)
							done = make(chan error, 1)
							go func() {
								value, err := callInt32(host, "tls_threads_release", 0, 0)
								if err == nil && value != 0 {
									err = fmt.Errorf("worker: %d", value)
								}
								done <- err
							}()
							waitThreadDestructorCount(t, host, "tls_dtor_entered", 4)
						}
						if err := s.Close(); !errors.Is(err, ErrPendingThreadDestructors) {
							t.Fatal("close did not retain pending native callbacks", err)
						}
						if s.failedImage == nil || len(s.symbolDependencies) != 1 {
							t.Fatal("cleanup retry lost code/data/provider ownership")
						}
						if mode == "active" {
							call(t, host, "tls_dtor_gate", 0, 0, 0)
							if err := <-done; err != nil {
								t.Fatal(err)
							}
						} else {
							call(t, host, "tls_threads_release", 0, 0, 0)
						}
						joined = true
						call(t, host, "recorded_event", -1, 0, 16)
						call(t, host, "tls_dtor_sum", 0, 0, 6888)
						if err := s.Close(); err != nil {
							t.Fatal(err)
						}
						call(t, host, "recorded_event", -1, 0, 17)
						call(t, host, "tls_dtor_sum", 0, 0, 7788)
					})
				}
			}
		})
	}
}

func waitThreadDestructorCount(t *testing.T, host *Session, name string, want int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := callInt32(host, name, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: got %d, want %d", name, got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestNativeEmulatedTLSThreadDestructorIsolation(t *testing.T) {
	needNative(t)
	testTLSThreadDestructorIsolation(t, false)
}

func testTLSThreadDestructorIsolation(t *testing.T, nativeMode bool) {
	for _, optimization := range []string{"-O0", "-O2"} {
		t.Run(optimization, func(t *testing.T) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			object, _, finalizer := threadDestructorInputs(t, optimization, nativeMode)
			host := nativeThreadLibrary(t, "testdata/emutls_dtor_host.c")
			first := threadDestructorSession(t, host, object, finalizer, nativeMode)
			second := threadDestructorSession(t, host, object, finalizer, nativeMode)
			call(t, first, "tls_step", 20, 22, 49)
			call(t, second, "tls_step", 20, 22, 49)
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			call(t, second, "tls_step", 20, 22, 50)
			if err := second.Close(); err != nil {
				t.Fatal(err)
			}
			read := func(a, b int32) (int32, error) { return callInt32(host, "recorded_event", a, b) }
			expectEvents(t, read, 9, 704, 8, 704, 900, 9, 705, 9, 705, 900)
		})
	}
}

func TestNativeEmulatedTLSThreadDestructorLateRegistration(t *testing.T) {
	needNative(t)
	testTLSThreadDestructorLateRegistration(t, false)
}

func testTLSThreadDestructorLateRegistration(t *testing.T, nativeMode bool) {
	for _, optimization := range []string{"-O0", "-O2"} {
		t.Run(optimization, func(t *testing.T) {
			object, _, _ := threadDestructorInputs(t, optimization, nativeMode)
			finalizer := compile(t, "testdata/emutls_dtor_finalizer.c", filepath.Join(t.TempDir(), "late.o"), optimization, "-DDYLIB_TLS_LATE")
			host := nativeThreadLibrary(t, "testdata/emutls_dtor_host.c")
			s := threadDestructorSession(t, host, object, finalizer, nativeMode)
			if err := s.Link("tls_step"); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			read := func(a, b int32) (int32, error) { return callInt32(host, "recorded_event", a, b) }
			expectEvents(t, read, 900, 901, 902, 903)
		})
	}
}

func TestEmulatedTLSThreadDestructorFormats(t *testing.T) {
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		t.Run(target, func(t *testing.T) {
			path := compile(t, "testdata/emutls_dtor_objects.cpp", filepath.Join(t.TempDir(), "destructors.o"), "--target="+target, "-ffreestanding", "-std=c++17", "-femulated-tls", "-fno-exceptions", "-fno-rtti")
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
		})
	}
}

func TestNativeEmulatedTLSThreadDestructorManual(t *testing.T) {
	needNative(t)
	testTLSThreadDestructorManual(t, false)
}

func testTLSThreadDestructorManual(t *testing.T, nativeMode bool) {
	for _, mode := range []string{"registration", "budget"} {
		t.Run(mode, func(t *testing.T) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			dir := t.TempDir()
			source := "testdata/emutls_dtor_manual.c"
			if runtime.GOOS == "windows" && os.Getenv("DYLIB_TEST_TLS_ABI") == "msvc" {
				source = "testdata/emutls_msvc_manual.c"
			}
			input := compile(t, source, filepath.Join(dir, "manual.o"))
			finalizer := compile(t, "testdata/emutls_dtor_finalizer.c", filepath.Join(dir, "finalizer.o"))
			host := nativeThreadLibrary(t, "testdata/emutls_dtor_host.c")
			withoutOpt := New(Options{ProcessSymbols: true})
			defer withoutOpt.Close()
			event, err := host.Resolve("record_event")
			if err != nil {
				t.Fatal(err)
			}
			if err := withoutOpt.DefineSymbol("record_event", event); err != nil {
				t.Fatal(err)
			}
			load(t, withoutOpt, input)
			if err := withoutOpt.Link("tls_register"); err == nil || !strings.Contains(err.Error(), "Options.EmulatedTLS") {
				t.Fatal("process-global thread runtime accepted without opt-in", err)
			}
			s := threadDestructorSession(t, host, input, finalizer, nativeMode)
			call(t, s, "tls_invalid", 0, 0, -1)
			count, sum := int32(2), int32(942)
			if mode == "registration" {
				call(t, s, "tls_register", 20, 22, 0)
			} else {
				call(t, s, "tls_budget", 0, 0, 4096)
				count, sum = 4097, 4996
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			call(t, host, "recorded_event", -1, 0, count)
			call(t, host, "tls_dtor_sum", 0, 0, sum)
		})
	}
}

func TestNativeEmulatedTLSThreadDestructorInitialization(t *testing.T) {
	needNative(t)
	testTLSThreadDestructorInitialization(t, false)
}

func testTLSThreadDestructorInitialization(t *testing.T, nativeMode bool) {
	for _, optimization := range []string{"-O0", "-O2"} {
		t.Run(optimization, func(t *testing.T) {
			object, archive, finalizer := threadDestructorInputs(t, optimization, nativeMode, "-DDYLIB_TLS_DYNAMIC_INIT")
			for _, kind := range []string{"object", "archive"} {
				t.Run(kind, func(t *testing.T) {
					runtime.LockOSThread()
					defer runtime.UnlockOSThread()
					host := nativeThreadLibrary(t, "testdata/emutls_dtor_host.c")
					input := object
					if kind == "archive" {
						input = archive
					}
					s := threadDestructorSession(t, host, input, finalizer, nativeMode)
					call(t, s, "tls_step", 20, 22, 49)
					call(t, s, "tls_step", 20, 22, 50)
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					read := func(a, b int32) (int32, error) { return callInt32(host, "recorded_event", a, b) }
					expectEvents(t, read, 1007, 1009, 9, 705, 9, 705, 900)
				})
			}
		})
	}
}

func TestNativeEmulatedTLSThreadDestructorInitializerFailure(t *testing.T) {
	needNative(t)
	testTLSThreadDestructorInitializerFailure(t, false)
}

func testTLSThreadDestructorInitializerFailure(t *testing.T, nativeMode bool) {
	for _, optimization := range []string{"-O0", "-O2"} {
		t.Run(optimization, func(t *testing.T) {
			object, archive, finalizer := threadDestructorInputs(t, optimization, nativeMode)
			failed := compile(t, "testdata/emutls_dtor_cinit.c", filepath.Join(t.TempDir(), "failed.o"), optimization)
			for _, kind := range []string{"object", "archive"} {
				t.Run(kind, func(t *testing.T) {
					runtime.LockOSThread()
					defer runtime.UnlockOSThread()
					host := nativeThreadLibrary(t, "testdata/emutls_dtor_host.c")
					input := object
					if kind == "archive" {
						input = archive
					}
					s := threadDestructorSession(t, host, input, finalizer, nativeMode)
					loadCInitializerFixture(t, s, failed)
					if err := s.Link("tls_step"); !errors.Is(err, ErrInitialization) {
						t.Fatal("C initializer failure", err)
					}
					read := func(a, b int32) (int32, error) { return callInt32(host, "recorded_event", a, b) }
					expectEvents(t, read, 100)
					if s.failedImage == nil || s.failedImage.initialized {
						t.Fatal("lost incomplete image ownership")
					}
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					expectEvents(t, read, 100, 9, 704, 8, 704)
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					expectEvents(t, read, 100, 9, 704, 8, 704)
				})
			}
		})
	}
}
