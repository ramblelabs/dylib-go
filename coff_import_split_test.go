package dylib

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type splitDirectoryShape struct {
	name   string
	layout directoryLayout
	omit   []bool
}

var splitDirectoryShapes = []splitDirectoryShape{
	{"separate_null", directoryLayout{separateNull: true}, nil},
	{"fragments", directoryLayout{fragments: true, separateNull: true}, nil},
	{"inline_last", directoryLayout{fragments: true}, nil},
	{"public_helpers", directoryLayout{publicHelpers: true}, nil},
	{"combined", directoryLayout{fragments: true, separateNull: true, publicHelpers: true, repeated: true}, nil},
	{"bound", directoryLayout{fragments: true, separateNull: true, publicHelpers: true, repeated: true, timestamps: []uint32{1, 0x12345678}}, nil},
	{"iat_only", directoryLayout{fragments: true, separateNull: true, publicHelpers: true, repeated: true}, []bool{true, true}},
}

func stagedSplitDirectory(t *testing.T, target string, shape splitDirectoryShape) *object {
	t.Helper()
	assembly := importDirectoryAssemblyOptions(target, directoryGroups(), nil, shape.omit, shape.layout)
	assembly = strings.NewReplacer(".idata$2", ".unused2", ".id001$2", ".sd001$2", ".id002$2", ".sd002$2").Replace(assembly)
	_, o := coffDirectoryAssembly(t, target, assembly)
	for _, s := range o.sections {
		if s != nil && (s.name == ".unused2" || strings.HasPrefix(s.name, ".sd")) {
			s.name = ".idata$2"
		}
	}
	return o
}

func TestCOFFSplitImportTargets(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		for _, shape := range splitDirectoryShapes {
			for _, reverse := range []bool{false, true} {
				for _, kind := range []string{"pure", "ordinary", "comdat"} {
					if kind != "pure" && shape.name != "combined" && shape.name != "bound" && shape.name != "iat_only" {
						continue
					}
					t.Run(fmt.Sprintf("%s/%s/reverse_%t/%s", target, shape.name, reverse, kind), func(t *testing.T) {
						order := []int{0, 1}
						if reverse {
							order = []int{1, 0}
						}
						extra := ""
						if kind != "pure" {
							extra = directoryMixedAssembly(target, kind == "comdat")
						}
						_, control := coffDirectoryAssembly(t, target, importDirectoryAssemblyTables(target, directoryGroups(), order, shape.omit)+extra)
						data, o := coffDirectoryAssembly(t, target, importDirectoryAssemblyOptions(target, directoryGroups(), order, shape.omit, shape.layout)+extra)
						if len(o.info.Unsupported) != 0 || !reflect.DeepEqual(o.info.Imports, control.info.Imports) || len(o.groups) != len(control.groups) {
							t.Fatal("split directory changed imports or ordinary groups", o.info, o.groups, control.info.Imports)
						}
						for _, s := range o.symbols {
							if s.global && (strings.HasPrefix(s.name, "directory") || s.name == "dll0" || s.name == "dll1") {
								t.Fatal("metadata helper exposed as native symbol", s)
							}
						}
						t.Run("bigobj", func(t *testing.T) {
							file, err := parse("split.obj", promoteBigCOFF(t, data))
							if err != nil {
								t.Fatal(err)
							}
							if len(file.obj.info.Unsupported) != 0 || !reflect.DeepEqual(file.obj.info.Imports, o.info.Imports) || !reflect.DeepEqual(file.obj.groups, o.groups) {
								t.Fatal("bigobj lost split imports", file.obj.info)
							}
						})
					})
				}
			}
			t.Run(target+"/"+shape.name+"/copy_on_write", func(t *testing.T) {
				o := stagedSplitDirectory(t, target, shape)
				before := copyDirectoryObject(o)
				out, ok := newCOFFImportGraph([]*object{o}).convert(o)
				if !ok || len(out.info.Imports) != 5 || !reflect.DeepEqual(o, before) {
					t.Fatal("split conversion mutated input or lost imports", ok)
				}
			})
		}
	}
}

func TestCOFFSplitImportValidation(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			base := stagedSplitDirectory(t, target, splitDirectoryShapes[4])
			dirs := repeatedSections(base, ".idata$2")
			null := directorySection(t, base, ".idata$3")
			var helper int
			for i, s := range base.symbols {
				if s.name == "directory1" {
					helper = i
				}
			}
			if helper == 0 || len(dirs) != 2 {
				t.Fatal("missing split helper fixture")
			}
			cases := []struct {
				name string
				edit func(*object)
			}{
				{"missing_null", func(o *object) { o.sections[null].name = ".unknown" }},
				{"partial_null", func(o *object) { s := o.sections[null]; s.size--; s.data = s.data[:s.size] }},
				{"oversized_null", func(o *object) { s := o.sections[null]; s.size += 20; s.data = append(s.data, make([]byte, 20)...) }},
				{"nonzero_null", func(o *object) { o.sections[null].data[19] = 1 }},
				{"duplicate_null", func(o *object) { s := *o.sections[null]; o.sections = append(o.sections, &s) }},
				{"relocated_null", func(o *object) { r := o.relocs[0]; r.section = null; r.offset = 0; o.relocs = append(o.relocs, r) }},
				{"executable_null", func(o *object) { o.sections[null].exec = true }},
				{"overaligned_null", func(o *object) { o.sections[null].align = 16 }},
				{"overaligned_fragment", func(o *object) { o.sections[dirs[1]].align = 16 }},
				{"grouped_null", func(o *object) {
					o.groups = append(o.groups, sectionGroup{sections: []int{null}, selection: 2, key: "directory_end"})
				}},
				{"partial_fragment", func(o *object) { s := o.sections[dirs[1]]; s.size--; s.data = s.data[:s.size] }},
				{"empty_fragment", func(o *object) { s := o.sections[dirs[1]]; s.size = 0; s.data = nil }},
				{"early_null", func(o *object) {
					o.sections[null].name = ".unknown"
					clear(o.sections[dirs[0]].data)
					var kept []relocation
					for _, r := range o.relocs {
						if r.section != dirs[0] {
							kept = append(kept, r)
						}
					}
					o.relocs = kept
				}},
				{"double_terminator", func(o *object) { s := o.sections[dirs[1]]; s.size += 20; s.data = append(s.data, make([]byte, 20)...) }},
				{"fragment_unknown_fixup", func(o *object) { directoryRelocation(t, o, dirs[1], 12).typ = 0xffff }},
				{"fragment_missing_name", func(o *object) { directoryRelocation(t, o, dirs[1], 12).offset = 8 }},
				{"fragment_external_name", func(o *object) { r := directoryRelocation(t, o, dirs[1], 12); o.symbols[r.symbol].section = 0 }},
				{"middle_descriptor_helper", func(o *object) { o.symbols[helper].value++ }},
				{"outside_descriptor_helper", func(o *object) { o.symbols[helper].value = 20 }},
				{"middle_name_helper", func(o *object) {
					for _, s := range o.symbols {
						if s.name == "dll1" {
							s.name, s.value = "middle_name", s.value+1
							o.symbols = append(o.symbols, s)
							return
						}
					}
				}},
				{"duplicate_helper_name", func(o *object) { s := o.symbols[helper]; o.symbols = append(o.symbols, s) }},
				{"helper_alias", func(o *object) { o.symbols[helper].alias = &weakAlias{} }},
				{"live_descriptor_reference", func(o *object) {
					i := len(o.sections)
					o.sections = append(o.sections, &section{name: ".data$helper", size: 8, data: make([]byte, 8)})
					o.relocs = append(o.relocs, relocation{section: i, symbol: helper, typ: 1})
				}},
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					o := copyDirectoryObject(base)
					c.edit(o)
					before := copyDirectoryObject(o)
					if _, ok := newCOFFImportGraph([]*object{o}).convert(o); ok {
						t.Fatal("malformed split directory accepted")
					}
					if !reflect.DeepEqual(o, before) {
						t.Fatal("rejected split directory mutated input")
					}
				})
			}
		})
	}
}

func TestCOFFSplitImportSystemControl(t *testing.T) {
	if _, err := exec.LookPath("lld-link"); err != nil {
		t.Skip("optional cross-target PE control; native Windows requires lld-link")
	}
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "consumer.c")
			if err := os.WriteFile(source, []byte(directoryConsumer), 0600); err != nil {
				t.Fatal(err)
			}
			consumer := compile(t, source, source+".obj", "--target="+target, "-O2")
			for _, shape := range splitDirectoryShapes {
				t.Run(shape.name, func(t *testing.T) {
					data, _ := coffDirectoryAssembly(t, target, importDirectoryAssemblyOptions(target, directoryGroups(), []int{1, 0}, shape.omit, shape.layout))
					temporary := t.TempDir()
					imports := filepath.Join(temporary, "imports.obj")
					output := filepath.Join(temporary, "control.dll")
					if err := os.WriteFile(imports, data, 0600); err != nil {
						t.Fatal(err)
					}
					linkDirectoryControl(t, target, output, imports, consumer)
					// This independent reader also checks descriptor order, cache bytes
					// when present, and the contiguous native null terminator.
					checkBoundDirectoryControl(t, target, output, shape.layout, []int{1, 0})
					if len(shape.omit) != 0 {
						checkDirectoryLookupRVAs(t, output, []bool{shape.omit[1], shape.omit[0]})
					}
				})
			}
		})
	}
}

func TestNativeCOFFSplitImports(t *testing.T) {
	for _, shape := range splitDirectoryShapes {
		t.Run(shape.name, func(t *testing.T) { testNativeCOFFImportDirectoryLayout(t, shape.omit, shape.layout) })
	}
}

func TestNativeCOFFSplitImportMixed(t *testing.T) {
	for _, shape := range splitDirectoryShapes[4:] {
		t.Run(shape.name, func(t *testing.T) { testNativeCOFFImportDirectoryMixedLayout(t, shape.omit, shape.layout) })
	}
}
