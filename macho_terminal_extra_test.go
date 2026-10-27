package dylib

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func machoTerminalBSS(t *testing.T, target, variant string) (string, []byte) {
	t.Helper()
	path, b := machoCoalescedFixture(t, target, "macho_terminal.S", "-DDYLIB_TERM_ZERO", "-DDYLIB_TERM_FORM=0")
	if variant == "gb" {
		h := machoPointerHeader(t, b, "__bss")
		le.PutUint32(b[h+64:], le.Uint32(b[h+64:])&^0xff|0xc)
		path = filepath.Join(t.TempDir(), "gb.o")
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return path, b
}

func TestMachOTerminalBSS(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		for _, variant := range []string{"zero", "gb"} {
			t.Run(target+"/"+variant, func(t *testing.T) {
				_, b := machoTerminalBSS(t, target, variant)
				weak := parseMachoAliases(t, b)
				a := assertMachOTerminal(t, weak, 0)
				if weak.sections[a.section].data != nil {
					t.Fatal("zero-fill storage acquired file bytes")
				}
				symbols := append([]symbol(nil), weak.symbols...)
				for mode := 0; mode < 4; mode++ {
					for _, order := range []string{"weak_first", "strong_first"} {
						t.Run(fmt.Sprintf("override%d/%s", mode, order), func(t *testing.T) {
							inputs := []*object{weak}
							for i, name := range []string{"term_owner", "term_marker"} {
								if mode&(1<<i) != 0 {
									p := &object{info: weak.info, symbols: []symbol{{name: name, section: -1, value: uint64(0x123400 + 256*i), global: true}}}
									if order == "strong_first" {
										inputs = append([]*object{p}, inputs...)
									} else {
										inputs = append(inputs, p)
									}
								}
							}
							selected, err := coalesceObjects(inputs)
							if err != nil {
								t.Fatal(err)
							}
							im := machoCoalescedImage(t, selected)
							owner, e1 := im.lookup("term_owner")
							marker, e2 := im.lookup("term_marker")
							refs, e3 := im.lookup("term_refs")
							if e1 != nil || e2 != nil || e3 != nil || mode&1 != 0 && owner != 0x123400 || mode&2 != 0 && marker != 0x123500 {
								t.Fatal("BSS terminal resolution", e1, e2, e3)
							}
							for i, want := range []uintptr{owner, marker, marker, marker, owner + 8, owner + 24} {
								if got := le.Uint64(im.mem[refs-im.base+uintptr(8*i):]); got != uint64(want) {
									t.Fatal("BSS terminal pointer", i, got, want)
								}
							}
							if !reflect.DeepEqual(symbols, weak.symbols) {
								t.Fatal("BSS selection mutated staged definitions")
							}
						})
					}
				}
			})
		}
	}
}

func TestNativeMachOTerminalBSS(t *testing.T) {
	target := needMachOCoalescedNative(t)
	sdk := machoCoalescedSDK(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			providers := machoTerminalProviders(t, target, false)
			for _, variant := range []string{"zero", "gb"} {
				weak, b := machoTerminalBSS(t, target, variant)
				// Keep actual zero-fill headers even when the system archiver
				// rejects S_GB_ZEROFILL.
				archive := filepath.Join(t.TempDir(), "zero.a")
				if err := os.WriteFile(archive, append([]byte("!<arch>\n"), archiveMemberBytes("zero.o/", b)...), 0600); err != nil {
					t.Fatal(err)
				}
				for mode := 0; mode < 4; mode++ {
					flags := append(machoTerminalFlags("regular", 0, mode, opt), "-DDYLIB_TERM_ZERO")
					probe, _ := machoCoalescedFixture(t, target, "macho_terminal.c", flags...)
					for _, input := range []string{"objects", "archive"} {
						t.Run(fmt.Sprintf("%s/override%d/%s", variant, mode, input), func(t *testing.T) {
							path := weak
							if input == "archive" {
								path = archive
							}
							inputs := []string{probe, path}
							chosen := machoTerminalChosen(providers, 0, mode)
							if input == "archive" {
								inputs = append(chosen, inputs...)
							} else {
								inputs = append(inputs, chosen...)
							}
							s := New(Options{})
							defer s.Close()
							load(t, s, inputs...)
							for n := int32(0); n < 5; n++ {
								call(t, s, "term_check", int32(mode), 42+n, 42+n)
							}
						})
					}
					if variant == "zero" {
						t.Run(fmt.Sprintf("zero/override%d/system_control", mode), func(t *testing.T) {
							// Older Apple linkers reject raw weak BSS replacement.
							// Use equivalent regular zero storage for this control.
							control, _ := machoCoalescedFixture(t, target, "macho_terminal.S", append(flags, "-DDYLIB_TERM_ZERO_CONTROL")...)
							inputs := append([]string{control}, machoTerminalChosen(providers, 0, mode)...)
							if opt == "-O2" {
								inputs = append(machoTerminalChosen(providers, 0, mode), control)
							}
							exe := filepath.Join(t.TempDir(), "control")
							args := []string{"--target=" + target, "-isysroot", sdk, "-Wl,-syslibroot," + sdk, "-Wl,-dead_strip", "testdata/macho_terminal.c", "-DDYLIB_TERM_STANDALONE"}
							args = append(args, flags...)
							command(t, compiler(), append(append(args, inputs...), "-o", exe)...)
							command(t, exe)
						})
					}
				}
			}
		})
	}
}

func TestNativeMachOTerminalRetry(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		for _, kind := range []string{"regular", "coalesced"} {
			for _, input := range []string{"objects", "archive"} {
				t.Run(opt+"/"+kind+"/"+input, func(t *testing.T) {
					flags := machoTerminalFlags(kind, 0, 3, opt)
					weak, _ := machoCoalescedFixture(t, target, "macho_terminal.S", append(flags, "-DDYLIB_TERM_POISON")...)
					probe, _ := machoCoalescedFixture(t, target, "macho_terminal.c", flags...)
					providers := machoTerminalProviders(t, target, false)
					if input == "archive" {
						weak = machoCoalescedArchive(t, weak)
					}
					s := New(Options{})
					defer s.Close()
					load(t, s, weak, providers[1])
					if err := s.Link("term_refs"); err == nil || !strings.Contains(err.Error(), "term_missing") || s.image != nil {
						t.Fatal("zero-byte override dropped the preceding body's dependency", err)
					}
					load(t, s, providers[0], probe)
					if err := s.Link("term_check"); err != nil {
						t.Fatal(err)
					}
					call(t, s, "term_check", 3, 42, 42)
				})
			}
		}
	}
}

func TestNativeMachOTerminalArchiveRoots(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, kind := range []string{"regular", "coalesced"} {
		for _, mode := range []string{"terminal_member", "strong_bypass"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				flags := machoTerminalFlags(kind, 0, 0, "-O2")
				weak, _ := machoCoalescedFixture(t, target, "macho_terminal.S", append(flags, "-DDYLIB_TERM_POISON")...)
				providers := machoTerminalProviders(t, target, false)
				s := New(Options{})
				defer s.Close()
				load(t, s, machoCoalescedArchive(t, weak))
				if mode == "terminal_member" {
					if err := s.Link("term_marker"); err == nil || !strings.Contains(err.Error(), "term_missing") || s.image != nil {
						t.Fatal("selected terminal member lost its retained body", err)
					}
					load(t, s, providers[0])
				} else {
					load(t, s, providers[1])
				}
				if _, err := s.Resolve("term_marker"); err != nil {
					t.Fatal(err)
				}
				want := 2
				if mode == "strong_bypass" {
					want = 1
				}
				if len(s.image.objects) != want {
					t.Fatal("terminal archive root selection", len(s.image.objects), want)
				}
			})
		}
	}
}
