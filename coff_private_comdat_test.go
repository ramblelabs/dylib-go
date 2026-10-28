package dylib

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func privateCOFF(t *testing.T, target, optimization, entry, tag string, extra ...string) ([]byte, *object) {
	t.Helper()
	flags := []string{"--target=" + target, optimization, "-ffreestanding", "-funwind-tables", "-ffunction-sections", "-fdata-sections", "-DPRIVATE_ENTRY=" + entry, "-DPRIVATE_TAG=\"" + tag + "\""}
	flags = append(flags, extra...)
	path := compile(t, "testdata/coff_private.c", filepath.Join(t.TempDir(), entry+".obj"), flags...)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := parse(path, b)
	if err != nil {
		t.Fatal(err)
	}
	return b, f.obj
}

func TestCOFFPrivateCOMDATCompilerOutput(t *testing.T) {
	for _, arch := range []string{"x86_64", "aarch64", "i686"} {
		for _, dialect := range []string{"w64-windows-gnu", "pc-windows-msvc"} {
			for _, optimization := range []string{"-O0", "-O2"} {
				target := arch + "-" + dialect
				t.Run(target+"/"+optimization, func(t *testing.T) {
					first, _ := privateCOFF(t, target, optimization, "private_a", "a")
					second, _ := privateCOFF(t, target, optimization, "private_b", "b")
					for _, encoding := range []string{"ordinary", "bigobj"} {
						t.Run(encoding, func(t *testing.T) {
							var inputs []*object
							var members [][]byte
							for _, data := range [][]byte{first, second} {
								if encoding == "bigobj" {
									data = promoteBigCOFF(t, data)
								}
								f, err := parse("private.obj", data)
								if err != nil || len(f.info.Unsupported) != 0 {
									t.Fatalf("private compiler metadata: %v %+v", err, f)
								}
								locals, implicit := 0, 0
								for _, g := range f.obj.groups {
									if g.local {
										locals++
									}
									if g.selection == 5 {
										implicit++
										if g.parent <= 0 || !f.obj.sections[g.parent].exec {
											t.Fatal("unwind metadata lost its executable parent", g)
										}
									}
								}
								if locals == 0 || dialect == "w64-windows-gnu" && arch != "i686" && implicit == 0 {
									t.Fatal("fixture lost private leaders or implicit metadata", locals, implicit)
								}
								inputs = append(inputs, f.obj)
								members = append(members, data)
							}
							for _, order := range [][]*object{inputs, {inputs[1], inputs[0]}} {
								out, err := coalesceObjects(order)
								if err != nil {
									t.Fatal(err)
								}
								for i, o := range out {
									if !reflect.DeepEqual(o.sections, order[i].sections) || !reflect.DeepEqual(o.relocs, order[i].relocs) {
										t.Fatal("private names coalesced across objects or changed inputs")
									}
								}
							}
							archive := []byte("!<arch>\n")
							for i, member := range members {
								archive = append(archive, archiveMemberBytes([]string{"a.obj/", "b.obj/"}[i], member)...)
							}
							if f, err := parse("private.a", archive); err != nil || len(f.members) != 2 {
								t.Fatalf("eager archive parsing: %v %+v", err, f)
							}
						})
					}
				})
			}
		}
	}
}

func setCOFFSelection(t *testing.T, data []byte, name string, selection uint8) []byte {
	t.Helper()
	b := append([]byte(nil), data...)
	f, err := readCOFF(b)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(f.records)/f.entrySize; {
		s := f.symbol(i)
		if s.class == 3 && s.aux != 0 && s.section > 0 && f.sections[s.section-1].name == name {
			f.record(i + 1)[14] = selection
			return b
		}
		i += 1 + int(s.aux)
	}
	t.Fatal("missing COMDAT section definition", name)
	return nil
}

func TestCOFFImplicitUnwindDiscard(t *testing.T) {
	for _, target := range []string{"x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		t.Run(target, func(t *testing.T) {
			first, _ := privateCOFF(t, target, "-O2", "private_a", "a")
			second, _ := privateCOFF(t, target, "-O2", "private_a", "b", "-DPRIVATE_OBSERVER=missing_only_in_discarded")
			var inputs []*object
			for _, data := range [][]byte{first, second} {
				data = setCOFFSelection(t, data, ".text$private_a", 2)
				f, err := parse("any.obj", data)
				if err != nil {
					t.Fatal(err)
				}
				inputs = append(inputs, f.obj)
			}
			out, err := coalesceObjects(inputs)
			if err != nil {
				t.Fatal(err)
			}
			for i, o := range out {
				for _, g := range o.groups {
					want := g.local || i == 0
					if (o.sections[g.sections[0]] != nil) != want || inputs[i].sections[g.sections[0]] == nil {
						t.Fatal("implicit association or private retention changed", i, g)
					}
				}
			}
			refs, err := references(out[1])
			if err != nil {
				t.Fatal(err)
			}
			for _, ref := range refs {
				if strings.Contains(ref.name, "missing_only") {
					t.Fatal("discarded code/unwind retained its missing dependency")
				}
			}
		})
	}
}

func TestCOFFPrivateCOMDATSelections(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			for selection, rule := range []string{"one_only", "discard", "same_size", "same_contents", "discard", "largest", "discard"} {
				if selection == 4 {
					continue // ASSOCIATIVE has a parent rather than an independent leader.
				}
				t.Run([]string{"noduplicates", "any", "same_size", "exact_match", "", "largest", "newest"}[selection], func(t *testing.T) {
					var inputs []*object
					for i, owner := range []string{"owner_a", "owner_b"} {
						text := ".section .data$local,\"dw\"," + rule + ",local\nlocal:\n.long 42\n"
						if i == 1 {
							text += ".long 43\n"
						}
						text += ".section .rdata$attached,\"dr\",associative,local\n.rva local\n.data\n.globl " + owner + "\n" + owner + ":\n.rva local\n"
						data, o := coffAssembly(t, target, text)
						if selection == 6 {
							f, err := parse("newest.obj", setCOFFSelection(t, data, ".data$local", 7))
							if err != nil {
								t.Fatal(err)
							}
							o = f.obj
						}
						if !o.groups[0].local {
							t.Fatal("assembler did not preserve a static leader")
						}
						inputs = append(inputs, o)
					}
					_, global := coffAssembly(t, target, ".section .data$global,\"dw\",discard,local\n.globl local\nlocal:\n.long 99\n")
					inputs = append(inputs, global)
					out, err := coalesceObjects(inputs)
					if err != nil {
						t.Fatal(err)
					}
					defs, err := definitions(out)
					if err != nil || defs["local"].o != out[2] {
						t.Fatal("private leader replaced the external definition", err)
					}
					im := &image{objects: out, defs: defs}
					for _, o := range out[:2] {
						for _, g := range o.groups {
							if o.sections[g.sections[0]] == nil {
								t.Fatal("private selection compared independent objects", g)
							}
						}
						for i, sym := range o.symbols {
							if sym.name == "local" && !sym.global {
								d, err := im.resolveDefinition(o, i)
								if err != nil || d.o != o || d.index != i {
									t.Fatal("private reference escaped its original object", err)
								}
							}
						}
					}
				})
			}
		})
	}
}

func TestCOFFImplicitUnwindValidation(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		for _, name := range []string{"pdata", "xdata", "eh_frame", "forward", "missing_parent", "ambiguous_parent", "non_executable_parent", "removed_parent", "debug_parent", "invalid_metadata_leader", "invalid_metadata_class", "executable_metadata", "unknown_prefix", "empty_suffix"} {
			t.Run(target+"/"+name, func(t *testing.T) {
				metadata := ".pdata$suffix"
				if name == "xdata" || name == "eh_frame" {
					metadata = "." + name + "$suffix"
				}
				if name == "missing_parent" {
					metadata = ".pdata$absent"
				} else if name == "unknown_prefix" {
					metadata = ".rdata$suffix"
				} else if name == "empty_suffix" {
					metadata = ".pdata$"
				}
				parent := ".section .text$suffix,\"xr\",discard,first\n.globl first\nfirst:\nnop\n"
				if name == "debug_parent" {
					parent = strings.Replace(parent, ".text$suffix", ".debug$suffix", 1)
				}
				child := ".section " + metadata + ",\"dr\"\n.long 0\n"
				if name == "invalid_metadata_leader" || name == "invalid_metadata_class" {
					child = ".section " + metadata + ",\"dr\"\n.globl metadata_leader\nmetadata_leader:\n.long 0\n"
				}
				text := parent + child
				if name == "forward" {
					text = child + parent
				} else if name == "ambiguous_parent" {
					text += ".section .code$suffix,\"xr\",discard,second\n.globl second\nsecond:\nnop\n"
				}
				data, original := coffAssembly(t, target, text)
				data = setCOFFSelection(t, data, metadata, 2)
				input, err := readCOFF(data)
				if err != nil {
					t.Fatal(err)
				}
				for i, symbol := range original.symbols {
					if symbol.name == "metadata_leader" {
						if name == "invalid_metadata_leader" {
							le.PutUint32(input.record(i)[8:], 1)
						} else if name == "invalid_metadata_class" {
							input.record(i)[16] = 6
						}
					}
				}
				for i, section := range input.sections {
					position := 20 + i*40 + 36
					if section.name == metadata {
						flags := section.flags | 0x1000
						if name == "executable_metadata" {
							flags |= 0x20000000
						}
						le.PutUint32(data[position:], flags)
					} else if section.name == ".text$suffix" && name == "non_executable_parent" {
						le.PutUint32(data[position:], section.flags&^0x20000000)
					} else if section.name == ".text$suffix" && name == "removed_parent" {
						le.PutUint32(data[position:], section.flags|0x200)
					}
				}
				valid := name == "pdata" || name == "xdata" || name == "eh_frame" || name == "forward"
				for _, encoding := range []string{"ordinary", "bigobj"} {
					t.Run(encoding, func(t *testing.T) {
						b := data
						if encoding == "bigobj" {
							b = promoteBigCOFF(t, data)
						}
						f, err := parse("metadata.obj", b)
						if !valid {
							if err == nil {
								t.Fatal("unqualified leaderless metadata accepted")
							}
							return
						}
						if err != nil {
							t.Fatal(err)
						}
						for _, g := range f.obj.groups {
							if f.obj.sections[g.sections[0]].name == metadata && (g.selection != 5 || g.parent <= 0) {
								t.Fatal("valid metadata did not become associative", g)
							}
						}
					})
				}
			})
		}
	}
}

func TestCOFFPrivateCOMDATAliasAndLeaderValidation(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			data, o := coffAssembly(t, target, ".section .data$private,\"dw\",discard,local\nlocal:\n.long 42\n.globl public_alias\n.set public_alias,local\n")
			if !o.groups[0].local {
				t.Fatal("a later external alias replaced the static COMDAT leader")
			}
			out, err := coalesceObjects([]*object{o, o})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := definitions(out); err == nil {
				t.Fatal("private groups coalesced by their external aliases")
			}
			leader := -1
			for i, sym := range o.symbols {
				if sym.name == "local" && !sym.global {
					leader = i
				}
			}
			if leader < 0 {
				t.Fatal("missing static leader")
			}
			for _, encoding := range []string{"ordinary", "bigobj"} {
				for _, malformed := range []string{"offset", "class", "name", "auxiliary"} {
					t.Run(encoding+"/"+malformed, func(t *testing.T) {
						b := append([]byte(nil), data...)
						if encoding == "bigobj" {
							b = promoteBigCOFF(t, b)
						}
						input, err := readCOFF(b)
						if err != nil {
							t.Fatal(err)
						}
						record := input.record(leader)
						class := 16
						if input.big {
							class = 18
						}
						switch malformed {
						case "offset":
							le.PutUint32(record[8:], 1)
						case "class":
							record[class] = 6
						case "name":
							clear(record[:8])
						case "auxiliary":
							record[class+1] = 1
						}
						if _, err := parse("invalid-leader.obj", b); err == nil {
							t.Fatal("malformed private leader accepted or replaced by a later alias")
						}
					})
				}
			}
		})
	}
}

func TestCOFFPrivateCOMDATInterleavedLeader(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			data, o := coffAssembly(t, target, ".section .data$local,\"dw\",discard,local\nlocal:\n.long 42\n.globl absolute\n.set absolute,7\n")
			if len(o.relocs) != 0 {
				t.Fatal("symbol-order fixture must have no relocation indexes to update")
			}
			for _, encoding := range []string{"ordinary", "bigobj"} {
				t.Run(encoding, func(t *testing.T) {
					b := append([]byte(nil), data...)
					if encoding == "bigobj" {
						b = promoteBigCOFF(t, b)
					}
					input, err := readCOFF(b)
					if err != nil {
						t.Fatal(err)
					}
					local, absolute := -1, -1
					for i, symbol := range o.symbols {
						if symbol.name == "local" {
							local = i
						} else if symbol.name == "absolute" {
							absolute = i
						}
					}
					if local < 0 || absolute <= local || input.symbol(local).aux != 0 || input.symbol(absolute).aux != 0 {
						t.Fatal("missing ordered, auxiliary-free leader/absolute records")
					}
					copyOfLocal := append([]byte(nil), input.record(local)...)
					copy(input.record(local), input.record(absolute))
					copy(input.record(absolute), copyOfLocal)
					f, err := parse("interleaved.obj", b)
					if err != nil || len(f.obj.groups) != 1 || !f.obj.groups[0].local {
						t.Fatalf("absolute symbol consumed the section's leader: %v", err)
					}
				})
			}
		})
	}
}
