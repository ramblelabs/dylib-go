package dylib

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

var machoSharedForms = []string{"start_weak", "next_weak", "start_strong_owner", "next_strong_owner", "start_strong_alias", "next_strong_alias"}

func machoSharedModes(form int) []int {
	if form == 2 || form == 3 {
		return []int{0, 2}
	}
	if form >= 4 {
		return []int{0, 1}
	}
	return []int{0, 1, 2, 3}
}

func machoSharedFlags(kind string, form int, opt string) []string {
	return []string{opt, "-DDYLIB_SHARED_KIND=" + kind, fmt.Sprintf("-DDYLIB_SHARED_FORM=%d", form)}
}

func machoSharedProviders(t *testing.T, target string) []string {
	t.Helper()
	var paths []string
	for index, name := range []string{"owner", "zalias", "before"} {
		value := []int{42, 84, 99}[index]
		paths = append(paths, machoCoalescedAssembly(t, target, fmt.Sprintf(".data\n.p2align 3\n.globl _shared_%s\n_shared_%s:\n.quad %d,%d,%d\n", name, name, value, value+1, value+2)))
	}
	return paths
}

func TestMachOSharedPublicAliases(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		t.Run(target, func(t *testing.T) {
			providers := machoSharedProviders(t, target)
			strong := make([]*object, len(providers))
			for index, path := range providers {
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				strong[index] = parseMachoAliases(t, b)
			}
			aliasPath := machoCoalescedAssembly(t, target, ".globl _shared_alias_ref\n_shared_alias_ref = _shared_zalias\n")
			aliasBytes, err := os.ReadFile(aliasPath)
			if err != nil {
				t.Fatal(err)
			}
			alias := parseMachoAliases(t, aliasBytes)
			for _, kind := range []string{"regular", "coalesced"} {
				for form, name := range machoSharedForms {
					t.Run(kind+"/"+name, func(t *testing.T) {
						_, original := machoCoalescedFixture(t, target, "macho_shared_aliases.S", machoSharedFlags(kind, form, "-O0")...)
						for _, table := range []string{"normal", "reverse"} {
							t.Run(table, func(t *testing.T) {
								b := original
								if table == "reverse" {
									b = machoReversePublicSymbols(t, b, "_shared_owner", "_shared_zalias")
								}
								weak := parseMachoAliases(t, b)
								wantAtoms := 1 + form%2
								if len(weak.machoAtoms) != wantAtoms {
									t.Fatal("shared public boundaries", weak.machoAtoms)
								}
								atom := weak.machoAtoms[len(weak.machoAtoms)-1]
								if weak.symbols[atom.key].name != "shared_owner" {
									t.Fatal("alternate entry became storage owner", weak.symbols[atom.key])
								}
								savedSymbols := append([]symbol(nil), weak.symbols...)
								savedRelocs := append([]relocation(nil), weak.relocs...)
								for _, mode := range machoSharedModes(form) {
									for _, order := range []string{"weak_first", "strong_first"} {
										t.Run(fmt.Sprintf("override%d/%s", mode, order), func(t *testing.T) {
											inputs := []*object{weak, alias}
											if form%2 != 0 {
												inputs = append(inputs, strong[2])
											}
											for i := 0; i < 2; i++ {
												if mode&(1<<i) != 0 {
													inputs = append(inputs, strong[i])
												}
											}
											if order == "strong_first" {
												inputs = append(inputs[1:], weak)
											}
											selected, err := coalesceObjects(inputs)
											if err != nil {
												t.Fatal(err)
											}
											im := machoCoalescedImage(t, selected)
											address := func(name string) uintptr {
												p, err := im.lookup(name)
												if err != nil {
													t.Fatal(name, err)
												}
												return p
											}
											a, b := address("shared_owner"), address("shared_zalias")
											if address("shared_alias_ref") != b {
												t.Fatal("N_INDR shared alias")
											}
											wantA, wantB := uint64(9), uint64(9)
											if mode&1 != 0 {
												wantA, wantB = 42, 42
											}
											if mode&2 != 0 {
												wantB = 84
											}
											if le.Uint64(im.mem[a-im.base:]) != wantA || le.Uint64(im.mem[b-im.base:]) != wantB {
												t.Fatal("shared alias selection", mode, a, b)
											}
											before := a
											if form%2 != 0 {
												before = address("shared_before")
											}
											refs := address("shared_refs")
											for index, want := range []uintptr{a, b, a, a + 8, a + 24, before} {
												if got := le.Uint64(im.mem[refs-im.base+uintptr(index)*8:]); got != uint64(want) {
													t.Fatal("shared private pointer", index, got, want)
												}
											}
											deltas := address("shared_deltas")
											for index, want := range []uint64{uint64(a) - uint64(refs), uint64(refs) - uint64(a), uint64(b) - uint64(refs), uint64(refs) - uint64(b), uint64(a+8) - uint64(refs), uint64(refs) - uint64(a+8)} {
												if got := le.Uint64(im.mem[deltas-im.base+uintptr(index)*8:]); got != want {
													t.Fatal("shared subtractor", index, got, want)
												}
											}
											for _, value := range []uintptr{a, b, a + 8} {
												if im.got[value] == 0 {
													t.Fatal("missing shared GOT entry", value)
												}
											}
											if !reflect.DeepEqual(savedSymbols, weak.symbols) || !reflect.DeepEqual(savedRelocs, weak.relocs) {
												t.Fatal("shared selection changed staged input")
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

func TestNativeMachOSharedPublicAliases(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			providers := machoSharedProviders(t, target)
			unused := machoCoalescedAssembly(t, target, ".data\n.globl _shared_unused\n_shared_unused:\n.quad _shared_poison\n")
			for _, kind := range []string{"regular", "coalesced"} {
				for form, name := range machoSharedForms {
					t.Run(kind+"/"+name, func(t *testing.T) {
						flags := machoSharedFlags(kind, form, opt)
						_, original := machoCoalescedFixture(t, target, "macho_shared_aliases.S", flags...)
						probe, _ := machoCoalescedFixture(t, target, "macho_shared_aliases.c", flags...)
						for _, table := range []string{"normal", "reverse"} {
							t.Run(table, func(t *testing.T) {
								b := original
								if table == "reverse" {
									b = machoReversePublicSymbols(t, b, "_shared_owner", "_shared_zalias")
								}
								weak := filepath.Join(t.TempDir(), "shared.o")
								if err := os.WriteFile(weak, b, 0600); err != nil {
									t.Fatal(err)
								}
								archive := machoCoalescedArchive(t, unused, weak)
								for _, mode := range machoSharedModes(form) {
									for _, order := range []string{"weak_first", "strong_first"} {
										for _, input := range []string{"objects", "archive"} {
											t.Run(fmt.Sprintf("override%d/%s/%s", mode, order, input), func(t *testing.T) {
												path := weak
												if input == "archive" {
													path = archive
												}
												inputs := []string{probe, path}
												var chosen []string
												if form%2 != 0 {
													chosen = append(chosen, providers[2])
												}
												for i := 0; i < 2; i++ {
													if mode&(1<<i) != 0 {
														chosen = append(chosen, providers[i])
													}
												}
												if order == "strong_first" {
													inputs = append(chosen, inputs...)
												} else {
													inputs = append(inputs, chosen...)
												}
												s := New(Options{})
												defer s.Close()
												load(t, s, inputs...)
												for n := int32(0); n < 5; n++ {
													call(t, s, "shared_check", int32(mode), 42+n, 42+n)
												}
												if len(s.image.objects) != 2+len(chosen) {
													t.Fatal("unrelated shared archive member selected")
												}
											})
										}
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

func TestNativeMachOSharedPublicControls(t *testing.T) {
	target := needMachOCoalescedNative(t)
	sdk := machoCoalescedSDK(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			providers := machoSharedProviders(t, target)
			for _, kind := range []string{"regular", "coalesced"} {
				for form, name := range machoSharedForms {
					t.Run(kind+"/"+name, func(t *testing.T) {
						flags := machoSharedFlags(kind, form, opt)
						weak, _ := machoCoalescedFixture(t, target, "macho_shared_aliases.S", flags...)
						for _, mode := range machoSharedModes(form) {
							t.Run(fmt.Sprintf("override%d", mode), func(t *testing.T) {
								var chosen []string
								if form%2 != 0 {
									chosen = append(chosen, providers[2])
								}
								for i := 0; i < 2; i++ {
									if mode&(1<<i) != 0 {
										chosen = append(chosen, providers[i])
									}
								}
								inputs := append([]string{weak}, chosen...)
								if opt == "-O2" {
									inputs = append(chosen, weak)
								}
								exe := filepath.Join(t.TempDir(), "control")
								args := []string{"--target=" + target, "-isysroot", sdk, "-Wl,-syslibroot," + sdk, "-Wl,-dead_strip", "testdata/macho_shared_aliases.c", "-DDYLIB_SHARED_STANDALONE", fmt.Sprintf("-DDYLIB_SHARED_OVERRIDE=%d", mode)}
								args = append(args, flags...)
								args = append(args, inputs...)
								command(t, compiler(), append(args, "-o", exe)...)
								command(t, exe)
							})
						}
					})
				}
			}
		})
	}
}
