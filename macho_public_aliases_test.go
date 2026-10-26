package dylib

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

var machoPublicForms = []string{"same_weak", "same_mixed", "same_weak_b", "alt_weak", "alt_strong", "end_weak"}

func machoPublicModes(form int) []int {
	if form == 1 || form == 4 {
		return []int{0, 1}
	}
	if form == 2 {
		return []int{0, 2}
	}
	return []int{0, 1, 2, 3}
}

func machoPublicProviders(t *testing.T, target string) []string {
	t.Helper()
	var paths []string
	for i, name := range []string{"a", "b"} {
		value := 42 * (i + 1)
		paths = append(paths, machoCoalescedAssembly(t, target, fmt.Sprintf(".data\n.p2align 4\n.globl _public_%s\n_public_%s:\n.quad %d,%d,%d\n", name, name, value, value+1, value+2)))
	}
	return paths
}

func machoPublicReverse(t *testing.T, input []byte) []byte {
	t.Helper()
	b := append([]byte(nil), input...)
	f := machoPointerMetadata(t, b)
	a, c := -1, -1
	for i, s := range f.Symtab.Syms {
		if s.Name == "_public_a" {
			a = i
		}
		if s.Name == "_public_b" {
			c = i
		}
	}
	if a < 0 || c < 0 {
		t.Fatal("missing public alias symbols")
	}
	off := int(le.Uint32(f.Symtab.Raw()[8:]))
	entry := append([]byte(nil), b[off+a*16:off+(a+1)*16]...)
	copy(b[off+a*16:off+(a+1)*16], b[off+c*16:off+(c+1)*16])
	copy(b[off+c*16:off+(c+1)*16], entry)
	for _, s := range f.Sections {
		for i, r := range s.Relocs {
			if !r.Extern {
				continue
			}
			value := r.Value
			if int(value) == a {
				value = uint32(c)
			} else if int(value) == c {
				value = uint32(a)
			} else {
				continue
			}
			field := int(s.Reloff) + i*8 + 4
			le.PutUint32(b[field:], le.Uint32(b[field:])&0xff000000|value)
		}
	}
	return b
}

func machoPublicFlags(kind string, form int, opt string) []string {
	return []string{opt, "-DDYLIB_PUBLIC_KIND=" + kind, fmt.Sprintf("-DDYLIB_PUBLIC_FORM=%d", form)}
}

func TestMachOPublicAliases(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		t.Run(target, func(t *testing.T) {
			providers := machoPublicProviders(t, target)
			aliasPath := machoCoalescedAssembly(t, target, ".globl _public_b_ref\n_public_b_ref = _public_b\n")
			aliasBytes, err := os.ReadFile(aliasPath)
			if err != nil {
				t.Fatal(err)
			}
			alias := parseMachoAliases(t, aliasBytes)
			strong := make([]*object, 2)
			for i, path := range providers {
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				strong[i] = parseMachoAliases(t, b)
			}
			for _, kind := range []string{"regular", "coalesced"} {
				t.Run(kind, func(t *testing.T) {
					for form, name := range machoPublicForms {
						t.Run(name, func(t *testing.T) {
							_, original := machoCoalescedFixture(t, target, "macho_public_aliases.S", machoPublicFlags(kind, form, "-O0")...)
							for _, table := range []string{"normal", "reverse"} {
								t.Run(table, func(t *testing.T) {
									b := original
									if table == "reverse" {
										b = machoPublicReverse(t, b)
									}
									weak := parseMachoAliases(t, b)
									if len(weak.machoAtoms) != 1 {
										t.Fatal("missing public group", weak.machoAtoms)
									}
									atom := weak.machoAtoms[0]
									wantKey := "public_b"
									if form >= 2 {
										wantKey = "public_a"
									}
									if weak.symbols[atom.key].name != wantKey {
										t.Fatal("unstable canonical storage", weak.symbols[atom.key])
									}
									savedSymbols := append([]symbol(nil), weak.symbols...)
									savedRelocs := append([]relocation(nil), weak.relocs...)
									for _, mode := range machoPublicModes(form) {
										for _, order := range []string{"weak_first", "strong_first"} {
											t.Run(fmt.Sprintf("override%d/%s", mode, order), func(t *testing.T) {
												inputs := []*object{weak, alias}
												for i, p := range strong {
													if mode&(1<<i) != 0 {
														inputs = append(inputs, p)
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
												addr := func(name string) uintptr {
													p, err := im.lookup(name)
													if err != nil {
														t.Fatal(name, err)
													}
													return p
												}
												a, b := addr("public_a"), addr("public_b")
												if addr("public_b_ref") != b {
													t.Fatal("N_INDR interior address")
												}
												owner := b
												if form >= 2 {
													owner = a
												}
												refs := addr("public_refs")
												for i, want := range []uintptr{a, b, owner, owner + 8, owner + 24} {
													if got := le.Uint64(im.mem[refs-im.base+uintptr(i)*8:]); got != uint64(want) {
														t.Fatalf("pointer %d: %#x want %#x", i, got, want)
													}
												}
												delta := addr("public_deltas")
												for i, want := range []uint64{uint64(a) - uint64(refs), uint64(refs) - uint64(a), uint64(b) - uint64(refs), uint64(refs) - uint64(b), uint64(owner+8) - uint64(refs), uint64(refs) - uint64(owner+8)} {
													if got := le.Uint64(im.mem[delta-im.base+uintptr(i)*8:]); got != want {
														t.Fatalf("subtractor %d: %#x want %#x", i, got, want)
													}
												}
												if im.got[a] == 0 || im.got[b] == 0 {
													t.Fatal("public alias GOT cells were not populated")
												}
												wantA, wantB := uint64(9), uint64(9)
												if form >= 3 {
													wantA = 7
												}
												if mode&1 != 0 {
													wantA = 42
													if form >= 3 {
														wantB = 43
													}
												}
												if mode&2 != 0 {
													wantB = 84
													if form == 0 && mode&1 == 0 {
														wantA = 84
													}
												}
												if got := le.Uint64(im.mem[a-im.base:]); got != wantA {
													t.Fatal("public a selection", got, wantA)
												}
												if form != 5 {
													if got := le.Uint64(im.mem[b-im.base:]); got != wantB {
														t.Fatal("public b selection", got, wantB)
													}
												} else if mode&2 == 0 && b != owner+24 {
													t.Fatal("one-past public alias")
												}
												if !reflect.DeepEqual(savedSymbols, weak.symbols) || !reflect.DeepEqual(savedRelocs, weak.relocs) {
													t.Fatal("alias selection changed staged input")
												}
											})
										}
									}
								})
							}
						})
					}
				})
			}
		})
	}
}

func TestNativeMachOPublicAliases(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			providers := machoPublicProviders(t, target)
			for _, kind := range []string{"regular", "coalesced"} {
				t.Run(kind, func(t *testing.T) {
					for form, name := range machoPublicForms {
						t.Run(name, func(t *testing.T) {
							flags := machoPublicFlags(kind, form, opt)
							weak, _ := machoCoalescedFixture(t, target, "macho_public_aliases.S", flags...)
							probe, _ := machoCoalescedFixture(t, target, "macho_public_aliases.c", flags...)
							unused := machoCoalescedAssembly(t, target, ".data\n.globl _public_unused\n_public_unused:\n.quad _public_poison\n")
							archive := machoCoalescedArchive(t, unused, weak)
							for _, mode := range machoPublicModes(form) {
								for _, order := range []string{"weak_first", "strong_first"} {
									for _, input := range []string{"objects", "archive"} {
										t.Run(fmt.Sprintf("override%d/%s/%s", mode, order, input), func(t *testing.T) {
											path := weak
											if input == "archive" {
												path = archive
											}
											inputs := []string{probe, path}
											var chosen []string
											for i, p := range providers {
												if mode&(1<<i) != 0 {
													chosen = append(chosen, p)
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
												call(t, s, "public_check", int32(mode), 42+n, 42+n)
											}
											if len(s.image.objects) != 2+len(chosen) {
												t.Fatal("unused alias archive member selected")
											}
										})
									}
								}
							}
						})
					}
				})
			}
		})
	}
}

func TestNativeMachOPublicAliasControls(t *testing.T) {
	target := needMachOCoalescedNative(t)
	sdk := machoCoalescedSDK(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			providers := machoPublicProviders(t, target)
			for _, kind := range []string{"regular", "coalesced"} {
				t.Run(kind, func(t *testing.T) {
					for form, name := range machoPublicForms {
						t.Run(name, func(t *testing.T) {
							flags := machoPublicFlags(kind, form, opt)
							weak, _ := machoCoalescedFixture(t, target, "macho_public_aliases.S", flags...)
							for _, mode := range machoPublicModes(form) {
								t.Run(fmt.Sprintf("override%d", mode), func(t *testing.T) {
									var selected []string
									for i, p := range providers {
										if mode&(1<<i) != 0 {
											selected = append(selected, p)
										}
									}
									inputs := append([]string{weak}, selected...)
									if opt == "-O2" {
										inputs = append(selected, weak)
									}
									exe := filepath.Join(t.TempDir(), "control")
									args := []string{"--target=" + target, "-isysroot", sdk, "-Wl,-syslibroot," + sdk, "-Wl,-dead_strip", "testdata/macho_public_aliases.c", "-DDYLIB_PUBLIC_STANDALONE", fmt.Sprintf("-DDYLIB_PUBLIC_OVERRIDE=%d", mode)}
									args = append(args, flags...)
									args = append(args, inputs...)
									command(t, compiler(), append(args, "-o", exe)...)
									command(t, exe)
								})
							}
						})
					}
				})
			}
		})
	}
}
