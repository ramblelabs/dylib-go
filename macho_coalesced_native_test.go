package dylib

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func needMachOCoalescedNative(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("native Mach-O coalescing requires macOS")
	}
	needNative(t)
	if runtime.GOARCH == "amd64" {
		return "x86_64-apple-macosx11"
	}
	return "arm64-apple-macosx11"
}

func machoCoalescedAssembly(t *testing.T, target, text string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "fixture.s")
	if err := os.WriteFile(src, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return compile(t, src, src+".o", "--target="+target)
}

func machoCoalescedArchive(t *testing.T, inputs ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "coalesced.a")
	command(t, "ar", append([]string{"rcs", path}, inputs...)...)
	return path
}

func TestNativeMachOCoalescedAtoms(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			weak, _ := machoCoalescedFixture(t, target, "macho_coalesced.S", opt)
			local := machoCoalescedLocalFile(t, weak)
			strong, _ := machoCoalescedFixture(t, target, "macho_coalesced_strong.c", opt)
			probe, _ := machoCoalescedFixture(t, target, "macho_coalesced_probe.c", opt)
			first, _ := machoCoalescedFixture(t, target, "macho_coalesced.S", opt, "-DDYLIB_COAL_VALUE=7")
			second, _ := machoCoalescedFixture(t, target, "macho_coalesced.S", opt, "-DDYLIB_COAL_VALUE=33", "-DDYLIB_COAL_ANCHOR=_coal_anchor_second")
			probe7, _ := machoCoalescedFixture(t, target, "macho_coalesced_probe.c", opt, "-DDYLIB_COAL_VALUE=7", "-DDYLIB_COAL_TWO_ANCHORS")
			probe33, _ := machoCoalescedFixture(t, target, "macho_coalesced_probe.c", opt, "-DDYLIB_COAL_VALUE=33", "-DDYLIB_COAL_TWO_ANCHORS")
			probeCommon, _ := machoCoalescedFixture(t, target, "macho_coalesced_probe.c", opt, "-DDYLIB_COAL_VALUE=7")
			common := machoCoalescedAssembly(t, target, ".comm _coal_value,8,3\n")
			poison := machoCoalescedAssembly(t, target, ".data\n.globl _coal_missing, _coal_missing_drop\n_coal_missing:\n.quad _coal_poison\n_coal_missing_drop:\n.quad 0\n.globl _coal_missing_func\n_coal_missing_func:\n.quad 0\n")
			bad := machoCoalescedAssembly(t, target, ".section __DATA,__coal,coalesced\n.globl _coal_unused, _coal_unused_alias\n.weak_definition _coal_unused\n_coal_unused:\n_coal_unused_alias:\n.quad _coal_unused_missing\n.subsections_via_symbols\n")
			archive := machoCoalescedArchive(t, poison, bad, weak)
			localArchive := machoCoalescedArchive(t, poison, bad, local)
			weakArchive := machoCoalescedArchive(t, first, second, poison)
			commonArchive := machoCoalescedArchive(t, poison, first)
			for _, tc := range []struct {
				name   string
				inputs []string
				two    bool
			}{
				{"strong_objects", []string{weak, strong, probe}, false},
				{"strong_reversed", []string{strong, weak, probe}, false},
				{"strong_archive", []string{probe, strong, archive}, false},
				{"local_objects", []string{local, strong, probe}, false},
				{"local_archive", []string{probe, strong, localArchive}, false},
				{"weak_first", []string{first, second, probe7}, true},
				{"weak_second", []string{second, first, probe33}, true},
				{"weak_archive", []string{probe7, weakArchive}, true},
				{"common_first", []string{common, first, probeCommon}, false},
				{"common_second", []string{first, common, probeCommon}, false},
				{"common_archive", []string{common, probeCommon, commonArchive}, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					s := New(Options{})
					defer s.Close()
					load(t, s, tc.inputs...)
					roots := []string{"coal_check", "coal_anchor"}
					if tc.two {
						roots = append(roots, "coal_anchor_second")
					}
					if err := s.Link(roots...); err != nil {
						t.Fatal(err)
					}
					call(t, s, "coal_check", 20, 22, 42)
					if len(s.image.objects) != 3 {
						t.Fatal("unrelated archive dependencies extracted", len(s.image.objects))
					}
				})
			}
			t.Run("retry", func(t *testing.T) {
				s := New(Options{})
				defer s.Close()
				load(t, s, weak, probe, archive)
				if err := s.Link("coal_check"); err == nil || s.image != nil {
					t.Fatal("unresolved retained weak body published", err)
				}
				load(t, s, strong)
				if err := s.Link("coal_check"); err != nil {
					t.Fatal(err)
				}
				call(t, s, "coal_check", 20, 22, 42)
				if len(s.image.objects) != 3 {
					t.Fatal("failed attempt retained poison extraction")
				}
			})
			t.Run("selected_alias", func(t *testing.T) {
				s := New(Options{})
				defer s.Close()
				load(t, s, archive)
				if err := s.Link("coal_unused"); err == nil || !strings.Contains(err.Error(), "coalesced atoms") {
					t.Fatal("selected public coalesced alias accepted", err)
				}
			})
			t.Run("host_conflict", func(t *testing.T) {
				s := New(Options{})
				defer s.Close()
				if err := s.Define("coal_value", 1); err != nil {
					t.Fatal(err)
				}
				load(t, s, first, probeCommon)
				if err := s.Link("coal_check"); err == nil || !strings.Contains(err.Error(), "defined by both host and object") || s.image != nil {
					t.Fatal("host/object conflict policy changed", err)
				}
			})
		})
	}
}

func TestNativeMachOCoalescedLifecycle(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			weak, _ := machoCoalescedFixture(t, target, "macho_coalesced.S", opt, "-DDYLIB_COAL_LIFECYCLE")
			strong, _ := machoCoalescedFixture(t, target, "macho_coalesced_strong.c", opt, "-DDYLIB_COAL_LIFECYCLE")
			probe, _ := machoCoalescedFixture(t, target, "macho_coalesced_probe.c", opt)
			for _, kind := range []string{"objects", "archive"} {
				t.Run(kind, func(t *testing.T) {
					_, observer, read := lifecycleObserver(t, t.TempDir())
					s := New(Options{})
					defer s.Close()
					event, err := observer.Resolve("record_event")
					if err != nil {
						t.Fatal(err)
					}
					if err := s.DefineSymbol("record_event", event); err != nil {
						t.Fatal(err)
					}
					input := weak
					if kind == "archive" {
						input = machoCoalescedArchive(t, weak)
					}
					load(t, s, strong, probe, input)
					if err := s.Link("coal_check"); err != nil {
						t.Fatal(err)
					}
					call(t, s, "coal_check", 20, 22, 42)
					expectEvents(t, read, 100)
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					expectEvents(t, read, 100, 200)
				})
			}
		})
	}
}

func TestNativeMachOCoalescedUnwind(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			weak, _ := machoCoalescedFixture(t, target, "macho_coalesced.S", opt, "-DDYLIB_COAL_CFI")
			strong, _ := machoCoalescedFixture(t, target, "macho_coalesced_strong.c", opt)
			probe, _ := machoCoalescedFixture(t, target, "macho_coalesced_probe.c", opt)
			for _, mode := range []string{"discarded", "retained"} {
				t.Run(mode, func(t *testing.T) {
					s := New(Options{RegisterUnwind: mode == "retained"})
					defer s.Close()
					load(t, s, weak, strong, probe)
					err := s.Link("coal_check")
					if mode == "retained" {
						if err == nil || !strings.Contains(err.Error(), "coalesced code discard with retained unwind") || s.image != nil {
							t.Fatal("unqualified coalesced frames accepted", err)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						call(t, s, "coal_check", 20, 22, 42)
					}
				})
			}
		})
	}
}

func TestNativeMachOCoalescedSystemControl(t *testing.T) {
	target := needMachOCoalescedNative(t)
	sdk := os.Getenv("SDKROOT")
	if sdk == "" {
		b, err := exec.Command("xcrun", "--sdk", "macosx", "--show-sdk-path").CombinedOutput()
		if err != nil {
			t.Fatalf("macOS SDK: %v: %s", err, b)
		}
		sdk = strings.TrimSpace(string(b))
	}
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			weak, _ := machoCoalescedFixture(t, target, "macho_coalesced.S", opt)
			local := machoCoalescedLocalFile(t, weak)
			strong, _ := machoCoalescedFixture(t, target, "macho_coalesced_strong.c", opt)
			resolved, _ := machoCoalescedFixture(t, target, "macho_coalesced.S", opt, "-DDYLIB_COAL_VALUE=7")
			common := machoCoalescedAssembly(t, target, ".comm _coal_value,8,3\n")
			for _, tc := range []struct {
				name   string
				inputs []string
				flags  []string
			}{
				{"weak_first", []string{weak, strong}, nil},
				{"strong_first", []string{strong, weak}, nil},
				{"local_weak_first", []string{local, strong}, nil},
				{"local_strong_first", []string{strong, local}, nil},
				{"common_first", []string{common, resolved}, []string{"-DDYLIB_COAL_VALUE=7"}},
				{"common_second", []string{resolved, common}, []string{"-DDYLIB_COAL_VALUE=7"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					exe := filepath.Join(t.TempDir(), "control")
					args := []string{"--target=" + target, "-isysroot", sdk, "-Wl,-syslibroot," + sdk, opt, "testdata/macho_coalesced_probe.c", "-DDYLIB_COAL_STANDALONE", "-Wl,-dead_strip"}
					args = append(args, tc.flags...)
					args = append(args, tc.inputs...)
					command(t, compiler(), append(args, "-o", exe)...)
					if output, err := exec.Command(exe).CombinedOutput(); err != nil {
						trace := exe + "-trace"
						command(t, compiler(), append(args, "-DDYLIB_COAL_TRACE", "-o", trace)...)
						detail, traceErr := exec.Command(trace).CombinedOutput()
						t.Fatalf("Apple control: %v: %s; diagnostic build: %v:\n%s", err, output, traceErr, detail)
					}
				})
			}
		})
	}
}
