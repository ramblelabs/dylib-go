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

var repeatedImportShapes = append([]struct {
	name string
	omit []bool
}{{"present", nil}}, iatLookupShapes...)

func repeatedDirectory(t *testing.T, target string, omit []bool) *object {
	t.Helper()
	assembly := importDirectoryAssemblyLayout(target, directoryGroups(), nil, omit, true)
	_, o := coffAssembly(t, target, strings.Replace(assembly, ".idata$2", ".unused2", 1))
	for _, s := range o.sections {
		if s != nil && s.name == ".unused2" {
			s.name = ".idata$2"
		}
	}
	return o
}

func repeatedSections(o *object, name string) []int {
	var indexes []int
	for i, s := range o.sections {
		if s != nil && s.name == name {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func TestCOFFRepeatedImportTables(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		for _, shape := range repeatedImportShapes {
			for _, reverse := range []bool{false, true} {
				for _, kind := range []string{"pure", "ordinary", "comdat"} {
					t.Run(fmt.Sprintf("%s/%s/reverse_%t/%s", target, shape.name, reverse, kind), func(t *testing.T) {
						order := []int{0, 1}
						if reverse {
							order = []int{1, 0}
						}
						extra := ""
						if kind != "pure" {
							extra = directoryMixedAssembly(target, kind == "comdat")
						}
						_, control := coffAssembly(t, target, importDirectoryAssemblyTables(target, directoryGroups(), order, shape.omit)+extra)
						data, o := coffAssembly(t, target, importDirectoryAssemblyLayout(target, directoryGroups(), order, shape.omit, true)+extra)
						if len(o.info.Unsupported) != 0 || len(o.info.Imports) != 5 || !reflect.DeepEqual(o.info.Imports, control.info.Imports) {
							t.Fatal("repeated sections changed DLL/name/ordinal identities", o.info, control.info.Imports)
						}
						t.Run("bigobj", func(t *testing.T) {
							file, err := parse("repeated.obj", promoteBigCOFF(t, data))
							if err != nil {
								t.Fatal(err)
							}
							if len(file.obj.info.Unsupported) != 0 || !reflect.DeepEqual(file.obj.info.Imports, o.info.Imports) || !reflect.DeepEqual(file.obj.groups, o.groups) {
								t.Fatal("bigobj changed import identities or COMDAT ownership", file.obj.info, file.obj.groups)
							}
						})
						if len(o.groups) != len(control.groups) {
							t.Fatal("ordinary COMDAT groups lost", o.groups)
						}
						for i, group := range o.groups {
							want := control.groups[i]
							if group.key != want.key || group.selection != want.selection || len(group.sections) != len(want.sections) {
								t.Fatal("COMDAT selection changed", group, want)
							}
							for j, section := range group.sections {
								if o.sections[section].name != control.sections[want.sections[j]].name {
									t.Fatal("COMDAT section ownership changed", group, want)
								}
							}
						}
					})
				}
			}
		}
		t.Run(target+"/copy_on_write", func(t *testing.T) {
			o := repeatedDirectory(t, target, nil)
			for _, name := range []string{".idata$4", ".idata$5", ".idata$6", ".idata$7"} {
				if len(repeatedSections(o, name)) != 2 {
					t.Fatal("fixture needs distinct same-name sections", name)
				}
			}
			// Two DLLs intentionally start their IATs and hint strings at offset zero.
			before := copyDirectoryObject(o)
			out, ok := newCOFFImportGraph([]*object{o}).convert(o)
			if !ok || len(out.info.Imports) != 5 || !reflect.DeepEqual(o, before) {
				t.Fatal("conversion lost identities or mutated input", ok)
			}
		})
	}
}

func TestCOFFRepeatedImportValidation(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			base := repeatedDirectory(t, target, nil)
			iat := repeatedSections(base, ".idata$5")[1]
			ilt := repeatedSections(base, ".idata$4")[1]
			dir := directorySection(t, base, ".idata$2")
			cases := []struct {
				name   string
				change func(*object)
			}{
				{"partial_second_iat", func(o *object) { s := o.sections[iat]; s.data = s.data[:len(s.data)-1]; s.size-- }},
				{"partial_second_ilt", func(o *object) { s := o.sections[ilt]; s.data = s.data[:len(s.data)-1]; s.size-- }},
				{"unclaimed_second_iat", func(o *object) {
					s := o.sections[iat]
					s.data = append(s.data, make([]byte, o.info.Bits/8)...)
					s.data[len(s.data)-1] = 1
					s.size = uint64(len(s.data))
				}},
				{"overlapping_second_iat", func(o *object) {
					r := directoryRelocation(t, o, dir, 36)
					r.symbol = directoryRelocation(t, o, dir, 16).symbol
				}},
				{"wrong_second_lookup", func(o *object) {
					r := directoryRelocation(t, o, dir, 20)
					r.symbol = directoryRelocation(t, o, dir, 0).symbol
				}},
				{"misaligned_second_iat", func(o *object) { le.PutUint32(o.sections[dir].data[36:], 1) }},
				{"extra_second_relocation", func(o *object) {
					r := *directoryRelocation(t, o, iat, 0)
					r.offset = o.sections[iat].size - uint64(o.info.Bits/8)
					o.relocs = append(o.relocs, r)
				}},
				{"wrong_thunk_slot", func(o *object) {
					first, second := -1, -1
					for i, s := range o.symbols {
						if s.name == "__imp_first" || s.name == "__imp__first" {
							first = i
						}
						if s.name == "__imp_second" || s.name == "__imp__second" {
							second = i
						}
					}
					if first < 0 || second < 0 {
						t.Fatal("fixture lacks import symbols")
					}
					for i := range o.relocs {
						r := &o.relocs[i]
						if o.sections[r.section].name == ".text" && r.symbol == second {
							r.symbol = first
							return
						}
					}
					t.Fatal("fixture lacks second thunk")
				}},
				{"duplicate_directory", func(o *object) { o.sections = append(o.sections, &section{name: ".idata$2"}) }},
				{"duplicate_text", func(o *object) { o.sections = append(o.sections, &section{name: ".text"}) }},
				{"executable_second_iat", func(o *object) { o.sections[iat].exec = true }},
				{"owned_second_iat", func(o *object) {
					o.groups = append(o.groups, sectionGroup{key: "second_iat", selection: 2, sections: []int{iat}})
				}},
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					o := copyDirectoryObject(base)
					c.change(o)
					before := copyDirectoryObject(o)
					if _, ok := newCOFFImportGraph([]*object{o}).convert(o); ok {
						t.Fatal("malformed repeated table accepted")
					}
					if !reflect.DeepEqual(o, before) {
						t.Fatal("failed conversion mutated input")
					}
				})
			}
		})
	}
}

func TestCOFFRepeatedImportSystemControl(t *testing.T) {
	if _, err := exec.LookPath("lld-link"); err != nil {
		t.Skip("optional cross-target PE control; native Windows tests require lld-link")
	}
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		for _, shape := range repeatedImportShapes {
			t.Run(target+"/"+shape.name, func(t *testing.T) {
				data, _ := coffAssembly(t, target, importDirectoryAssemblyLayout(target, directoryGroups(), []int{1, 0}, shape.omit, true))
				dir := t.TempDir()
				imports, source := filepath.Join(dir, "imports.obj"), filepath.Join(dir, "consumer.c")
				if err := os.WriteFile(imports, data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(source, []byte(directoryConsumer), 0600); err != nil {
					t.Fatal(err)
				}
				object := compile(t, source, source+".obj", "--target="+target, "-O2")
				control := filepath.Join(dir, "control.dll")
				if len(shape.omit) == 0 {
					directoryControl(t, target, control, imports, object)
				} else {
					linkDirectoryControl(t, target, control, imports, object)
					checkDirectoryLookupRVAs(t, control, []bool{shape.omit[1], shape.omit[0]})
				}
			})
		}
	}
}

func TestNativeCOFFRepeatedImportTables(t *testing.T) {
	for _, shape := range repeatedImportShapes {
		t.Run(shape.name, func(t *testing.T) { testNativeCOFFImportDirectory(t, shape.omit, true) })
	}
}
func TestNativeCOFFRepeatedImportMixed(t *testing.T) {
	for _, shape := range repeatedImportShapes {
		t.Run(shape.name, func(t *testing.T) { testNativeCOFFImportDirectoryMixed(t, shape.omit, true) })
	}
}
