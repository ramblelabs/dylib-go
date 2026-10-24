package dylib

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCOFFMixedCOMDATMetadata(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			parts := longImportParts(t, target, "provider.dll")
			data, raw := coffAssembly(t, target, longImportAssembly(target, longImportFixture{"add", "add", 0, false})+mixedCOMDATImportAssembly(target))
			f, err := parse("mixed.lib", longImportArchive(append(parts, data)))
			if err != nil {
				t.Fatal(err)
			}
			o := f.members[2].obj
			if len(o.info.Imports) != 1 || len(o.info.Unsupported) != 0 || len(o.groups) != 4 || !reflect.DeepEqual(o.groups, raw.groups) {
				t.Fatalf("grouped import metadata: %+v, groups %+v", o.info, o.groups)
			}
			if len(o.symbols) != len(raw.symbols) || len(o.sections) != len(raw.sections) {
				t.Fatal("original symbol/section indices changed")
			}
			for _, group := range o.groups {
				index := group.sections[0]
				if !reflect.DeepEqual(o.sections[index], raw.sections[index]) {
					t.Fatal("group section changed", index)
				}
				if group.selection == 5 && (group.parent == 0 || o.sections[group.parent] == nil) {
					t.Fatal("associative parent removed")
				}
			}
			for i, sym := range raw.symbols {
				if sym.section > 0 && o.sections[sym.section] != nil && o.symbols[i] != sym {
					t.Fatal("retained symbol changed", sym.name)
				}
			}
			var relocs []relocation
			for _, r := range raw.relocs {
				if o.sections[r.section] != nil {
					relocs = append(relocs, r)
				}
			}
			if !reflect.DeepEqual(o.relocs, relocs) {
				t.Fatal("retained relocations changed")
			}
		})
	}
}

func TestCOFFMixedCOMDATSelection(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			for _, tc := range []struct {
				name, rule  string
				secondSize  int
				secondValue int
				winner      int
				fail        bool
			}{
				{"noduplicates", "one_only", 4, 42, 0, true},
				{"any", "discard", 4, 43, 0, false},
				{"same_size", "same_size", 4, 43, 0, false},
				{"size_mismatch", "same_size", 8, 42, 0, true},
				{"exact_match", "same_contents", 4, 42, 0, false},
				{"content_mismatch", "same_contents", 4, 43, 0, true},
				{"largest", "largest", 8, 43, 1, false},
				{"newest", "discard", 4, 43, 1, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					parts := longImportParts(t, target, "provider.dll")
					key := "extra"
					if strings.HasPrefix(target, "i686") {
						key = "_" + key
					}
					for i := range 2 {
						value, size := 42, 4
						if i == 1 {
							value, size = tc.secondValue, tc.secondSize
						}
						ordinary := fmt.Sprintf(".section .data$extra,\"dw\",%s,%s\n.global %s\n%s:\n.long %d\n.space %d\n.section .rdata$associated,\"dr\",associative,%s\n.rva %s\n", tc.rule, key, key, key, value, size-4, key, key)
						data, raw := coffAssembly(t, target, longImportAssembly(target, longImportFixture{"add", "add", 0, false})+ordinary)
						if tc.name == "newest" {
							le.PutUint32(data[4:], uint32(i+1))
							for index, sym := range raw.symbols {
								if sym.section == raw.groups[0].sections[0] && !sym.global {
									aux := int(le.Uint32(data[8:])) + (index+1)*18
									data[aux+14] = 7
									break
								}
							}
						}
						parts = append(parts, data)
					}
					f, err := parse("selection.lib", longImportArchive(parts))
					if err != nil {
						t.Fatal(err)
					}
					inputs := []*object{f.members[2].obj, f.members[3].obj}
					for _, o := range inputs {
						if len(o.groups) != 2 || len(o.info.Imports) != 1 || len(o.info.Unsupported) != 0 || len(o.relocs) != 1 {
							t.Fatalf("selection fixture not converted: %+v", o.info)
						}
					}
					selected, err := coalesceObjects(inputs)
					if tc.fail {
						if err == nil {
							t.Fatal("invalid duplicate accepted")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					defs, err := definitions(selected)
					if err != nil || defs["extra"].o != selected[tc.winner] {
						t.Fatalf("wrong winner: %v", err)
					}
					for i, o := range selected {
						for _, group := range o.groups {
							if (o.sections[group.sections[0]] != nil) != (i == tc.winner) {
								t.Fatal("association did not follow parent selection")
							}
						}
						wantRelocs := 0
						if i == tc.winner {
							wantRelocs = 1
						}
						if len(inputs[i].relocs) != 1 || len(o.relocs) != wantRelocs {
							t.Fatal("relocation discard changed parsed input")
						}
					}
				})
			}
		})
	}
}

func TestCOFFMixedCOMDATValidation(t *testing.T) {
	const target = "x86_64-pc-windows-msvc"
	parts := longImportParts(t, target, "provider.dll")
	data, _ := coffAssembly(t, target, longImportAssembly(target, longImportFixture{"add", "add", 0, false})+mixedCOMDATImportAssembly(target))
	parts = append(parts, data)
	for _, name := range []string{"empty", "invalid_index", "zero_index", "thunk", "iat", "associative_thunk"} {
		t.Run(name, func(t *testing.T) {
			var inputs []*object
			for _, data := range parts {
				f, err := parse("raw.obj", data)
				if err != nil {
					t.Fatal(err)
				}
				inputs = append(inputs, f.obj)
			}
			o := inputs[2]
			switch name {
			case "empty":
				o.groups[0].sections = nil
			case "invalid_index":
				o.groups[0].sections = []int{len(o.sections)}
			case "zero_index":
				o.groups[0].sections = []int{0}
			default:
				section := ".text"
				if name == "iat" {
					section = ".idata$5"
				}
				for i, sec := range o.sections {
					if sec != nil && sec.name == section {
						if name == "associative_thunk" {
							for j := range o.groups {
								if o.groups[j].selection == 5 {
									o.groups[j].parent = i
								}
							}
						} else {
							o.groups[0].sections = []int{i}
						}
					}
				}
			}
			if _, ok := newCOFFImportGraph(inputs).convert(o); ok {
				t.Fatal("group intersecting removed/invalid content accepted")
			}
		})
	}
}

func TestCOFFMixedEmptyCOMDAT(t *testing.T) {
	const target = "x86_64-pc-windows-msvc"
	parts := longImportParts(t, target, "provider.dll")
	data, _ := coffAssembly(t, target, longImportAssembly(target, longImportFixture{"add", "add", 0, false})+".section .data$empty,\"dw\",discard,empty\n.global empty\nempty:\n")
	f, err := parse("empty.lib", longImportArchive(append(parts, data)))
	if err != nil {
		t.Fatal(err)
	}
	o := f.members[2].obj
	if len(o.groups) != 1 || o.sections[o.groups[0].sections[0]] == nil || len(o.info.Imports) != 1 || len(o.info.Unsupported) != 0 {
		t.Fatalf("zero-size ordinary group lost: %+v", o.info)
	}
}

func TestCOFFMixedCOMDATImportTemplateRejected(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			public := "add"
			if strings.HasPrefix(target, "i686") {
				public = "_" + public
			}
			for _, template := range []string{"thunk", "iat"} {
				t.Run(template, func(t *testing.T) {
					parts := longImportParts(t, target, "provider.dll")
					text := longImportAssembly(target, longImportFixture{"add", "add", 0, false})
					if template == "thunk" {
						text = strings.Replace(text, ".text\n", ".section .text,\"xr\",discard,"+public+"\n", 1)
					} else {
						text = strings.Replace(text, ".section .idata$5,\"dr\"", ".section .idata$5,\"dr\",discard,__imp_"+public, 1)
					}
					data, _ := coffAssembly(t, target, text+mixedCOMDATImportAssembly(target))
					f, err := parse("grouped-template.lib", longImportArchive(append(parts, data)))
					if err != nil {
						t.Fatal(err)
					}
					info := f.members[2].info
					if len(info.Imports) != 0 || len(info.Unsupported) == 0 {
						t.Fatal("grouped import template normalized", info)
					}
				})
			}
		})
	}
}

func TestCOFFMixedCOMDATKeepsUnsupported(t *testing.T) {
	const target = "x86_64-pc-windows-msvc"
	parts := longImportParts(t, target, "provider.dll")
	data, _ := coffAssembly(t, target, longImportAssembly(target, longImportFixture{"add", "add", 0, false})+mixedCOMDATImportAssembly(target)+".section .tls_unsupported,\"dw\"\n.long 0\n")
	f, err := parse("tls.lib", longImportArchive(append(parts, data)))
	if err != nil {
		t.Fatal(err)
	}
	info := f.members[2].info
	if len(info.Imports) != 1 || len(info.Unsupported) != 1 || !strings.Contains(info.Unsupported[0], "TLS section") {
		t.Fatal("ordinary unsupported feature hidden", info)
	}
}
