package dylib

import (
	"bytes"
	"debug/macho"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func machoCoalescedFixture(t *testing.T, target, source string, flags ...string) (string, []byte) {
	t.Helper()
	path := compile(t, "testdata/"+source, filepath.Join(t.TempDir(), "coalesced.o"), append([]string{"--target=" + target}, flags...)...)
	b, err := readFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, b
}

// Encode the equivalent section-relative form rather than LLVM's nlist form.
// Apple-linked controls execute these objects too, independently of our parser.
func machoCoalescedLocalBytes(t *testing.T, input []byte) []byte {
	t.Helper()
	b := append([]byte(nil), input...)
	f := machoPointerMetadata(t, b)
	coal := f.Section("__coal")
	ordinal := 0
	for i, section := range f.Sections {
		if section == coal {
			ordinal = i + 1
		}
	}
	middle := uint64(0)
	for _, s := range f.Symtab.Syms {
		if s.Name == "lcoal_middle" {
			middle = s.Value
		}
	}
	if ordinal == 0 || middle == 0 {
		t.Fatal("missing local reference producer")
	}
	dataSites, signedSites := 0, 0
	for _, sec := range f.Sections {
		for i, r := range sec.Relocs {
			data := sec.Name == "__data" && (r.Addr == 32 || r.Addr == 40)
			signed := f.Cpu == macho.CpuAmd64 && sec.Name == "__text" && (r.Type == 1 || r.Type >= 6 && r.Type <= 8)
			if !data && !signed {
				continue
			}
			bits := uint32(ordinal) | uint32(r.Type)<<28 | uint32(r.Len)<<25
			field := int(sec.Offset + r.Addr)
			if data {
				address := middle
				if r.Addr == 40 {
					address = coal.Addr + coal.Size
				}
				le.PutUint64(b[field:], address)
				dataSites++
			} else {
				bits |= 1 << 24
				end := sec.Addr + uint64(r.Addr) + 4
				if r.Type >= 6 {
					end += uint64(1) << (r.Type - 6)
				}
				le.PutUint32(b[field:], uint32(int64(middle)-int64(end)))
				signedSites++
			}
			le.PutUint32(b[int(sec.Reloff)+i*8+4:], bits)
		}
	}
	if dataSites != 2 || f.Cpu == macho.CpuAmd64 && signedSites != 4 {
		t.Fatal("missing section-relative sites", dataSites, signedSites)
	}
	return b
}

func machoCoalescedLocalFile(t *testing.T, path string) string {
	t.Helper()
	b, err := readFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "local.o")
	if err := os.WriteFile(out, machoCoalescedLocalBytes(t, b), 0600); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMachOCoalescedLocalReferences(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		t.Run(target, func(t *testing.T) {
			_, b := machoCoalescedFixture(t, target, "macho_coalesced.S")
			local := machoCoalescedLocalBytes(t, b)
			weak := parseMachoAliases(t, local)
			_, b = machoCoalescedFixture(t, target, "macho_coalesced_strong.c")
			strong := parseMachoAliases(t, b)
			discardUnwind(strong)
			objects, err := coalesceObjects([]*object{weak, strong})
			if err != nil {
				t.Fatal(err)
			}
			im := machoCoalescedImage(t, objects)
			anchor, _ := im.lookup("coal_anchor")
			record, _ := im.lookup("coal_record")
			for i, offset := range []uint64{8, 16} {
				if le.Uint64(im.mem[anchor-im.base+32+uintptr(i)*8:]) != uint64(record)+offset {
					t.Fatal("local or one-past pointer kept a discarded atom")
				}
			}
			for _, mode := range []string{"outside_target", "local_got"} {
				t.Run(mode, func(t *testing.T) {
					bad := append([]byte(nil), local...)
					f := machoPointerMetadata(t, bad)
					sec := f.Section("__data")
					if mode == "outside_target" {
						le.PutUint64(bad[int(sec.Offset)+32:], ^uint64(0))
					} else {
						for i, r := range sec.Relocs {
							if r.Addr == 32 {
								bits := le.Uint32(bad[int(sec.Reloff)+i*8+4:])
								le.PutUint32(bad[int(sec.Reloff)+i*8+4:], bits&0xffffff|1<<24|2<<25|3<<28)
							}
						}
					}
					parsed, err := parse("invalid-local.o", bad)
					if err == nil && len(parsed.info.Unsupported) == 0 {
						t.Fatal("unqualified local coalesced reference accepted")
					}
				})
			}
		})
	}
}

func machoCoalescedImage(t *testing.T, objects []*object) *image {
	t.Helper()
	defs, err := definitions(objects)
	if err != nil {
		t.Fatal(err)
	}
	im := &image{base: 0x100000, pointerSize: 8, objects: objects, defs: defs, mem: make([]byte, 262144),
		external: func(string) uintptr { return 0 }, resolved: map[string]uintptr{}, got: map[uintptr]uintptr{}, stubs: map[uintptr]uintptr{},
		gotStart: 131072, gotNext: 131072, stubStart: 196608, stubNext: 196608}
	next := uint64(0)
	for _, o := range objects {
		for _, sec := range o.sections {
			if sec != nil {
				sec.offset = next
				copy(im.mem[next:next+sec.size], sec.data)
				next += alignUp(sec.size, 4096)
			}
		}
	}
	for _, o := range objects {
		for _, r := range o.relocs {
			if err := im.relocate(o, r); err != nil {
				t.Fatal(err)
			}
		}
	}
	return im
}

func TestMachOCoalescedAtoms(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		t.Run(target, func(t *testing.T) {
			_, b := machoCoalescedFixture(t, target, "macho_coalesced.S")
			weak := parseMachoAliases(t, b)
			_, b = machoCoalescedFixture(t, target, "macho_coalesced_strong.c")
			strong := parseMachoAliases(t, b)
			discardUnwind(strong)
			if len(weak.machoAtoms) != 6 {
				t.Fatal("genuine multi-atom input missing", weak.machoAtoms)
			}
			for _, s := range weak.symbols {
				if s.name == "coal_anchor" || s.name == "coal_record" {
					sec := weak.sections[s.section]
					if sec.align < 16 || (sec.original+s.value)%16 != 0 {
						t.Fatal("assembly array violates native C alignment", s.name)
					}
				}
			}
			relocs, symbols := append([]relocation(nil), weak.relocs...), append([]symbol(nil), weak.symbols...)
			for _, order := range []string{"weak_first", "strong_first"} {
				t.Run(order, func(t *testing.T) {
					inputs := []*object{weak, strong}
					if order == "strong_first" {
						inputs = []*object{strong, weak}
					}
					selected, err := coalesceObjects(inputs)
					if err != nil {
						t.Fatal(err)
					}
					im := machoCoalescedImage(t, selected)
					value, _ := im.lookup("coal_value")
					keep, _ := im.lookup("coal_keep")
					record, _ := im.lookup("coal_record")
					drop, _ := im.lookup("coal_drop")
					anchor, err := im.lookup("coal_anchor")
					if err != nil {
						t.Fatal(err)
					}
					for i, want := range []uintptr{value, keep, record, record + 8, record + 8, record + 16} {
						if got := le.Uint64(im.mem[anchor-im.base+uintptr(i)*8:]); got != uint64(want) {
							t.Fatalf("anchor %d: %#x, want %#x", i, got, want)
						}
					}
					private := le.Uint64(im.mem[anchor-im.base+48:])
					if le.Uint64(im.mem[private-uint64(im.base):]) != 17 || le.Uint64(im.mem[anchor-im.base+56:]) != uint64(drop) {
						t.Fatal("private atom or whole-section binding lost")
					}
					if le.Uint64(im.mem[anchor-im.base+64:]) != uint64(record)+16 {
						t.Fatal("named one-past alternate entry was not rebound")
					}
					for _, name := range []string{"coal_delta", "coal_reverse"} {
						p, err := im.lookup(name)
						want := uint64(record+8) - uint64(drop)
						if name == "coal_reverse" {
							want = -want
						}
						if err != nil || le.Uint64(im.mem[p-im.base:]) != want {
							t.Fatal("coalesced subtractor", name, err)
						}
					}
					if target == "x86_64-apple-macosx11" {
						seen := make(map[uint32]bool)
						for _, o := range selected {
							for _, r := range o.relocs {
								if o.sections[r.section].name != "__text" || r.typ != 1 && (r.typ < 6 || r.typ > 8) {
									continue
								}
								place := o.sections[r.section].offset + r.offset
								got := int64(im.base) + int64(place) + 4 + int64(int32(le.Uint32(im.mem[place:])))
								if r.typ >= 6 {
									got += 1 << (r.typ - 6)
								}
								if got != int64(record+8) {
									t.Fatalf("SIGNED relocation %d points to %#x", r.typ, got)
								}
								seen[r.typ] = true
							}
						}
						if len(seen) != 4 {
							t.Fatal("local SIGNED/1/2/4 producers missing", seen)
						}
					}
					for _, o := range selected {
						refs, err := references(o)
						if err != nil {
							t.Fatal(err)
						}
						for _, ref := range refs {
							if ref.name == "coal_missing" || ref.name == "coal_missing_drop" || ref.name == "coal_missing_func" {
								t.Fatal("discarded atom retained dependency", ref.name)
							}
						}
					}
					if !reflect.DeepEqual(weak.relocs, relocs) || !reflect.DeepEqual(weak.symbols, symbols) {
						t.Fatal("selection changed staged input")
					}
				})
			}
		})
	}
}

func TestMachOCoalescedLarge(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		t.Run(target, func(t *testing.T) {
			var src strings.Builder
			src.WriteString(".section __DATA,__coal,coalesced\n.p2align 3\n")
			for i := 0; i < 513; i++ {
				fmt.Fprintf(&src, ".globl _item%d\n.weak_definition _item%d\n_item%d:\n.quad _missing%d\n", i, i, i, i)
			}
			src.WriteString(".subsections_via_symbols\n")
			path := machoCoalescedAssembly(t, target, src.String())
			b, err := readFile(path)
			if err != nil {
				t.Fatal(err)
			}
			o := parseMachoAliases(t, b)
			if len(o.machoAtoms) != 513 {
				t.Fatal("large multi-atom section lost boundaries")
			}
			provider := &object{info: o.info}
			for i := 0; i < 513; i++ {
				provider.symbols = append(provider.symbols, symbol{name: fmt.Sprintf("item%d", i), section: -1, global: true, value: uint64(0x100000 + i*8)})
			}
			selected, err := coalesceObjects([]*object{o, provider})
			if err != nil || len(selected[0].relocs) != 0 {
				t.Fatal("losing large section retained dependencies", err)
			}
			for _, sec := range selected[0].sections {
				if sec != nil && sec.name == "__coal" {
					t.Fatal("entirely discarded section still allocated")
				}
			}
		})
	}
}

func TestMachOCoalescedFarBranch(t *testing.T) {
	for _, tc := range []struct {
		name string
		add  int64
	}{{"negative", -12}, {"zero", -4}, {"positive", 4}} {
		t.Run(tc.name, func(t *testing.T) {
			im := &image{base: 0x100000, mem: make([]byte, 256), stubStart: 128, stubNext: 128, stubs: map[uintptr]uintptr{}}
			b := im.mem[4:8]
			le.PutUint32(b, 4) // Stored and explicit addends jointly select an interior address.
			target := uintptr(0xf0000000)
			o := &object{info: Info{Format: "Mach-O", Arch: "amd64"}}
			if err := im.relocMachO(o, relocation{typ: 2, width: 4, pcrel: true, addend: tc.add, pair: -1}, b, target, im.base+4); err != nil {
				t.Fatal(err)
			}
			jump := int64(im.base) + 8 + int64(int32(le.Uint32(b)))
			if jump != int64(im.base)+128 || le.Uint64(im.mem[134:]) != uint64(int64(target)+4+tc.add) {
				t.Fatal("far branch addend entered an island interior or lost its target offset")
			}
		})
	}
}

func TestMachOCoalescedValidation(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		t.Run(target, func(t *testing.T) {
			_, original := machoCoalescedFixture(t, target, "macho_coalesced.S")
			for _, name := range []string{"missing_subsections", "weak_alternate", "same_address_alias", "public_alternate", "public_end", "symbol_bounds", "zero_size", "crossing_fixup", "overlap_fixup"} {
				t.Run(name, func(t *testing.T) {
					b := append([]byte(nil), original...)
					record, _ := machoAliasNlist(t, b, "coal_record")
					switch name {
					case "missing_subsections":
						le.PutUint32(b[24:], le.Uint32(b[24:]) & ^uint32(macho.FlagSubsectionsViaSymbols))
					case "weak_alternate":
						le.PutUint16(b[record+6:], le.Uint16(b[record+6:])|0x200)
					case "same_address_alias":
						keep, _ := machoAliasNlist(t, b, "coal_keep")
						le.PutUint64(b[keep+8:], le.Uint64(b[record+8:]))
					case "public_alternate", "public_end":
						f := machoPointerMetadata(t, b)
						alias := "lcoal_middle"
						if name == "public_end" {
							alias = "lcoal_end"
						}
						patched := false
						for i, s := range f.Symtab.Syms {
							if s.Name == alias {
								offset := int(le.Uint32(f.Symtab.Raw()[8:])) + i*16
								b[offset+4] |= 1
								patched = true
							}
						}
						if !patched {
							t.Fatal("missing public alternate-entry producer")
						}
					case "symbol_bounds", "zero_size":
						f := machoPointerMetadata(t, b)
						sec := f.Section("__coal")
						value := sec.Addr + sec.Size
						if name == "symbol_bounds" {
							value += 8
						}
						le.PutUint64(b[record+8:], value)
					case "crossing_fixup", "overlap_fixup":
						f := machoPointerMetadata(t, b)
						sec := f.Section("__coal")
						offset := uint32(4)
						if name == "overlap_fixup" {
							offset = 12
						}
						le.PutUint32(b[int(sec.Reloff):], offset)
					}
					f, err := parse("invalid-coalesced.o", b)
					if err == nil && len(f.info.Unsupported) == 0 {
						t.Fatal("unqualified coalescing accepted")
					}
				})
			}
		})
	}
}

func TestMachOWeakCommonPriority(t *testing.T) {
	for _, format := range []string{"Mach-O", "ELF", "COFF"} {
		for _, order := range []string{"weak_first", "common_first"} {
			t.Run(format+"/"+order, func(t *testing.T) {
				weak := &object{info: Info{Format: format}, symbols: []symbol{{name: "value", section: 1, global: true, weak: true}}}
				common := &object{info: Info{Format: format}, symbols: []symbol{{name: "value", section: -2, size: 8, global: true}}}
				objects := []*object{weak, common}
				if order == "common_first" {
					objects = []*object{common, weak}
				}
				defs, err := definitions(objects)
				want := common
				if format == "Mach-O" {
					want = weak
				}
				if err != nil || defs["value"].o != want {
					t.Fatal("weak/common priority", defs, err)
				}
			})
		}
	}
}

func TestMachOCoalescedSingleSection(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		t.Run(target, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "single.s")
			if err := os.WriteFile(src, []byte(".section __DATA,__coal,coalesced\n.p2align 3\n.globl _one\n.weak_definition _one\n_one:\n.quad 7\n"), 0600); err != nil {
				t.Fatal(err)
			}
			path := compile(t, src, src+".o", "--target="+target)
			b, err := readFile(path)
			if err != nil {
				t.Fatal(err)
			}
			f, err := macho.NewFile(bytes.NewReader(b))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if f.Flags&macho.FlagSubsectionsViaSymbols != 0 || len(parseMachoAliases(t, b).machoAtoms) != 1 {
				t.Fatal("single whole-section atom incorrectly needs subsections flag")
			}
		})
	}
}
