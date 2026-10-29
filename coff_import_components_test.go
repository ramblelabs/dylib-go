package dylib

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type componentDirectoryShape struct {
	name               string
	namespace          string
	multi, nullInTable bool
	crossArchive       bool
	omit               []bool
	timestamps         []uint32
}

var componentDirectoryShapes = []componentDirectoryShape{
	{name: "split"},
	{name: "multi_owner", multi: true, nullInTable: true},
	{name: "bound", multi: true, nullInTable: true, timestamps: []uint32{1, 0x12345678}},
	{name: "iat_only", multi: true, omit: []bool{true, true}},
	{name: "mixed_ilt", multi: true, nullInTable: true, omit: []bool{false, true}, timestamps: []uint32{1, 0}},
}

func (shape componentDirectoryShape) layout() directoryLayout {
	return directoryLayout{fragments: true, separateNull: true, publicHelpers: true, repeated: true, timestamps: shape.timestamps}
}

// Partition genuine assembler output at section boundaries. Global labels
// make cross-file RVAs explicit; no synthetic object records are needed.
func componentImportParts(t *testing.T, target string, shape componentDirectoryShape, extra string, reverse bool) ([][]byte, []*object) {
	t.Helper()
	assembly := importDirectoryAssemblyOptions(target, directoryGroups(), []int{1, 0}, shape.omit, shape.layout())
	var parts [5]strings.Builder
	prefix := ""
	if strings.HasPrefix(target, "i686") {
		prefix = ".def @feat.00; .scl 3; .type 0; .endef\n.set @feat.00,1\n"
	}
	group, bucket := 0, -1
	for _, line := range strings.Split(assembly, "\n") {
		if strings.HasPrefix(line, ".section ") {
			name := strings.Split(strings.TrimPrefix(line, ".section "), ",")[0]
			if strings.HasPrefix(name, ".id00") {
				group = int(name[5] - '1')
			}
			switch name[len(name)-1] {
			case '2':
				bucket = 0
			case '3':
				bucket = 0
				if shape.nullInTable {
					bucket = 3
				}
			case '4':
				bucket = 1
			case '5':
				bucket = 3
				if shape.multi {
					bucket += group
				}
			case '6', '7':
				bucket = 2
			default:
				t.Fatal("unexpected component section", name)
			}
		} else if line == ".text" {
			bucket = 3
			if shape.multi {
				bucket += group
			}
		}
		if bucket < 0 {
			continue
		}
		if strings.HasSuffix(line, ":") && (strings.HasPrefix(line, "ilt") || strings.HasPrefix(line, "iat") || strings.HasPrefix(line, "hint")) {
			fmt.Fprintf(&parts[bucket], ".global %s\n", strings.TrimSuffix(line, ":"))
		}
		parts[bucket].WriteString(line + "\n")
	}
	parts[2].WriteString(extra) // Ordinary code/lifecycle can live in a metadata provider.
	var data [][]byte
	var objects []*object
	for i := range parts {
		if parts[i].Len() == 0 {
			continue
		}
		body := parts[i].String()
		if shape.namespace != "" {
			var replacements []string
			for _, name := range []string{"first", "second", "numbered", "directory", "hint", "ilt", "iat", "dll0", "dll1"} {
				replacements = append(replacements, name, shape.namespace+name)
			}
			body = strings.NewReplacer(replacements...).Replace(body)
		}
		b, o := coffDirectoryAssembly(t, target, prefix+body)
		data = append(data, b)
		objects = append(objects, o)
	}
	if reverse {
		for i, j := 0, len(data)-1; i < j; i, j = i+1, j-1 {
			data[i], data[j] = data[j], data[i]
			objects[i], objects[j] = objects[j], objects[i]
		}
	}
	return data, objects
}

func TestCOFFComponentImportTargets(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		for _, shape := range componentDirectoryShapes {
			for _, reverse := range []bool{false, true} {
				for _, kind := range []string{"pure", "ordinary", "comdat"} {
					if kind != "pure" && shape.name != "multi_owner" {
						continue
					}
					t.Run(fmt.Sprintf("%s/%s/reverse_%t/%s", target, shape.name, reverse, kind), func(t *testing.T) {
						extra := ""
						if kind != "pure" {
							extra = directoryMixedAssembly(target, kind == "comdat")
						}
						parts, objects := componentImportParts(t, target, shape, extra, reverse)
						before := make([]*object, len(objects))
						for i, o := range objects {
							before[i] = copyDirectoryObject(o)
						}
						graph := newCOFFImportGraph(objects)
						var imports []ImportInfo
						for _, o := range objects {
							out, ok := graph.convert(o)
							if !ok || len(out.info.Unsupported) != 0 || len(out.coffImportGroup) != len(objects) || len(out.symbols) != len(o.symbols) || len(out.sections) != len(o.sections) {
								t.Fatal("component failed or original indices lost", ok, o.info, out)
							}
							imports = append(imports, out.info.Imports...)
						}
						if len(imports) != 5 {
							t.Fatal("component lost/duplicated IAT identities", imports)
						}
						for i, o := range objects {
							if !reflect.DeepEqual(o, before[i]) {
								t.Fatal("conversion changed source member", i)
							}
						}
						archive, err := parse("component.lib", longImportArchive(parts))
						if err != nil {
							t.Fatal(err)
						}
						for _, m := range archive.members {
							if len(m.info.Unsupported) != 0 || len(m.obj.coffImportGroup) != len(parts) {
								t.Fatal("archive composition failed", m.info)
							}
						}
						t.Run("bigobj", func(t *testing.T) {
							var big [][]byte
							for _, b := range parts {
								big = append(big, promoteBigCOFF(t, b))
							}
							f, err := parse("component-big.lib", longImportArchive(big))
							if err != nil {
								t.Fatal(err)
							}
							for i, m := range f.members {
								if len(m.info.Unsupported) != 0 || !reflect.DeepEqual(m.info.Imports, archive.members[i].info.Imports) {
									t.Fatal("bigobj changed component identities", m.info)
								}
							}
						})
					})
				}
			}
		}
	}
}

func TestCOFFComponentImportSystemControl(t *testing.T) {
	if _, err := exec.LookPath("lld-link"); err != nil {
		t.Skip("optional cross-target PE controls; native Windows requires the linker")
	}
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "consumer.c")
			if err := os.WriteFile(source, []byte(directoryConsumer), 0600); err != nil {
				t.Fatal(err)
			}
			consumer := compile(t, source, source+".obj", "--target="+target, "-O2")
			for _, shape := range componentDirectoryShapes {
				t.Run(shape.name, func(t *testing.T) {
					parts, _ := componentImportParts(t, target, shape, "", true)
					paths := writeComponentObjects(t, t.TempDir(), parts)
					output := filepath.Join(filepath.Dir(paths[0]), "control.dll")
					linkDirectoryInputs(t, target, output, paths, consumer)
					checkBoundDirectoryControl(t, target, output, shape.layout(), []int{1, 0})
					if len(shape.omit) != 0 {
						checkDirectoryLookupRVAs(t, output, []bool{shape.omit[1], shape.omit[0]})
					}
				})
			}
		})
	}
}

func TestCOFFComponentImportValidation(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			_, base := componentImportParts(t, target, componentDirectoryShapes[1], "", false)
			if len(base) != 5 {
				t.Fatal("five-member fixture required")
			}
			dirs := repeatedSections(base[0], ".idata$2")
			lookup := repeatedSections(base[1], ".idata$4")[1]
			null := directorySection(t, base[3], ".idata$3")
			cases := []struct {
				name string
				edit func([]*object) []*object
			}{
				{"missing_descriptor", func(o []*object) []*object { return o[1:] }},
				{"missing_lookup", func(o []*object) []*object { return append(o[:1], o[2:]...) }},
				{"missing_names", func(o []*object) []*object { return append(o[:2], o[3:]...) }},
				{"missing_later_iat", func(o []*object) []*object { return o[:4] }},
				{"ambiguous_names", func(o []*object) []*object { return append(o, copyDirectoryObject(o[2])) }},
				{"descriptor_cycle", func(o []*object) []*object {
					r := directoryRelocation(t, o[0], dirs[1], 12)
					o[0].symbols[r.symbol] = symbol{name: "directory1", section: dirs[0], global: true}
					return o
				}},
				{"external_rva_bounds", func(o []*object) []*object { le.PutUint32(o[0].sections[dirs[1]].data[16:], 0xffffffff); return o }},
				{"unknown_lookup_fixup", func(o []*object) []*object { directoryRelocation(t, o[1], lookup, 0).typ = 0xffff; return o }},
				{"duplicate_lookup_fixup", func(o []*object) []*object {
					o[1].relocs = append(o[1].relocs, *directoryRelocation(t, o[1], lookup, 0))
					return o
				}},
				{"nonzero_null", func(o []*object) []*object { o[3].sections[null].data[19] = 1; return o }},
				{"missing_null", func(o []*object) []*object { o[3].sections[null].name = ".unknown"; return o }},
				{"duplicate_null", func(o []*object) []*object {
					s := *o[3].sections[null]
					o[4].sections = append(o[4].sections, &s)
					return o
				}},
				{"early_null", func(o []*object) []*object {
					clear(o[0].sections[dirs[0]].data)
					var kept []relocation
					for _, r := range o[0].relocs {
						if r.section != dirs[0] {
							kept = append(kept, r)
						}
					}
					o[0].relocs = kept
					return o
				}},
				{"overaligned_fragment", func(o []*object) []*object { o[0].sections[dirs[1]].align = 16; return o }},
				{"interior_hint_label", func(o []*object) []*object {
					for _, s := range o[2].symbols {
						if strings.HasPrefix(s.name, "hint") && s.section > 0 {
							s.name = "interior_hint"
							s.value++
							o[2].symbols = append(o[2].symbols, s)
							break
						}
					}
					return o
				}},
				{"live_external_helper", func(o []*object) []*object {
					member := o[3]
					index := len(member.sections)
					member.sections = append(member.sections, &section{name: ".data$probe", size: 8, data: make([]byte, 8)})
					sym := len(member.symbols)
					member.symbols = append(member.symbols, symbol{name: "dll1", global: true})
					member.relocs = append(member.relocs, relocation{section: index, symbol: sym, typ: 1, pair: -1})
					return o
				}},
				{"helper_alias", func(o []*object) []*object {
					for i, s := range o[2].symbols {
						if s.name == "dll1" {
							o[2].symbols[i].alias = &weakAlias{}
						}
					}
					return o
				}},
				{"empty_thunk_alias", func(o []*object) []*object {
					for i, s := range o[3].symbols {
						if s.name == "first" {
							o[3].symbols[i].value = o[3].sections[s.section].size
						}
					}
					return o
				}},
				{"grouped_metadata", func(o []*object) []*object {
					o[0].groups = append(o[0].groups, sectionGroup{key: "directory0", selection: 2, sections: []int{dirs[0]}})
					return o
				}},
				{"wrong_arch", func(o []*object) []*object { o[2].info.Arch = "foreign"; return o }},
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					objects := make([]*object, len(base))
					for i, o := range base {
						objects[i] = copyDirectoryObject(o)
					}
					objects = c.edit(objects)
					before := make([]*object, len(objects))
					for i, o := range objects {
						before[i] = copyDirectoryObject(o)
					}
					g := newCOFFImportGraph(objects)
					for _, o := range objects {
						if _, ok := g.convert(o); ok {
							t.Fatal("malformed component converted", o.info)
						}
					}
					for i, o := range objects {
						if !reflect.DeepEqual(o, before[i]) {
							t.Fatal("rejection changed source member", i)
						}
					}
				})
			}
		})
	}
}

// Exercise selection independently of execution. Foreign fixture metadata is
// replaced only for the existing archive host gate; native calls run below.
func TestCOFFComponentImportSelection(t *testing.T) {
	target := "x86_64-pc-windows-msvc"
	parts, _ := componentImportParts(t, target, componentDirectoryShapes[1], directoryMixedAssembly(target, true), false)
	unusedShape := componentDirectoryShapes[1]
	unusedShape.namespace = "missing_"
	unusedParts, _ := componentImportParts(t, target, unusedShape, "", false)
	f, err := parse("component.lib", longImportArchive(append(append([][]byte{}, parts...), unusedParts...)))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range f.members {
		if len(m.obj.coffImportGroup) != len(parts) || len(m.info.Unsupported) != 0 {
			t.Fatal("separate named components did not normalize", m.info)
		}
		m.obj.info.Format = map[string]string{"windows": "COFF", "linux": "ELF", "darwin": "Mach-O"}[runtime.GOOS]
		m.obj.info.OS, m.obj.info.Arch = runtime.GOOS, runtime.GOARCH
		m.obj.info.Bits = 64
		if runtime.GOARCH == "386" {
			m.obj.info.Bits = 32
		}
	}
	s := New(Options{})
	defer s.Close()
	s.files = []*file{f}
	selected, err := s.selectObjects([]string{"first"})
	if err != nil || len(selected) != len(parts) {
		t.Fatal("import did not select ordinary metadata-provider code/lifecycle atomically", err, len(selected))
	}
	life := 0
	for _, o := range selected {
		for _, sec := range o.sections {
			if sec != nil && sec.lifecycle != 0 {
				life++
			}
		}
	}
	if life != 2 {
		t.Fatal("metadata-provider lifecycle was lost", life)
	}
	unused := New(Options{})
	defer unused.Close()
	unused.files = []*file{f}
	selected, err = unused.selectObjects(nil)
	if err != nil || len(selected) != 0 {
		t.Fatal("unselected component extracted", err, len(selected))
	}
}

func writeComponentObjects(t *testing.T, dir string, parts [][]byte) []string {
	t.Helper()
	var paths []string
	for i, b := range parts {
		p := filepath.Join(dir, fmt.Sprintf("component%d.obj", i))
		if err := os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return paths
}

func TestNativeCOFFComponentImports(t *testing.T) {
	for _, shape := range componentDirectoryShapes {
		t.Run(shape.name, func(t *testing.T) { testNativeCOFFImportDirectoryComponent(t, shape.omit, shape.layout(), &shape) })
	}
}

func TestNativeCOFFComponentImportMixed(t *testing.T) {
	shape := componentDirectoryShapes[1]
	testNativeCOFFImportDirectoryMixedComponent(t, shape.omit, shape.layout(), &shape)
}
