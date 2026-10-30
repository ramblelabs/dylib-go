package dylib

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestCOFFDelayCompatImports(t *testing.T) {
	for _, target := range []string{"i686-pc-windows-msvc", "i686-w64-windows-gnu"} {
		for _, entry := range delayEntries {
			for _, kind := range []string{"pure", "mixed", "comdat"} {
				t.Run(target+"/"+entry.public+"/"+kind, func(t *testing.T) {
					extra := compatImportAlias(entry.public)
					if kind == "mixed" {
						extra += mixedImportAssembly(target)
					} else if kind == "comdat" {
						extra += mixedCOMDATImportAssembly(target)
					}
					parts, objects := delayFixturePartsExtra(t, target, []delayFixtureEntry{entry}, extra)
					before := make([]*object, len(objects))
					for i, o := range objects {
						before[i] = copyDirectoryObject(o)
					}
					g := newCOFFImportGraph(objects)
					out, ok := g.convert(objects[1])
					if !ok || len(g.delays.helpers) != 2 || len(out.info.Imports) != 1 || !out.info.Imports[0].Delay || out.info.Imports[0].Name != entry.name || out.info.Imports[0].Ordinal != entry.ordinal {
						t.Fatal("compatibility delay import lost", ok, out)
					}
					defs, err := definitions([]*object{out})
					if err != nil {
						t.Fatal(err)
					}
					imported := defs["__imp_"+entry.public].sym().imported.entry
					im := &image{base: 0x10000000, defs: defs, imports: map[*coffImport]uintptr{imported: 0x12345678}, got: map[uintptr]uintptr{}, mem: make([]byte, 64), pointerSize: 4, stubStart: 64}
					aliases := 0
					for i, s := range out.symbols {
						if s.name == "__imp_"+entry.public && s.imported != nil {
							address, err := im.symbol(out, i, false)
							if err != nil || address != im.base || s.imported.entry != imported || !s.imported.indirect {
								t.Fatal("delay aliases lost shared IAT", address, err)
							}
							aliases++
						}
					}
					if aliases != 2 || im.gotNext != 4 || le.Uint32(im.mem) != 0x12345678 {
						t.Fatal("duplicate delay IAT storage", aliases, im.gotNext)
					}
					if checkCOFFDelayImports([]*object{out}, DelayImportsDisabled) == nil || checkCOFFDelayImports([]*object{out}, DelayImportsEager) != nil {
						t.Fatal("compatibility aliases bypassed delay policy")
					}
					if kind != "pure" {
						if len(out.symbols) != len(objects[1].symbols) || len(out.sections) != len(objects[1].sections) || !reflect.DeepEqual(out.groups, objects[1].groups) {
							t.Fatal("mixed indices or groups changed")
						}
						for i, s := range out.sections {
							if s != nil && !reflect.DeepEqual(s, objects[1].sections[i]) {
								t.Fatal("ordinary section changed", i)
							}
						}
					}
					for i, o := range objects {
						if !reflect.DeepEqual(o, before[i]) {
							t.Fatal("staged snapshot changed", i)
						}
					}
					for _, big := range []bool{false, true} {
						data := parts
						if big {
							data = nil
							for _, b := range parts {
								data = append(data, promoteBigCOFF(t, b))
							}
						}
						f, err := parse("compat-delay.lib", longImportArchive(data))
						if err != nil || !reflect.DeepEqual(f.members[1].info.Imports, out.info.Imports) {
							t.Fatal("archive lost delay import", err, big)
						}
					}
				})
			}
		}
	}
}

func TestCOFFDelayCompatValidation(t *testing.T) {
	for _, target := range []string{"i686-pc-windows-msvc", "i686-w64-windows-gnu"} {
		for _, kind := range []string{"different_name", "different_offset", "different_section", "weak_alias", "duplicate_thunk", "private_iat", "grouped_thunk"} {
			t.Run(target+"/"+kind, func(t *testing.T) {
				_, objects := delayFixturePartsExtra(t, target, delayEntries[:1], compatImportAlias("add")+delayMixedAssembly(target, true))
				o := objects[1]
				index := -1
				for i, s := range o.symbols {
					if s.name == "__imp_add" && s.global && s.section > 0 {
						index = i
					}
				}
				if index < 0 {
					t.Fatal("missing alias")
				}
				switch kind {
				case "different_name":
					o.symbols[index].name = "__imp_other"
				case "different_offset":
					o.symbols[index].value = 1
				case "different_section":
					o.symbols[index].section = directorySection(t, o, ".text")
				case "weak_alias":
					o.symbols[index].alias = &weakAlias{target: 0, search: 3}
				case "duplicate_thunk":
					o.symbols = append(o.symbols, symbol{name: "add", global: true, section: directorySection(t, o, ".text")})
				case "private_iat":
					s := len(o.symbols)
					o.symbols = append(o.symbols, symbol{name: "private_iat", section: directorySection(t, o, ".idata$5")})
					o.relocs = append(o.relocs, relocation{section: directorySection(t, o, ".data$mix"), symbol: s, typ: 6, pair: -1})
				case "grouped_thunk":
					o.groups = append(o.groups, sectionGroup{key: "add", sections: []int{directorySection(t, o, ".text")}, selection: 2})
				}
				before := copyDirectoryObject(o)
				g := newCOFFImportGraph(objects)
				if _, ok := g.convert(o); ok || len(g.delays.helpers) != 0 {
					t.Fatal("invalid compatibility template consumed helpers")
				}
				if !reflect.DeepEqual(o, before) {
					t.Fatal("rejected input changed")
				}
			})
		}
	}
}

func TestCOFFGNUCompatDelayFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/coffdelay/windows_386_compat.a")
	if err != nil {
		t.Fatal(err)
	}
	f, err := parse("compat-delay.a", b)
	if err != nil {
		t.Fatal(err)
	}
	imports, aliases := 0, 0
	for _, m := range f.members {
		for _, imp := range m.info.Imports {
			if !imp.Delay || imp.DLL != "probe.dll" || imp.Name != "add" && imp.Ordinal != 7 {
				t.Fatal(imp)
			}
			imports++
		}
		for _, s := range m.obj.symbols {
			if s.imported != nil && s.imported.indirect {
				aliases++
			}
		}
	}
	if imports != 2 || aliases != 4 {
		t.Fatal("GNU compatibility delay imports lost", imports, aliases)
	}
}

func TestNativeCOFFDelayCompatMixed(t *testing.T) {
	if runtime.GOARCH != "386" {
		t.Skip("GNU compatibility names require i386")
	}
	testNativeCOFFDelayMixed(t, true)
}

const compatDelayConsumer = `typedef int (*Entry)(int,int);
extern Entry legacy __asm__("___imp_add");
extern Entry modern __asm__("__imp__add");
__declspec(dllimport) int numbered(int,int);
int delay_call(int a,int b) {
    Entry *volatile legacy_slot = &legacy, *volatile modern_slot = &modern;
    if (legacy_slot != modern_slot) return -1000;
    return (*legacy_slot)(a,b) + (*modern_slot)(a,b) + numbered(a,b);
}`

func compatDelayControl(t *testing.T, dir, opt string) (consumer, control string) {
	t.Helper()
	const target = "i686-pc-windows-msvc"
	src := filepath.Join(dir, "compat.c")
	if err := os.WriteFile(src, []byte(compatDelayConsumer), 0600); err != nil {
		t.Fatal(err)
	}
	consumer = compile(t, src, src+".obj", "--target="+target, opt)
	src = filepath.Join(dir, "dependency.c")
	if err := os.WriteFile(src, []byte("int dependency(int a,int b){return a+b+1;}"), 0600); err != nil {
		t.Fatal(err)
	}
	dependency := compile(t, src, src+".obj", "--target="+target, opt)
	b, err := os.ReadFile("testdata/coffdelay/windows_386_compat.a")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := archiveEntries(b, false)
	if err != nil {
		t.Fatal(err)
	}
	// LLD orders GNU import contributions by member name. Retain that order
	// when the control separates delay tables from synthetic ordinary imports.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	var parts [][]byte
	for _, e := range entries {
		parts = append(parts, e.Data)
	}
	// GNU's i386 assembler does not emit MSVC SafeSEH compatibility records.
	control = linkDelayControl(t, target, opt, dir, consumer, dependency, parts, "/safeseh:no")
	return
}

func TestCOFFGNUCompatDelaySystemControl(t *testing.T) {
	if _, err := exec.LookPath("lld-link"); err != nil {
		t.Skip("optional cross-target PE control")
	}
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) { compatDelayControl(t, t.TempDir(), opt) })
	}
}

func TestNativeCOFFGNUCompatDelayImports(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "386" {
		t.Skip("GNU compatibility delay fixture requires Windows Go 386")
	}
	needNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			dir, dll := delayProvider(t, "i686-pc-windows-msvc", opt)
			consumer, control := compatDelayControl(t, dir, opt)
			observer := New(Options{})
			defer observer.Close()
			load(t, observer, dll)
			call(t, observer, "provider_calls", 0, 0, 0)
			lib := "testdata/coffdelay/windows_386_compat.a"
			rejected := New(Options{LibraryPaths: []string{dir}})
			defer rejected.Close()
			load(t, rejected, consumer, lib)
			if err := rejected.Link("delay_call"); err == nil || !strings.Contains(err.Error(), "delay imports") || rejected.image != nil || len(rejected.libs) != 0 {
				t.Fatal("compatibility imports bypassed default policy", err)
			}
			call(t, observer, "provider_calls", 0, 0, 0)
			t.Run("system", func(t *testing.T) {
				s := New(Options{})
				defer s.Close()
				load(t, s, control)
				defer func() { _, _ = callInt32(s, "release_provider", 0, 0) }()
				call(t, s, "observed", 0, 0, 0)
				call(t, s, "delay_call", 20, 22, 126)
				call(t, s, "observed", 0, 0, 2)
				call(t, s, "delay_call", 21, 22, 129)
				call(t, s, "observed", 0, 0, 2)
				call(t, s, "release_provider", 0, 0, 1)
			})
			call(t, observer, "provider_calls", 0, 0, 6)
			t.Run("eager", func(t *testing.T) {
				s := New(Options{DelayImports: DelayImportsEager, LibraryPaths: []string{dir}})
				defer s.Close()
				load(t, s, consumer, lib)
				if err := s.Link("delay_call"); err != nil {
					t.Fatal(err)
				}
				call(t, observer, "provider_calls", 0, 0, 6)
				call(t, s, "delay_call", 20, 22, 126)
				call(t, s, "delay_call", 21, 22, 129)
				if len(s.libs) != 1 || len(s.image.objects) != 3 {
					t.Fatal("compatibility imports duplicated providers or extracted helpers")
				}
			})
			call(t, observer, "provider_calls", 0, 0, 12)
		})
	}
}
