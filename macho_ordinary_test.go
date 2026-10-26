package dylib

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func machoOrdinaryZero(t *testing.T, target, opt, variant string) (string, []string) {
	t.Helper()
	flags := []string{opt}
	if variant == "whole" {
		flags = append(flags, "-DDYLIB_ZERO_WHOLE", "-DDYLIB_ZERO_ALIASLESS")
	}
	path, b := machoCoalescedFixture(t, target, "macho_ordinary_zero.S", flags...)
	if variant == "gb" {
		header := machoPointerHeader(t, b, "__bss")
		le.PutUint32(b[header+64:], le.Uint32(b[header+64:])&^0xff|0xc)
		path = filepath.Join(t.TempDir(), "gb-zero.o")
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return path, flags
}

func TestMachOOrdinaryZeroFill(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		for _, variant := range []string{"partial", "whole", "gb", "retained"} {
			t.Run(target+"/"+variant, func(t *testing.T) {
				path, _ := machoOrdinaryZero(t, target, "-O0", variant)
				b, err := readFile(path)
				if err != nil {
					t.Fatal(err)
				}
				weak := parseMachoAliases(t, b)
				_, strongBytes := machoCoalescedFixture(t, target, "macho_ordinary_zero.c", "-DDYLIB_ZERO_STRONG")
				strong := parseMachoAliases(t, strongBytes)
				if len(weak.machoAtoms) != 1 || weak.sections[weak.machoAtoms[0].section].data != nil {
					t.Fatal("zero-fill weak atom was not modeled without backing bytes")
				}
				orders := []string{"weak_first", "strong_first"}
				if variant == "retained" {
					orders = []string{"weak_only"}
				}
				for _, order := range orders {
					t.Run(order, func(t *testing.T) {
						inputs := []*object{weak, strong}
						if order == "strong_first" {
							inputs = []*object{strong, weak}
						} else if order == "weak_only" {
							inputs = []*object{weak}
						}
						selected, err := coalesceObjects(inputs)
						if err != nil {
							t.Fatal(err)
						}
						im := machoCoalescedImage(t, selected)
						zero, err := im.lookup("ordinary_zero")
						if err != nil {
							t.Fatal(err)
						}
						want := []uint64{11, 31}
						if variant == "retained" {
							want = []uint64{0, 0}
						}
						for i, value := range want {
							if le.Uint64(im.mem[zero-im.base+uintptr(i)*8:]) != value {
								t.Fatal("zero-fill selection/data lost", i)
							}
						}
						if variant != "whole" {
							pointers, err := im.lookup("ordinary_zero_pointers")
							if err != nil {
								t.Fatal(err)
							}
							for i := 0; i < 3; i++ {
								if le.Uint64(im.mem[pointers-im.base+uintptr(i)*8:]) != uint64(zero)+uint64(i)*8 {
									t.Fatal("BSS interior/one-past pointer lost", i)
								}
							}
						} else {
							chosen := selected[0]
							if order == "strong_first" {
								chosen = selected[1]
							}
							if chosen.sections[weak.machoAtoms[0].section] != nil {
								t.Fatal("whole discarded BSS still owns storage")
							}
						}
						if weak.sections[weak.machoAtoms[0].section] == nil {
							t.Fatal("selection changed original BSS")
						}
					})
				}
			})
		}
	}
}

func TestMachOOrdinaryBoundaries(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		for _, mode := range []string{"no_subsections", "same_address", "mixed_address", "public_alternate", "zero_size"} {
			t.Run(target+"/"+mode, func(t *testing.T) {
				source := ".data\n.globl _ordinary_entry\n.weak_definition _ordinary_entry\n_ordinary_entry:\n.quad _ordinary_missing\n"
				switch mode {
				case "same_address", "mixed_address":
					source = ".data\n.globl _ordinary_entry,_ordinary_other\n.weak_definition _ordinary_other\n"
					if mode == "same_address" {
						source += ".weak_definition _ordinary_entry\n"
					}
					source += "_ordinary_entry:\n_ordinary_other:\n.quad _ordinary_missing\n"
				case "public_alternate":
					source += ".globl _ordinary_other\n.weak_definition _ordinary_other\n.alt_entry _ordinary_other\n_ordinary_other:\n.quad _ordinary_missing\n"
				case "zero_size":
					source += ".globl _ordinary_empty\n.weak_definition _ordinary_empty\n_ordinary_empty:\n"
				}
				if mode != "zero_size" {
					source += ".globl _ordinary_sentinel\n.weak_definition _ordinary_sentinel\n_ordinary_sentinel:\n.quad _ordinary_other_missing\n"
				}
				if mode != "no_subsections" {
					source += ".subsections_via_symbols\n"
				}
				path := machoCoalescedAssembly(t, target, source)
				b, err := readFile(path)
				if err != nil {
					t.Fatal(err)
				}
				o := parseMachoAliases(t, b)
				wantAtoms := 2
				if mode == "zero_size" {
					wantAtoms = 1
				}
				if mode == "no_subsections" {
					wantAtoms = 0
				}
				if len(o.machoAtoms) != wantAtoms {
					t.Fatal("unexpected bounded alias ownership", len(o.machoAtoms))
				}
				relocs, symbols := append([]relocation(nil), o.relocs...), append([]symbol(nil), o.symbols...)
				name := "ordinary_sentinel"
				if mode == "zero_size" {
					name = "ordinary_entry"
				}
				provider := &object{info: o.info, symbols: []symbol{{name: name, global: true, section: -1, value: 0x123400}}}
				selected, err := coalesceObjects([]*object{o, provider})
				if err != nil {
					t.Fatal(err)
				}
				refs, err := references(selected[0])
				if err != nil {
					t.Fatal(err)
				}
				missing, other := false, false
				for _, r := range refs {
					missing = missing || r.name == "ordinary_missing"
					other = other || r.name == "ordinary_other_missing"
				}
				if missing != (mode != "zero_size") || other != (mode == "no_subsections") {
					t.Fatal("unqualified storage or its dependencies were discarded", missing, other)
				}
				if !reflect.DeepEqual(relocs, o.relocs) || !reflect.DeepEqual(symbols, o.symbols) {
					t.Fatal("boundary selection changed original symbols/fixups")
				}
			})
		}
	}
}

func TestNativeMachOOrdinaryZeroFill(t *testing.T) {
	target := needMachOCoalescedNative(t)
	sdk := machoCoalescedSDK(t)
	for _, opt := range []string{"-O0", "-O2"} {
		for _, variant := range []string{"partial", "whole", "gb", "retained"} {
			t.Run(opt+"/"+variant, func(t *testing.T) {
				weak, flags := machoOrdinaryZero(t, target, opt, variant)
				strong, _ := machoCoalescedFixture(t, target, "macho_ordinary_zero.c", opt, "-DDYLIB_ZERO_STRONG")
				if variant != "retained" {
					flags = append(flags, "-DDYLIB_ZERO_SELECTED")
				}
				probe, _ := machoCoalescedFixture(t, target, "macho_ordinary_zero.c", flags...)
				unused := machoCoalescedAssembly(t, target, ".data\n.globl _ordinary_unused\n_ordinary_unused:\n.quad _ordinary_poison\n")
				for _, kind := range []string{"objects", "archive", "system_control"} {
					t.Run(kind, func(t *testing.T) {
						if kind == "system_control" {
							// Older Apple linkers reject weak zero-fill replacements.
							// Compare the same values/pointers with regular zero storage;
							// loader object/archive calls above still use real zero-fill.
							controlFlags := append([]string{"-DDYLIB_ZERO_CONTROL"}, flags...)
							control, _ := machoCoalescedFixture(t, target, "macho_ordinary_zero.c", controlFlags...)
							exe := filepath.Join(t.TempDir(), "control")
							args := []string{"--target=" + target, "-isysroot", sdk, "-Wl,-syslibroot," + sdk, "-Wl,-dead_strip", "testdata/macho_ordinary_zero.c", "-DDYLIB_ZERO_STANDALONE"}
							args = append(args, flags...)
							args = append(args, control)
							if variant != "retained" {
								args = append(args, strong)
							}
							command(t, compiler(), append(args, "-o", exe)...)
							command(t, exe)
							return
						}
						s := New(Options{})
						defer s.Close()
						inputs := []string{weak, probe}
						if kind == "archive" {
							// LLVM and older Apple archivers reject S_GB_ZEROFILL.
							// Keep the genuine objects in an indexless ar container.
							archive := filepath.Join(t.TempDir(), "zero.a")
							data := []byte("!<arch>\n")
							for _, input := range []string{unused, weak} {
								b, err := os.ReadFile(input)
								if err != nil {
									t.Fatal(err)
								}
								data = append(data, archiveMemberBytes(filepath.Base(input)+"/", b)...)
							}
							if err := os.WriteFile(archive, data, 0600); err != nil {
								t.Fatal(err)
							}
							inputs = []string{probe, archive}
						}
						if variant != "retained" {
							if kind == "archive" {
								inputs = append([]string{strong}, inputs...)
							} else {
								inputs = append(inputs, strong)
							}
						}
						load(t, s, inputs...)
						for i := int32(0); i < 5; i++ {
							call(t, s, "ordinary_zero_check", 20+i, 22, 42+i)
						}
						wantObjects := 2
						if variant != "retained" && (variant != "whole" || kind != "archive") {
							wantObjects = 3
						}
						if len(s.image.objects) != wantObjects {
							t.Fatal("unused BSS archive provider selected")
						}
					})
				}
			})
		}
	}
}
