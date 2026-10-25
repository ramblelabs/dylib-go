package dylib

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Exercise both legal encodings: LLVM's LOCAL marker plus an ordinary
// relocation, and an indirect nlist entry whose placeholder is overwritten.
func machoInteriorPointers(t *testing.T, input []byte) []byte {
	t.Helper()
	b := append([]byte(nil), input...)
	f := machoPointerMetadata(t, b)
	sec := f.Section("__nl_symbol_ptr")
	for entry, name := range []string{"lalias_mid", "lalias_end", "lalias_function"} {
		found := false
		for index, s := range f.Symtab.Syms {
			if s.Name == name {
				le.PutUint32(b[machoPointerEntry(t, b, sec.Name, entry):], uint32(index))
				le.PutUint64(b[int(sec.Offset)+entry*8:], 0xdeadbeef)
				found = true
			}
		}
		if !found {
			t.Fatal("missing local indirect alias", name)
		}
	}
	return b
}

func TestMachOInteriorAliases(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		t.Run(target, func(t *testing.T) {
			_, b := machoCoalescedFixture(t, target, "macho_interior_aliases.S")
			_, strongBytes := machoCoalescedFixture(t, target, "macho_interior_strong.S")
			for _, encoding := range []string{"local", "nlist"} {
				t.Run(encoding, func(t *testing.T) {
					input := b
					if encoding == "nlist" {
						input = machoInteriorPointers(t, input)
					}
					o := parseMachoAliases(t, input)
					strong := parseMachoAliases(t, strongBytes)
					counts := make(map[uint32]int)
					for _, r := range o.relocs {
						if !machoAtomAddressRelocation(o, r) {
							continue
						}
						counts[r.typ]++
						if name := o.symbols[r.symbol].name; name != "coal_alias_record" && name != "coal_alias_function" {
							t.Fatal("indirection kept a local atom identity", name)
						}
						if r.targetOffset != 8 && r.targetOffset != 16 || r.addend != 0 {
							t.Fatal("alias offset polluted the relocation field", r)
						}
					}
					want := map[uint32]int{3: 3, 4: 1, machoStubFixup: 1}
					if o.info.Arch == "arm64" {
						want = map[uint32]int{5: 3, 6: 3, 7: 1, machoStubFixup: 1}
					}
					if encoding == "nlist" {
						want[machoPointerFixup] = 3
					}
					if !reflect.DeepEqual(counts, want) {
						t.Fatal("missing genuine GOT or indirect producers", counts, want)
					}
					relocs, symbols := append([]relocation(nil), o.relocs...), append([]symbol(nil), o.symbols...)
					for _, order := range []string{"weak_first", "strong_first", "retained"} {
						t.Run(order, func(t *testing.T) {
							inputs := []*object{o, strong}
							if order == "strong_first" {
								inputs = []*object{strong, o}
							} else if order == "retained" {
								inputs = []*object{o}
							}
							selected, err := coalesceObjects(inputs)
							if err != nil {
								t.Fatal(err)
							}
							im := machoCoalescedImage(t, selected)
							record, _ := im.lookup("coal_alias_record")
							function, _ := im.lookup("coal_alias_function")
							pointers, _ := im.lookup("alias_indirect")
							for index, address := range []uintptr{record + 8, record + 16, function + 8} {
								if le.Uint64(im.mem[pointers-im.base+uintptr(index)*8:]) != uint64(address) {
									t.Fatal("indirect pointer lost winning interior target", index)
								}
								got := im.got[address]
								if got == 0 || le.Uint64(im.mem[got-im.base:]) != uint64(address) {
									t.Fatal("GOT cell lost winning interior target", index)
								}
							}
							if len(im.got) != 3 { // Repeated references share the full target's cell.
								t.Fatal("GOT identity omitted or double-applied an offset", im.got)
							}
							if !reflect.DeepEqual(relocs, o.relocs) || !reflect.DeepEqual(symbols, o.symbols) {
								t.Fatal("selection mutated staged aliases")
							}
						})
					}
				})
			}
		})
	}
}

func TestMachOInteriorTargetOffsets(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		for _, far := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/far%t", arch, far), func(t *testing.T) {
				const base, offset = 0x100000, 128
				target := uintptr(base + 1024)
				if far {
					address := uint64(0x9000000000)
					target = uintptr(address)
					if uint64(target) != address {
						target = 0xf0000000
					}
				}
				for _, width := range machoCustomWidths(arch) {
					o := &object{info: Info{Format: "Mach-O", Arch: arch}, sections: []*section{nil, {size: uint64(width), offset: offset}}, symbols: []symbol{{section: -1, value: uint64(target)}}}
					im := &image{base: base, mem: make([]byte, 4096), pointerSize: 8, got: map[uintptr]uintptr{}, stubs: map[uintptr]uintptr{}, gotStart: 2048, gotNext: 2048, stubStart: 3072, stubNext: 3072}
					im.mem[offset+width] = 0xcc
					r := relocation{section: 1, width: width, typ: machoStubFixup, pair: -1, targetOffset: 8}
					if err := im.relocate(o, r); err != nil {
						t.Fatal(err)
					}
					full := target + 8
					short := arch == "amd64" && width == 5 || arch == "arm64" && width < 12
					if short {
						var destination uintptr
						if arch == "amd64" {
							destination = uintptr(int64(base+offset+5) + int64(int32(le.Uint32(im.mem[offset+1:]))))
						} else {
							destination = uintptr(int64(base+offset) + signExtend(le.Uint32(im.mem[offset:])&0x3ffffff, 26)*4)
						}
						want := full
						if far {
							want = im.stubs[full]
							literal := uintptr(6)
							if arch == "arm64" {
								literal = 8
							}
							if want == 0 || le.Uint64(im.mem[want-base+literal:]) != uint64(full) {
								t.Fatal("island lost interior target")
							}
						}
						if destination != want {
							t.Fatal("branch lost interior target", destination, want)
						}
					} else if slot := im.got[full]; slot == 0 || le.Uint64(im.mem[slot-base:]) != uint64(full) {
						t.Fatal("stub GOT lost interior target")
					}
					if im.mem[offset+width] != 0xcc {
						t.Fatal("interior stub overwrote adjacent bytes")
					}
				}
			})
		}
	}
	for _, mode := range []string{"overflow", "direct", "local", "subtractor"} {
		t.Run(mode, func(t *testing.T) {
			o := &object{info: Info{Format: "Mach-O", Arch: "amd64"}, sections: []*section{nil, {size: 8}}, symbols: []symbol{{section: -1, value: 0x100000}}}
			r := relocation{section: 1, width: 8, typ: machoPointerFixup, pair: -1, targetOffset: 8}
			switch mode {
			case "overflow":
				o.symbols[0].value = uint64(^uintptr(0) - 4)
			case "direct":
				r.typ = 0
			case "local":
				r.symbol, r.local = 1, true
			case "subtractor":
				r.pair = 0
			}
			im := &image{base: 0x100000, mem: bytes.Repeat([]byte{0xcc}, 8)}
			if err := im.relocate(o, r); err == nil || !bytes.Equal(im.mem, bytes.Repeat([]byte{0xcc}, 8)) {
				t.Fatal("invalid target offset wrote storage", err)
			}
		})
	}
}

func machoInteriorFile(t *testing.T, input []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "interior.o")
	if err := os.WriteFile(path, machoInteriorPointers(t, input), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNativeMachOInteriorAliases(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			weak, b := machoCoalescedFixture(t, target, "macho_interior_aliases.S", opt)
			nlist := machoInteriorFile(t, b)
			strong, _ := machoCoalescedFixture(t, target, "macho_interior_strong.S", opt)
			probe, _ := machoCoalescedFixture(t, target, "macho_interior_probe.c", opt)
			unused := machoCoalescedAssembly(t, target, ".data\n.globl _alias_unused\n_alias_unused:\n.quad _alias_missing\n")
			for _, encoding := range []string{"local", "nlist"} {
				t.Run(encoding, func(t *testing.T) {
					input := weak
					if encoding == "nlist" {
						input = nlist
					}
					for _, order := range []string{"weak_first", "strong_first"} {
						for _, kind := range []string{"objects", "archive"} {
							t.Run(order+"/"+kind, func(t *testing.T) {
								s := New(Options{})
								defer s.Close()
								path := input
								if kind == "archive" {
									path = machoCoalescedArchive(t, unused, input)
								}
								inputs := []string{path, strong, probe}
								if order == "strong_first" {
									inputs = []string{strong, path, probe}
								}
								load(t, s, inputs...)
								if err := s.Link("alias_check"); err != nil {
									t.Fatal(err)
								}
								for i := int32(0); i < 5; i++ {
									call(t, s, "alias_check", 20+i, 22, 42+i)
								}
								if len(s.image.objects) != 3 {
									t.Fatal("unrelated archive dependency extracted")
								}
							})
						}
					}
				})
			}
			t.Run("provider_retry", func(t *testing.T) {
				proxy := machoCoalescedAssembly(t, target, ".globl _coal_alias_record, _coal_alias_function, _alias_provider_root\n_coal_alias_record = _native_record\n_coal_alias_function = _native_function\n.data\n_alias_provider_root:\n.long 0\n")
				native, _ := machoCoalescedFixture(t, target, "macho_interior_strong.S", opt, "-D_coal_alias_record=_native_record", "-D_coal_alias_function=_native_function")
				host, s := New(Options{}), New(Options{})
				defer host.Close()
				defer s.Close()
				load(t, host, native)
				load(t, s, nlist, probe, machoCoalescedArchive(t, unused, proxy))
				before := append([]relocation(nil), s.files[0].obj.relocs...)
				if err := s.Link("alias_check", "alias_provider_root"); err == nil || !strings.Contains(err.Error(), "unresolved symbol") || s.image != nil {
					t.Fatal("missing alias provider published an image", err)
				}
				if !reflect.DeepEqual(before, s.files[0].obj.relocs) {
					t.Fatal("failed link mutated staged GOT aliases")
				}
				for _, name := range []string{"native_record", "native_function"} {
					sym, err := host.Resolve(name)
					if err != nil {
						t.Fatal(err)
					}
					if err := s.DefineSymbol(name, sym); err != nil {
						t.Fatal(err)
					}
				}
				if err := s.Link("alias_check", "alias_provider_root"); err != nil {
					t.Fatal(err)
				}
				call(t, s, "alias_check", 20, 22, 42)
				closed := make(chan error, 1)
				go func() { closed <- host.Close() }()
				waitSessionRetired(t, host)
				call(t, s, "alias_check", 21, 22, 43)
				select {
				case <-closed:
					t.Fatal("provider retired while interior addresses were leased")
				default:
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				if err := <-closed; err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestNativeMachOInteriorSystemControl(t *testing.T) {
	target := needMachOCoalescedNative(t)
	sdk := machoCoalescedSDK(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			// Apple ld rejects these raw symboled indirect-section inputs. Use
			// equivalent ordinary pointer storage and a GOT tail call, keeping
			// the identical probe's pointer identity and interior calls intact.
			weak, _ := machoCoalescedFixture(t, target, "macho_interior_aliases.S", opt, "-DDYLIB_ALIAS_CONTROL")
			strong, _ := machoCoalescedFixture(t, target, "macho_interior_strong.S", opt)
			for _, order := range []string{"weak_first", "strong_first"} {
				t.Run(order, func(t *testing.T) {
					inputs := []string{weak, strong}
					if order == "strong_first" {
						inputs = []string{strong, weak}
					}
					exe := filepath.Join(t.TempDir(), "control")
					args := []string{"--target=" + target, "-isysroot", sdk, "-Wl,-syslibroot," + sdk, opt, "testdata/macho_interior_probe.c", "-DDYLIB_ALIAS_STANDALONE", "-Wl,-dead_strip"}
					command(t, compiler(), append(append(args, inputs...), "-o", exe)...)
					command(t, exe)
				})
			}
		})
	}
}
