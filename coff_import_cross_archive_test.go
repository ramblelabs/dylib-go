package dylib

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// Preserve member order within each container while deliberately distributing
// descriptor/name and lookup/IAT owners across separate archives.
func crossImportArchives(t *testing.T, dir string, parts [][]byte) []string {
	t.Helper()
	var groups [2][][]byte
	for i, b := range parts {
		groups[i%2] = append(groups[i%2], b)
	}
	var paths []string
	for i, group := range groups {
		origin := filepath.Join(dir, fmt.Sprintf("origin %d", i))
		if err := os.MkdirAll(origin, 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(origin, "imports.lib")
		if err := os.WriteFile(path, longImportArchive(group), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}

func TestCOFFCrossArchiveImports(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		for _, shape := range componentDirectoryShapes {
			for _, reverse := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/reverse_%t", target, shape.name, reverse), func(t *testing.T) {
					parts, _ := componentImportParts(t, target, shape, "", false)
					paths := crossImportArchives(t, t.TempDir(), parts)
					if reverse {
						paths[0], paths[1] = paths[1], paths[0]
					}
					var objects []*object
					for order, path := range paths {
						data, err := os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						f, err := parse(path, data)
						if err != nil {
							t.Fatal(err)
						}
						for member, m := range f.members {
							if len(m.obj.coffImportGroup) != 0 {
								t.Fatal("incomplete archive composed by itself")
							}
							m.obj.directory = filepath.Dir(path)
							m.obj.inputOrder, m.obj.memberOrder = order, member
							objects = append(objects, m.obj)
						}
					}
					before := make([]*object, len(objects))
					for i, o := range objects {
						before[i] = copyDirectoryObject(o)
					}
					_, converted := prepareCOFFImportInputs(nil, objects)
					count := 0
					for i, o := range converted {
						if len(o.info.Unsupported) != 0 || len(o.coffImportGroup) != len(parts) || len(o.symbols) != len(objects[i].symbols) || len(o.sections) != len(objects[i].sections) || o.directory != objects[i].directory || o.inputOrder != objects[i].inputOrder || o.memberOrder != objects[i].memberOrder {
							t.Fatal("cross-archive identity or origin lost", o.info)
						}
						count += len(o.info.Imports)
						if !reflect.DeepEqual(objects[i], before[i]) {
							t.Fatal("staged snapshot changed")
						}
					}
					if count != 5 {
						t.Fatal("imports duplicated or lost", count)
					}
					// The same graph can include an explicitly staged raw object.
					roots, members := prepareCOFFImportInputs(objects[:1], objects[1:])
					if len(roots[0].coffImportGroup) != len(parts) || len(members) != len(parts)-1 {
						t.Fatal("mixed raw/archive component lost")
					}
				})
			}
		}
	}
}

func TestCOFFCrossArchiveSelection(t *testing.T) {
	target := "x86_64-pc-windows-msvc"
	shape := componentDirectoryShapes[1]
	parts, _ := componentImportParts(t, target, shape, directoryMixedAssembly(target, true), false)
	poison := shape
	poison.namespace = "missing_"
	unused, _ := componentImportParts(t, target, poison, "", false)
	paths := crossImportArchives(t, t.TempDir(), append(parts, unused...))
	var all []*object
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parse(path, b)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range f.members {
			all = append(all, m.obj)
		}
	}
	_, views := prepareCOFFImportInputs(nil, all)
	// As in TestCOFFComponentImportSelection, substitute only host metadata
	// after validation. Native Windows tests execute the original bytes below.
	archive := &file{}
	for _, o := range views {
		o.info.Format = map[string]string{"windows": "COFF", "linux": "ELF", "darwin": "Mach-O"}[runtime.GOOS]
		o.info.OS, o.info.Arch, o.info.Bits = runtime.GOOS, runtime.GOARCH, 64
		if runtime.GOARCH == "386" {
			o.info.Bits = 32
		}
		archive.members = append(archive.members, &file{obj: o})
	}
	for _, root := range []string{"", "first"} {
		s := New(Options{})
		defer s.Close()
		s.files = []*file{archive}
		var roots []string
		if root != "" {
			roots = []string{root}
		}
		selected, err := s.selectObjects(roots)
		want := 0
		if root != "" {
			want = len(parts)
		}
		if err != nil || len(selected) != want {
			t.Fatal("component selection/laziness failed", err, len(selected), want)
		}
		if root != "" {
			life := 0
			for _, o := range selected {
				for _, sec := range o.sections {
					if sec != nil && sec.lifecycle != 0 {
						life++
					}
				}
			}
			if life != 2 {
				t.Fatal("provider lifecycle lost", life)
			}
		}
	}
}

func TestCOFFCrossArchiveValidation(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		_, base := componentImportParts(t, target, componentDirectoryShapes[1], "", false)
		for _, kind := range []string{"missing_iat", "ambiguous_provider", "foreign_provider", "unconnected_null", "ordinary_helper_reference"} {
			t.Run(target+"/"+kind, func(t *testing.T) {
				var objects []*object
				for _, o := range base {
					objects = append(objects, copyDirectoryObject(o))
				}
				switch kind {
				case "missing_iat":
					objects = objects[:len(objects)-1]
				case "ambiguous_provider":
					objects = append(objects, copyDirectoryObject(objects[2]))
				case "foreign_provider":
					objects[2].info.Arch = "foreign"
				case "unconnected_null":
					null := directorySection(t, objects[3], ".idata$3")
					objects[3].sections[null] = nil
					for i, s := range objects[3].symbols {
						if s.section == null {
							objects[3].symbols[i] = symbol{section: -3}
						}
					}
					_, orphan := coffDirectoryAssembly(t, target, ".section .idata$3,\"dr\"\n.zero 20\n")
					objects = append(objects, orphan)
				case "ordinary_helper_reference":
					o := objects[2]
					index := -1
					for i, s := range o.symbols {
						if s.section > 0 {
							index = i
							break
						}
					}
					if index < 0 {
						t.Fatal("missing metadata symbol")
					}
					o.sections = append(o.sections, &section{name: ".data$ordinary", size: 4, data: make([]byte, 4)})
					o.relocs = append(o.relocs, relocation{section: len(o.sections) - 1, symbol: index, pair: -1})
				}
				var before []*object
				for _, o := range objects {
					before = append(before, copyDirectoryObject(o))
				}
				_, views := prepareCOFFImportInputs(nil, objects)
				for i, o := range views {
					if len(o.coffImportGroup) != 0 || len(o.info.Imports) != 0 {
						t.Fatal("unqualified cross-archive composition accepted", o.info)
					}
					if !reflect.DeepEqual(objects[i], before[i]) {
						t.Fatal("failed composition changed snapshot")
					}
				}
			})
		}
	}
}

func TestArchiveSelectionPreservesCandidates(t *testing.T) {
	// An entry later in an archive refers back to its first member. Growing
	// the selected roots must not overwrite that unselected provider.
	for _, raw := range []bool{false, true} {
		t.Run(fmt.Sprint(raw), func(t *testing.T) {
			makeObject := func(name, dependency string) *object {
				o := &object{info: Info{Name: name, Kind: "object", Format: map[string]string{"windows": "COFF", "linux": "ELF", "darwin": "Mach-O"}[runtime.GOOS], OS: runtime.GOOS, Arch: runtime.GOARCH, Bits: 64}, sections: []*section{nil, {name: ".data", size: 8, align: 8, data: make([]byte, 8)}}, symbols: []symbol{{name: name, section: 1, global: true}}}
				if runtime.GOARCH == "386" {
					o.info.Bits = 32
				}
				if dependency != "" {
					o.symbols = append(o.symbols, symbol{name: dependency, global: true})
					o.relocs = []relocation{{section: 1, symbol: 1, pair: -1, typ: 1}}
				}
				return o
			}
			s := New(Options{})
			defer s.Close()
			if raw {
				s.files = append(s.files, &file{obj: makeObject("raw", "")})
			}
			s.files = append(s.files, &file{members: []*file{{obj: makeObject("dependency", "")}, {obj: makeObject("entry", "dependency")}, {obj: makeObject("unused", "not_installed")}}})
			selected, err := s.selectObjects([]string{"entry"})
			want := 2
			if raw {
				want++
			}
			if err != nil || len(selected) != want {
				t.Fatal("candidate list was overwritten", err, len(selected))
			}
			defs, err := definitions(selected)
			if err != nil || defs["entry"].o == nil || defs["dependency"].o == nil || defs["unused"].o != nil {
				t.Fatal("dependency/laziness changed", err)
			}
		})
	}
}

func TestCOFFCrossArchiveContributionOrder(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		for _, terminalFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/terminal_first_%t", target, terminalFirst), func(t *testing.T) {
				_, objects := componentImportParts(t, target, componentDirectoryShapes[1], "", false)
				dirs := repeatedSections(objects[0], ".idata$2")
				first, last := copyDirectoryObject(objects[0]), copyDirectoryObject(objects[0])
				remove := func(o *object, section int) {
					o.sections[section] = nil
					for i, s := range o.symbols {
						if s.section == section {
							o.symbols[i] = symbol{section: -3}
						}
					}
					var relocs []relocation
					for _, r := range o.relocs {
						if r.section != section {
							relocs = append(relocs, r)
						}
					}
					o.relocs = relocs
				}
				remove(first, dirs[1])
				remove(last, dirs[0])
				// Move the terminator into the final descriptor contribution.
				sec := last.sections[dirs[1]]
				sec.data = append(sec.data, make([]byte, 20)...)
				sec.size += 20
				remove(objects[3], directorySection(t, objects[3], ".idata$3"))
				members := append([]*object{first}, objects[1:]...)
				for i, o := range members {
					o.inputOrder, o.memberOrder = 0, i
				}
				last.inputOrder, last.memberOrder = 1, -1
				if terminalFirst {
					last.inputOrder = 0
					for _, o := range members {
						o.inputOrder = 1
					}
				}
				roots, archives := prepareCOFFImportInputs([]*object{last}, members)
				for _, o := range append(roots, archives...) {
					if terminalFirst {
						if len(o.coffImportGroup) != 0 {
							t.Fatal("input order hid a later descriptor")
						}
					} else if len(o.coffImportGroup) != len(members)+1 || len(o.info.Unsupported) != 0 {
						t.Fatal("raw roots reordered archive descriptors", o.info)
					}
				}
			})
		}
	}
}

func TestCOFFCrossArchiveSystemControl(t *testing.T) {
	if _, err := exec.LookPath("lld-link"); err != nil {
		t.Skip("optional PE control linker")
	}
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		for _, shape := range componentDirectoryShapes {
			t.Run(target+"/"+shape.name, func(t *testing.T) {
				dir := t.TempDir()
				parts, _ := componentImportParts(t, target, shape, "", false)
				paths := crossImportArchives(t, dir, parts)
				source := filepath.Join(dir, "consumer.c")
				if err := os.WriteFile(source, []byte(directoryConsumer), 0600); err != nil {
					t.Fatal(err)
				}
				consumer := compile(t, source, source+".obj", "--target="+target, "-O2")
				output := filepath.Join(dir, "control.dll")
				linkDirectoryInputs(t, target, output, []string{"/wholearchive:" + paths[1], "/wholearchive:" + paths[0]}, consumer)
				checkBoundDirectoryControl(t, target, output, shape.layout(), []int{1, 0})
			})
		}
	}
}

func TestNativeCOFFCrossArchiveImports(t *testing.T) {
	for _, shape := range componentDirectoryShapes {
		shape.crossArchive = true
		t.Run(shape.name, func(t *testing.T) { testNativeCOFFImportDirectoryComponent(t, shape.omit, shape.layout(), &shape) })
	}
}

func TestNativeCOFFCrossArchiveMixed(t *testing.T) {
	shape := componentDirectoryShapes[1]
	shape.crossArchive = true
	testNativeCOFFImportDirectoryMixedComponent(t, shape.omit, shape.layout(), &shape)
}
