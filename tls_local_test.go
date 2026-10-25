package dylib

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func localTLSABIs() []string {
	if runtime.GOOS == "windows" {
		return []string{"gnu", "msvc"}
	}
	return []string{"host"}
}

func TestNativeCXXLocalTLS(t *testing.T) {
	needNativeCXXTLS(t)
	t.Setenv("DYLIB_TEST_TLS_LOCAL", "1")
	for _, abi := range localTLSABIs() {
		t.Run(abi, func(t *testing.T) {
			t.Setenv("DYLIB_TEST_TLS_ABI", abi)
			for _, mode := range []string{"native", "emulated"} {
				t.Run(mode, func(t *testing.T) {
					nativeMode := mode == "native"
					for _, tc := range []struct {
						name string
						run  func(*testing.T, bool)
					}{
						{"lazy", testTLSLocalLazy},
						{"destructors", testTLSThreadDestructors},
						{"retirement", testTLSThreadDestructorRetirement},
						{"isolation", testTLSThreadDestructorIsolation},
						{"late_registration", testTLSThreadDestructorLateRegistration},
						{"initialization", testTLSThreadDestructorInitialization},
						{"failed_initialization", testTLSThreadDestructorInitializerFailure},
						{"active_initialization", testTLSActiveInitialization},
					} {
						t.Run(tc.name, func(t *testing.T) { tc.run(t, nativeMode) })
					}
				})
			}
		})
	}
}

func TestCXXLocalTLSAcrossTargets(t *testing.T) {
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		for _, mode := range []string{"native", "emulated"} {
			for _, opt := range []string{"-O0", "-O2"} {
				t.Run(target+"/"+mode+"/"+opt, func(t *testing.T) {
					flag := "-fno-emulated-tls"
					if mode == "emulated" {
						flag = "-femulated-tls"
					}
					path := compile(t, "testdata/tls_local_objects.cpp", filepath.Join(t.TempDir(), "local.o"), "--target="+target, "-ffreestanding", "-std=c++17", "-fno-exceptions", "-fno-rtti", flag, opt)
					b, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					f, err := parse(path, b)
					if err != nil || len(f.info.Unsupported) != 0 {
						t.Fatal(f, err)
					}
					options := tlsDestructorOptions(mode == "native")
					if needed, err := checkTLSRuntime([]*object{f.obj}, options); err != nil || !needed {
						t.Fatal(needed, err)
					}
					if _, err := checkTLSRuntime([]*object{f.obj}, Options{}); err == nil {
						t.Fatal("thread exits accepted without option")
					}
					if mode == "native" {
						for _, s := range f.obj.symbols {
							if strings.HasPrefix(s.name, emulatedTLSPrefix) || s.name == emulatedTLSHelper {
								t.Fatal("native fixture uses emulated values")
							}
						}
						if strings.Contains(target, "-pc-windows-msvc") {
							if usesMSVCNativeTLS([]*object{f.obj}) {
								t.Fatal("function-local fixture unexpectedly requires the per-TU native guard")
							}
							for _, sec := range f.obj.sections {
								if sec != nil && sec.lifecycle == lifecycleThreadInit {
									t.Fatal("function-local constructors published as eager thread initializers")
								}
							}
						}
					}
				})
			}
		}
	}
}

func testTLSLocalLazy(t *testing.T, nativeMode bool) {
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			object, archive, finalizer := threadDestructorInputs(t, opt, nativeMode, "-DDYLIB_TLS_DYNAMIC_INIT")
			for _, kind := range []string{"object", "archive"} {
				for _, order := range []string{"first", "second"} {
					t.Run(kind+"/"+order, func(t *testing.T) {
						runtime.LockOSThread()
						defer runtime.UnlockOSThread()
						host := nativeThreadLibrary(t, "testdata/emutls_dtor_host.c")
						input := object
						if kind == "archive" {
							input = archive
						}
						s := threadDestructorSession(t, host, input, finalizer, nativeMode)
						if err := s.Link("tls_first", "tls_second", "tls_guard_probe"); err != nil {
							t.Fatal(err)
						}
						read := func(a, b int32) (int32, error) { return callInt32(host, "recorded_event", a, b) }
						expectEvents(t, read)
						call(t, s, "tls_guard_probe", 0, 0, 3)
						expectEvents(t, read)
						if order == "first" {
							call(t, s, "tls_first", 0, 0, 7)
							call(t, s, "tls_first", 0, 0, 8)
							expectEvents(t, read, 1007)
							call(t, s, "tls_second", 0, 0, 9)
							expectEvents(t, read, 1007, 1009)
							if err := s.Close(); err != nil {
								t.Fatal(err)
							}
							expectEvents(t, read, 1007, 1009, 10, 703, 9, 703, 900)
						} else {
							call(t, s, "tls_second", 0, 0, 9)
							call(t, s, "tls_second", 0, 0, 10)
							expectEvents(t, read, 1009)
							call(t, s, "tls_first", 0, 0, 7)
							expectEvents(t, read, 1009, 1007)
							if err := s.Close(); err != nil {
								t.Fatal(err)
							}
							expectEvents(t, read, 1009, 1007, 8, 703, 11, 703, 900)
						}
					})
				}
			}
		})
	}
}
