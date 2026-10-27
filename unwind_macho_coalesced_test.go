package dylib

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	examplecall "github.com/ramblelabs/dylib-go/examples/call"
)

func machoCoalescedFrames(t *testing.T, target, opt, mode string, strong bool, extra ...string) (string, *object) {
	t.Helper()
	flags := []string{opt, "-fexceptions", "-fno-stack-protector"}
	if mode == "compact" {
		flags = append(flags, "-DDYLIB_CXX_COMPACT")
	} else {
		flags = append(flags, "-femit-dwarf-unwind=always")
	}
	if strong {
		flags = append(flags, "-DDYLIB_FRAME_STRONG")
	}
	flags = append(flags, extra...)
	path, b := machoCoalescedFixture(t, target, "macho_coalesced_frames.cpp", flags...)
	return path, parseMachoAliases(t, b)
}

func TestMachOCoalescedFramePruning(t *testing.T) {
	testMachOFramePruning(t, false)
}

func TestMachOOrdinaryFramePruning(t *testing.T) {
	testMachOFramePruning(t, true)
}

func testMachOFramePruning(t *testing.T, ordinary bool, public ...bool) {
	var frameFlags, deadFlags, strongFlags []string
	if ordinary {
		frameFlags = []string{"-DDYLIB_FRAME_ORDINARY"}
		deadFlags = []string{"-DDYLIB_COAL_KIND=regular"}
	}
	if len(public) != 0 && public[0] {
		frameFlags = append(frameFlags, "-DDYLIB_FRAME_PUBLIC_ALIASES")
		strongFlags = []string{"-DDYLIB_FRAME_PUBLIC_ALIASES"}
	}
	if len(public) > 1 && public[1] {
		frameFlags = append(frameFlags, "-DDYLIB_FRAME_SHARED_ALIASES")
	}
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		for _, opt := range []string{"-O0", "-O2"} {
			for _, mode := range []string{"dwarf", "compact"} {
				t.Run(target+"/"+opt+"/"+mode, func(t *testing.T) {
					_, weak := machoCoalescedFrames(t, target, opt, mode, false, frameFlags...)
					if len(public) > 1 && public[1] {
						assertMachOSharedFrameAlias(t, weak)
					}
					_, strong := machoCoalescedFrames(t, target, opt, mode, true, strongFlags...)
					_, deadBytes := machoCoalescedFixture(t, target, "macho_coalesced_dead_frame.S", deadFlags...)
					dead := parseMachoAliases(t, deadBytes)
					typeReference := false
					for _, r := range weak.relocs {
						if weak.sections[r.section].name == "__gcc_except_tab" && r.symbol >= 0 && r.symbol < len(weak.symbols) && weak.symbols[r.symbol].name == "_ZTI13DeadFrameType" {
							typeReference = true
						}
					}
					if !typeReference {
						t.Fatal("producer has no dead LSDA RTTI relocation")
					}
					before := make([][]byte, len(weak.sections))
					for i, s := range weak.sections {
						if s != nil {
							before[i] = append([]byte(nil), s.data...)
						}
					}
					symbols, relocs := append([]symbol(nil), weak.symbols...), append([]relocation(nil), weak.relocs...)
					for _, order := range []string{"weak_first", "strong_first"} {
						t.Run(order, func(t *testing.T) {
							inputs := []*object{weak, dead, strong}
							if order == "strong_first" {
								inputs = []*object{strong, dead, weak}
							}
							selected, err := coalesceObjects(inputs)
							if err != nil {
								t.Fatal(err)
							}
							for _, o := range selected {
								refs, err := references(o)
								if err != nil {
									t.Fatal(err)
								}
								for _, ref := range refs {
									if ref.name == "coal_frame_missing" || ref.name == "coal_frame_missing_tail" || ref.name == "coal_frame_missing_deadonly" || ref.name == "coal_frame_missing_personality" || ref.name == "_ZTI13DeadFrameType" {
										t.Fatal("discarded frame dependency survived", ref.name)
									}
								}
							}
							for _, s := range selected[1].sections {
								if s != nil && (s.name == "__eh_frame" || s.name == "__compact_unwind") {
									t.Fatal("dead-only frame retained its unused personality/CIE")
								}
							}
							selectedWeak := selected[0]
							if order == "strong_first" {
								selectedWeak = selected[2]
							}
							im, err := machoOriginalUnwindImage(selectedWeak)
							if err != nil {
								t.Fatal(err)
							}
							if err := im.rebaseMachODWARF(); err != nil {
								t.Fatal(err)
							}
							frames := 0
							for _, s := range im.objects[0].sections {
								if s != nil && s.name == "__eh_frame" {
									fdes, err := im.validateFrameRecords(im.mem[s.offset:s.offset+s.size], s.offset, framePolicy{pcRelative: true, language: true})
									if err != nil {
										t.Fatal(err)
									}
									frames += len(fdes)
									if s.size >= uint64(len(machoFrameSectionData(weak, "__eh_frame"))) {
										t.Fatal("FDE pruning did not compact metadata")
									}
								}
							}
							if mode == "dwarf" && frames < 2 {
								t.Fatal("retained root/weak language frames missing", frames)
							}
							for i, s := range weak.sections {
								if s != nil && !bytes.Equal(before[i], s.data) {
									t.Fatal("frame pruning mutated staged bytes")
								}
							}
							if !reflect.DeepEqual(symbols, weak.symbols) || !reflect.DeepEqual(relocs, weak.relocs) {
								t.Fatal("frame pruning mutated staged definitions")
							}
						})
					}
				})
			}
		}
	}
}

func TestMachOCoalescedSharedLSDA(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		for _, order := range []string{"dead_first", "live_first"} {
			t.Run(target+"/"+order, func(t *testing.T) {
				_, o := machoCoalescedFrames(t, target, "-O0", "dwarf", false)
				var data *section
				for _, s := range o.sections {
					if s != nil && s.name == "__gcc_except_tab" {
						data = s
					}
				}
				if data == nil {
					t.Fatal("missing producer LSDA")
				}
				before := append([]byte(nil), data.data...)
				relocs, symbols := append([]relocation(nil), o.relocs...), append([]symbol(nil), o.symbols...)
				const base = uint64(0x100000)
				refs := []machoLSDAReference{{base + data.original, false}, {base + data.original, true}}
				if order == "live_first" {
					refs[0], refs[1] = refs[1], refs[0]
				}
				if err := o.pruneMachOLSDA(refs, base, nil); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, data.data) || !reflect.DeepEqual(relocs, o.relocs) || !reflect.DeepEqual(symbols, o.symbols) {
					t.Fatal("discarded owner removed a shared live exception table")
				}
			})
		}
	}
}

func machoFrameSectionData(o *object, name string) []byte {
	for _, s := range o.sections {
		if s != nil && s.name == name {
			return s.data
		}
	}
	return nil
}

func TestMachOCoalescedFrameValidation(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		t.Run(target, func(t *testing.T) {
			weakPath, _ := machoCoalescedFrames(t, target, "-O0", "dwarf", false)
			_, strong := machoCoalescedFrames(t, target, "-O0", "dwarf", true)
			input, err := readFile(weakPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"length", "dwarf64", "cie_reference", "cie_version", "cfi", "trailing_data", "compact_width", "crossing_range", "writable", "public_lsda", "lsda_boundary", "external_frame", "external_lsda", "sized_frame", "sparse_span", "scratch_budget", "overlap", "branch_fixup"} {
				t.Run(name, func(t *testing.T) {
					weak := parseMachoAliases(t, input)
					frameIndex, compactIndex, lsdaIndex := 0, 0, 0
					for i, s := range weak.sections {
						if s != nil {
							switch s.name {
							case "__eh_frame":
								frameIndex = i
							case "__compact_unwind":
								compactIndex = i
							case "__gcc_except_tab":
								lsdaIndex = i
							}
						}
					}
					frame, compact := weak.sections[frameIndex], weak.sections[compactIndex]
					addReference := func(targetSection int) {
						index := len(weak.symbols)
						weak.symbols = append(weak.symbols, symbol{section: targetSection})
						original := uint64(0)
						for _, s := range weak.sections {
							if s != nil && s.original+s.size > original {
								original = s.original + s.size
							}
						}
						weak.sections = append(weak.sections, &section{name: "__reference", data: make([]byte, 8), size: 8, align: 8, original: alignUp(original, 8)})
						weak.relocs = append(weak.relocs, relocation{section: len(weak.sections) - 1, symbol: index, width: 8, pair: -1})
					}
					switch name {
					case "length":
						le.PutUint32(frame.data, 0x7fffffff)
					case "dwarf64":
						le.PutUint32(frame.data, 0xffffffff)
					case "cie_reference":
						first := 4 + le.Uint32(frame.data)
						le.PutUint32(frame.data[first+4:], 0xffffffff)
					case "cie_version":
						frame.data[8] = 99
					case "cfi":
						end := int(4 + le.Uint32(frame.data))
						r := frameReader{b: frame.data[8:end]}
						version, _ := r.byte()
						zero := bytes.IndexByte(r.b, 0)
						augmented := zero != 0
						r.b = r.b[zero+1:]
						r.leb(false)
						r.leb(true)
						if version == 1 {
							r.byte()
						} else {
							r.leb(false)
						}
						if augmented {
							n, _ := r.leb(false)
							r.take(int(n))
						}
						frame.data[end-len(r.b)] = 0x2f
					case "trailing_data":
						frame.data = append(frame.data, 0, 0, 0, 0, 1)
						frame.size = uint64(len(frame.data))
					case "compact_width":
						compact.data = append(compact.data, 0)
						compact.size++
					case "crossing_range":
						le.PutUint32(compact.data[8:], 0x7fffffff)
					case "writable":
						frame.write = true
					case "public_lsda", "lsda_boundary":
						for i, sym := range weak.symbols {
							if sym.section == lsdaIndex && sym.value == 0 && (name == "lsda_boundary" || sym.name == "GCC_except_table0") {
								weak.symbols[i].global = true
							}
						}
					case "external_frame":
						addReference(frameIndex)
					case "external_lsda":
						addReference(lsdaIndex)
					case "sized_frame":
						weak.symbols = append(weak.symbols, symbol{section: frameIndex, size: 1})
					case "sparse_span":
						weak.sections = append(weak.sections, &section{original: maxImage + 8, size: 8, data: make([]byte, 8)})
					case "scratch_budget":
						weak.sections = append(weak.sections, &section{original: maxImage - 8, size: 8, data: make([]byte, 8)})
					case "overlap":
						weak.sections = append(weak.sections, &section{original: frame.original, size: 8, data: make([]byte, 8)})
					case "branch_fixup":
						for i := range weak.relocs {
							if weak.relocs[i].section == frameIndex {
								weak.relocs[i].typ, weak.relocs[i].width, weak.relocs[i].pcrel, weak.relocs[i].pair = 2, 4, true, -1
								break
							}
						}
					}
					before := *weak
					before.symbols = append([]symbol(nil), weak.symbols...)
					before.relocs = append([]relocation(nil), weak.relocs...)
					before.sections = make([]*section, len(weak.sections))
					for i, s := range weak.sections {
						if s != nil {
							c := *s
							c.data = append([]byte(nil), s.data...)
							before.sections[i] = &c
						}
					}
					if _, err := coalesceObjects([]*object{weak, strong}); err == nil {
						t.Fatal("invalid associated unwind was accepted", name)
					}
					if !reflect.DeepEqual(before, *weak) {
						t.Fatal("failed pruning mutated staged metadata", name)
					}
				})
			}
		})
	}
}

func TestNativeMachOCoalescedFrames(t *testing.T) {
	testNativeMachOFrames(t, false)
}

func TestNativeMachOOrdinaryFrames(t *testing.T) {
	testNativeMachOFrames(t, true)
}

func testNativeMachOFrames(t *testing.T, ordinary bool, public ...bool) {
	target := needMachOCoalescedNative(t)
	var frameFlags, deadFlags, strongFlags []string
	if ordinary {
		frameFlags = []string{"-DDYLIB_FRAME_ORDINARY"}
		deadFlags = []string{"-DDYLIB_COAL_KIND=regular"}
	}
	if len(public) != 0 && public[0] {
		frameFlags = append(frameFlags, "-DDYLIB_FRAME_PUBLIC_ALIASES")
		strongFlags = []string{"-DDYLIB_FRAME_PUBLIC_ALIASES"}
	}
	if len(public) > 1 && public[1] {
		frameFlags = append(frameFlags, "-DDYLIB_FRAME_SHARED_ALIASES")
	}
	for _, opt := range []string{"-O0", "-O2"} {
		for _, mode := range []string{"dwarf", "compact"} {
			t.Run(opt+"/"+mode, func(t *testing.T) {
				weak, weakObject := machoCoalescedFrames(t, target, opt, mode, false, frameFlags...)
				if len(public) > 1 && public[1] {
					assertMachOSharedFrameAlias(t, weakObject)
				}
				strong, _ := machoCoalescedFrames(t, target, opt, mode, true, strongFlags...)
				dead, _ := machoCoalescedFixture(t, target, "macho_coalesced_dead_frame.S", deadFlags...)
				unused := machoCoalescedAssembly(t, target, ".data\n.globl _coal_frame_unused\n_coal_frame_unused:\n.quad _coal_frame_poison\n")
				host := filepath.Join(t.TempDir(), "host.dylib")
				sdk := machoCoalescedSDK(t)
				flags := []string{"--target=" + target, "-isysroot", sdk, "-Wl,-syslibroot," + sdk, opt, "-dynamiclib", "-fexceptions", "testdata/unwind_cxx_host.cpp", "-o", host}
				if mode == "compact" {
					flags = append(flags, "-DDYLIB_CXX_COMPACT")
				}
				command(t, "clang++", flags...)
				observer := New(Options{})
				defer observer.Close()
				load(t, observer, host)
				for _, order := range []string{"weak_first", "strong_first"} {
					t.Run("system_control/"+order, func(t *testing.T) {
						flags := []string{opt, "-fexceptions", "-fno-stack-protector", "-DDYLIB_FRAME_STANDALONE"}
						flags = append(flags, frameFlags...)
						if mode == "compact" {
							flags = append(flags, "-DDYLIB_CXX_COMPACT")
						} else {
							flags = append(flags, "-femit-dwarf-unwind=always")
						}
						control, _ := machoCoalescedFixture(t, target, "macho_coalesced_frames.cpp", flags...)
						inputs := []string{control, dead, strong}
						if order == "strong_first" {
							inputs = []string{strong, dead, control}
						}
						exe := filepath.Join(t.TempDir(), "control")
						args := []string{"--target=" + target, "-isysroot", sdk, "-Wl,-syslibroot," + sdk, "-Wl,-dead_strip"}
						command(t, "clang++", append(append(args, inputs...), host, "-o", exe)...)
						command(t, exe)
					})
				}
				for _, order := range []string{"weak_first", "strong_first"} {
					for _, kind := range []string{"objects", "archive"} {
						t.Run(order+"/"+kind, func(t *testing.T) {
							s := New(Options{RegisterUnwind: true})
							defer s.Close()
							input := weak
							if kind == "archive" {
								input = machoCoalescedArchive(t, unused, weak)
							}
							inputs := []string{host, input, dead, strong}
							if order == "strong_first" {
								inputs = []string{host, strong, dead, input}
							}
							load(t, s, inputs...)
							if err := s.Link("coal_frame_root"); err != nil {
								t.Fatal(err)
							}
							for _, a := range []int32{20, -20} {
								call(t, observer, "cxx_events", 1, 0, 0)
								call(t, s, "coal_frame_root", a, 42-a, 42)
								if len(public) != 0 && public[0] {
									call(t, s, "coal_frame_keep", a, 42-a, 42)
								}
								call(t, observer, "cxx_events", 1, 0, 1)
								call(t, observer, "cxx_has_frame", 0, 0, 1)
							}
							if mode == "compact" && s.image.compactUsed == 0 {
								t.Fatal("compact language frames were not registered")
							}
							owner := s.image.unwind
							if owner == nil || !owner.registered {
								t.Fatal("retained unwind has no runtime owner")
							}
							entry, err := s.Resolve("coal_frame_root")
							if err != nil {
								t.Fatal(err)
							}
							lease, err := entry.Acquire()
							if err != nil {
								t.Fatal(err)
							}
							defer lease.Close()
							closed := make(chan error, 1)
							go func() { closed <- s.Close() }()
							waitSessionRetired(t, s)
							if err := lease.WithAddress(func(address uintptr) error {
								value, err := examplecall.Int32(address, 20, 22)
								if value != 42 {
									t.Errorf("retired exception call: %d", value)
								}
								return err
							}); err != nil {
								t.Fatal(err)
							}
							call(t, observer, "cxx_has_frame", 0, 0, 1)
							select {
							case <-closed:
								t.Fatal("unwind retired with an active lease")
							default:
							}
							lease.Close()
							if err := <-closed; err != nil {
								t.Fatal(err)
							}
							call(t, observer, "cxx_has_frame", 0, 0, 0)
							if owner.registered || owner.dynamic != 0 || owner.provider != 0 {
								t.Fatal("pruned frame registration survived Close", owner)
							}
							call(t, observer, "cxx_events", 1, 0, 1)
						})
					}
				}
				for _, failure := range []string{"dependency_retry", "registration_retry"} {
					t.Run(failure, func(t *testing.T) {
						s := New(Options{RegisterUnwind: true})
						defer s.Close()
						load(t, s, host, machoCoalescedArchive(t, unused, weak), dead)
						filled, slots := false, int32(0)
						if failure == "registration_retry" {
							load(t, s, strong)
							var err error
							slots, err = callInt32(observer, "cxx_unwind_slots", 1, 0)
							if err != nil || slots <= 0 {
								t.Fatal("native finder slots", slots, err)
							}
							filled = true
							defer func() {
								if filled {
									call(t, observer, "cxx_unwind_slots", 0, 0, slots)
								}
							}()
						}
						expected := "unresolved symbol"
						if filled {
							expected = "dynamic Mach-O unwind registration"
						}
						if err := s.Link("coal_frame_root"); err == nil || !strings.Contains(err.Error(), expected) || s.image != nil || s.initErr != nil {
							t.Fatal("failed link published pruned frames", err)
						}
						if filled {
							call(t, observer, "cxx_unwind_slots", 0, 0, slots)
							filled = false
						} else {
							load(t, s, strong)
						}
						call(t, observer, "cxx_events", 1, 0, 0)
						call(t, s, "coal_frame_root", 20, 22, 42)
						call(t, observer, "cxx_events", 1, 0, 1)
						call(t, observer, "cxx_has_frame", 0, 0, 1)
						if err := s.Close(); err != nil {
							t.Fatal(err)
						}
						call(t, observer, "cxx_has_frame", 0, 0, 0)
					})
				}
			})
		}
	}
}

func TestMachOPublicAliasFramePruning(t *testing.T) {
	for _, kind := range []string{"regular", "coalesced"} {
		t.Run(kind, func(t *testing.T) { testMachOFramePruning(t, kind == "regular", true) })
	}
}

func TestNativeMachOPublicAliasFrames(t *testing.T) {
	for _, kind := range []string{"regular", "coalesced"} {
		t.Run(kind, func(t *testing.T) { testNativeMachOFrames(t, kind == "regular", true) })
	}
}
