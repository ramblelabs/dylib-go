package dylib

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var machoTerminalForms = []string{"single", "pair", "strong_alternate", "weak_alternate", "strong_end"}

func machoTerminalModes(form int) []int {
	if form == 1 {
		return []int{0, 1, 2, 3, 4, 5, 6, 7}
	}
	if form == 3 {
		return []int{0, 1, 4, 5}
	}
	if form == 4 {
		return []int{0, 1}
	}
	return []int{0, 1, 2, 3}
}

func machoTerminalFlags(kind string, form, mode int, opt string) []string {
	return []string{opt, "-DDYLIB_TERM_KIND=" + kind, fmt.Sprintf("-DDYLIB_TERM_FORM=%d", form), fmt.Sprintf("-DDYLIB_TERM_MODE=%d", mode)}
}

func machoTerminalProviders(t *testing.T, target string, code bool) []string {
	t.Helper()
	var paths []string
	for i, name := range []string{"owner", "marker", "zmarker", "alt"} {
		value := []int{42, 84, 126, 126}[i]
		section, body := ".data", fmt.Sprintf(".quad %d,%d,%d\n", value, value+1, value+2)
		if code {
			section = ".text"
			body = fmt.Sprintf(".byte 0x0f,0x1f,0x40,0x00\nleal %d(%%rdi,%%rsi),%%eax\nretq\n", value)
			if strings.HasPrefix(target, "arm64") {
				body = fmt.Sprintf("nop\nadd w0,w0,w1\nadd w0,w0,#%d\nret\n", value)
			}
		}
		// Real bytes on both sides prevent accidental address equality with
		// an original terminal boundary in system-linked controls.
		text := section + "\n.p2align 3\n.space 64\n.globl _term_" + name + ",_term_" + name + "_storage\n_term_" + name + ":\n_term_" + name + "_storage:\n" + body + ".space 64\n"
		paths = append(paths, machoCoalescedAssembly(t, target, text))
	}
	return paths
}

func machoTerminalChosen(providers []string, form, mode int) []string {
	var chosen []string
	if mode&1 != 0 {
		chosen = append(chosen, providers[0])
	}
	if mode&2 != 0 {
		chosen = append(chosen, providers[1])
	}
	if mode&4 != 0 {
		index := 2
		if form == 3 {
			index = 3
		}
		chosen = append(chosen, providers[index])
	}
	return chosen
}

func machoTerminalReverse(t *testing.T, b []byte, form int) []byte {
	t.Helper()
	a, z := "_term_owner", "_term_marker"
	if form == 1 {
		a, z = "_term_marker", "_term_zmarker"
	} else if form == 2 || form == 3 {
		a, z = "_term_marker", "_term_alt"
	}
	return machoReversePublicSymbols(t, b, a, z)
}

func assertMachOTerminal(t *testing.T, o *object, form int) machoCoalescedAtom {
	t.Helper()
	if len(o.machoAtoms) != 2 {
		t.Fatal("body and terminal boundaries missing", o.machoAtoms)
	}
	a := o.machoAtoms[1]
	name := "term_marker"
	if form == 1 {
		name = "term_zmarker"
	}
	if a.start != a.end || a.start != o.sections[a.section].size || o.symbols[a.key].name != name || !o.symbols[a.key].global || o.symbols[a.key].weak != (form < 3) {
		t.Fatal("incorrect terminal storage identity", a)
	}
	for _, s := range o.symbols {
		alias := s.name == "term_marker" && form == 1 || s.name == "term_alt" && (form == 2 || form == 3)
		if alias && (!s.global || s.forward == nil || !s.forward.storage || s.forward.target != a.key || s.forward.offset != 0 || s.weak != (form != 2)) {
			t.Fatal("terminal fixture has an incorrect public alias", s)
		}
	}
	return a
}

func TestMachOTerminalOwners(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		t.Run(target, func(t *testing.T) {
			paths := machoTerminalProviders(t, target, false)
			var providers []*object
			for _, path := range paths {
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				providers = append(providers, parseMachoAliases(t, b))
			}
			alias := machoCoalescedAssembly(t, target, ".globl _term_alias\n_term_alias = _term_marker\n")
			b, err := os.ReadFile(alias)
			if err != nil {
				t.Fatal(err)
			}
			ao := parseMachoAliases(t, b)
			for _, kind := range []string{"regular", "coalesced"} {
				for form, name := range machoTerminalForms {
					t.Run(kind+"/"+name, func(t *testing.T) {
						_, original := machoCoalescedFixture(t, target, "macho_terminal.S", machoTerminalFlags(kind, form, 0, "-O0")...)
						for _, table := range []string{"normal", "reverse"} {
							t.Run(table, func(t *testing.T) {
								b := original
								if table == "reverse" {
									b = machoTerminalReverse(t, b, form)
								}
								weak := parseMachoAliases(t, b)
								terminal := assertMachOTerminal(t, weak, form)
								symbols, relocs := append([]symbol(nil), weak.symbols...), append([]relocation(nil), weak.relocs...)
								for _, mode := range machoTerminalModes(form) {
									for _, order := range []string{"weak_first", "strong_first"} {
										t.Run(fmt.Sprintf("override%d/%s", mode, order), func(t *testing.T) {
											inputs := []*object{weak, ao}
											var chosen []*object
											for _, path := range machoTerminalChosen(paths, form, mode) {
												for i, p := range paths {
													if p == path {
														chosen = append(chosen, providers[i])
													}
												}
											}
											index := 0
											if order == "strong_first" {
												index = len(chosen)
												inputs = append(chosen, inputs...)
											} else {
												inputs = append(inputs, chosen...)
											}
											selected, err := coalesceObjects(inputs)
											if err != nil {
												t.Fatal(err)
											}
											im := machoCoalescedImage(t, selected)
											lookup := func(name string) uintptr {
												p, err := im.lookup(name)
												if err != nil {
													t.Fatal(err)
												}
												return p
											}
											owner, marker := lookup("term_owner"), lookup("term_marker")
											canonical, extra := marker, marker
											if form == 1 {
												canonical, extra = lookup("term_zmarker"), lookup("term_zmarker")
											} else if form == 2 || form == 3 {
												extra = lookup("term_alt")
											}
											if mode&1 != 0 && owner != lookup("term_owner_storage") || mode&2 != 0 && marker != lookup("term_marker_storage") || lookup("term_alias") != marker {
												t.Fatal("terminal public-name selection")
											}
											if form == 1 && mode&4 != 0 && canonical != lookup("term_zmarker_storage") || form == 3 && mode&4 != 0 && extra != lookup("term_alt_storage") {
												t.Fatal("independent terminal alias selection")
											}
											terminalReplaced := form == 1 && mode&4 != 0 || form != 1 && mode&2 != 0
											sec := selected[index].sections[terminal.section]
											if !terminalReplaced && (sec == nil || canonical != im.base+uintptr(sec.offset+terminal.start)) {
												t.Fatal("unreplaced terminal followed the preceding body")
											}
											if mode&1 != 0 && terminalReplaced && sec != nil {
												t.Fatal("fully discarded body/end retained original storage")
											}
											refs := lookup("term_refs")
											for i, want := range []uintptr{owner, marker, canonical, extra, owner + 8, owner + 24} {
												if got := le.Uint64(im.mem[refs-im.base+uintptr(i)*8:]); got != uint64(want) {
													t.Fatal("terminal pointer identity", i, got, want)
												}
											}
											for _, address := range []uintptr{marker, canonical} {
												cell, ok := im.got[address]
												if !ok || le.Uint64(im.mem[cell-im.base:]) != uint64(address) {
													t.Fatal("terminal GOT target", address, cell)
												}
											}
											deltas := lookup("term_deltas")
											for i, want := range []uint64{uint64(canonical) - uint64(refs), uint64(refs) - uint64(canonical), uint64(owner) - uint64(refs), uint64(refs) - uint64(owner)} {
												if got := le.Uint64(im.mem[deltas-im.base+uintptr(i)*8:]); got != want {
													t.Fatal("terminal subtractor", i, got, want)
												}
											}
											if !reflect.DeepEqual(symbols, weak.symbols) || !reflect.DeepEqual(relocs, weak.relocs) {
												t.Fatal("terminal selection mutated staged metadata")
											}
										})
									}
								}
							})
						}
					})
				}
			}
		})
	}
}

func TestNativeMachOTerminalOwners(t *testing.T) { testNativeMachOTerminal(t, false) }
func TestNativeMachOTerminalCode(t *testing.T)   { testNativeMachOTerminal(t, true) }

func testNativeMachOTerminal(t *testing.T, code bool) {
	target := needMachOCoalescedNative(t)
	sdk := machoCoalescedSDK(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			providers := machoTerminalProviders(t, target, code)
			unused := machoCoalescedAssembly(t, target, ".data\n.globl _term_unused\n_term_unused:\n.quad _term_unused_missing\n")
			for _, kind := range []string{"regular", "coalesced"} {
				for form, name := range machoTerminalForms {
					if code && form != 0 {
						continue
					}
					t.Run(kind+"/"+name, func(t *testing.T) {
						flags := machoTerminalFlags(kind, form, 0, opt)
						if code {
							flags = append(flags, "-DDYLIB_TERM_CODE")
						}
						weak, original := machoCoalescedFixture(t, target, "macho_terminal.S", flags...)
						assertMachOTerminal(t, parseMachoAliases(t, original), form)
						for _, mode := range machoTerminalModes(form) {
							t.Run(fmt.Sprintf("override%d", mode), func(t *testing.T) {
								f := append(append([]string(nil), flags...), "-UDYLIB_TERM_MODE", fmt.Sprintf("-DDYLIB_TERM_MODE=%d", mode))
								probe, _ := machoCoalescedFixture(t, target, "macho_terminal.c", f...)
								chosen := machoTerminalChosen(providers, form, mode)
								for _, table := range []string{"normal", "reverse"} {
									if code && table == "reverse" {
										continue
									}
									t.Run(table, func(t *testing.T) {
										path := weak
										if table == "reverse" {
											path = filepath.Join(t.TempDir(), "reversed.o")
											if err := os.WriteFile(path, machoTerminalReverse(t, original, form), 0600); err != nil {
												t.Fatal(err)
											}
										}
										archive := machoCoalescedArchive(t, unused, path)
										for _, order := range []string{"weak_first", "strong_first"} {
											for _, input := range []string{"objects", "archive"} {
												t.Run(order+"/"+input, func(t *testing.T) {
													p := path
													if input == "archive" {
														p = archive
													}
													inputs := []string{probe, p}
													if order == "strong_first" {
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
													if len(s.image.objects) != 2+len(chosen) {
														t.Fatal("unused terminal archive member selected")
													}
												})
											}
										}
									})
								}
								t.Run("system_control", func(t *testing.T) {
									inputs := append([]string{weak}, chosen...)
									if opt == "-O2" {
										inputs = append(chosen, weak)
									}
									exe := filepath.Join(t.TempDir(), "control")
									args := []string{"--target=" + target, "-isysroot", sdk, "-Wl,-syslibroot," + sdk, "-Wl,-dead_strip", "testdata/macho_terminal.c", "-DDYLIB_TERM_STANDALONE"}
									args = append(args, f...)
									command(t, compiler(), append(append(args, inputs...), "-o", exe)...)
									command(t, exe)
								})
							})
						}
					})
				}
			}
		})
	}
}
