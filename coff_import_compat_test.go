package dylib

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// GNU i386 --compat-implib emits both the legacy and modern IAT spelling.
func compatLongImportAssembly(target string, entry longImportFixture) string {
	return longImportAssembly(target, entry) + compatImportAlias(entry.public)
}

func compatImportAlias(public string) string {
	legacy, modern := "___imp_"+public, "__imp__"+public
	return ".global " + legacy + "\n.set " + legacy + "," + modern + "\n"
}

func TestCOFFCompatLongImports(t *testing.T) {
	for _, target := range []string{"i686-pc-windows-msvc", "i686-w64-windows-gnu"} {
		for _, entry := range []longImportFixture{{"add", "add", 0, false}, {"numbered", "", 7, false}, {"value", "value", 0, true}} {
			for _, kind := range []string{"pure", "mixed", "comdat"} {
				t.Run(target+"/"+entry.public+"/"+kind, func(t *testing.T) {
					parts := longImportParts(t, target, "provider.dll")
					extra := ""
					if kind == "mixed" {
						extra = mixedImportAssembly(target)
					} else if kind == "comdat" {
						extra = mixedCOMDATImportAssembly(target)
					}
					data, original := coffAssembly(t, target, compatLongImportAssembly(target, entry)+extra)
					before := copyDirectoryObject(original)
					var objects []*object
					for _, b := range parts {
						f, err := parse("helper.obj", b)
						if err != nil {
							t.Fatal(err)
						}
						objects = append(objects, f.obj)
					}
					converted, ok := newCOFFImportGraph(append(objects, original)).convert(original)
					if !ok || !reflect.DeepEqual(original, before) {
						t.Fatal("conversion failed or changed input", ok)
					}
					for _, big := range []bool{false, true} {
						member := data
						if big {
							member = promoteBigCOFF(t, member)
						}
						f, err := parse("compat.lib", longImportArchive(append(parts, member)))
						if err != nil {
							t.Fatal(err)
						}
						o := f.members[2].obj
						if !reflect.DeepEqual(o.info.Imports, converted.info.Imports) {
							t.Fatal("archive changed import identities")
						}
						if len(o.info.Unsupported) != 0 || len(o.info.Imports) != 1 || o.info.Imports[0].Symbol != entry.public || o.info.Imports[0].Name != entry.export || o.info.Imports[0].Ordinal != entry.ordinal {
							t.Fatal("compatibility import lost", o.info)
						}
						defs, err := definitions([]*object{o})
						if err != nil {
							t.Fatal("same-cell aliases conflict", err)
						}
						imported := defs["__imp_"+entry.public].sym().imported.entry
						im := &image{base: 0x10000000, defs: defs, imports: map[*coffImport]uintptr{imported: 0x12345678}, got: map[uintptr]uintptr{}, mem: make([]byte, 64), pointerSize: 4, stubStart: 64}
						aliases := 0
						for i, s := range o.symbols {
							if s.name == "__imp_"+entry.public && s.imported != nil {
								address, err := im.symbol(o, i, false)
								if err != nil || address != im.base || s.imported.entry != imported || !s.imported.indirect {
									t.Fatal("aliases lost shared slot", address, err)
								}
								aliases++
							}
						}
						if aliases != 2 || im.gotNext != 4 || le.Uint32(im.mem) != 0x12345678 {
							t.Fatal("duplicate compatibility IAT storage", aliases, im.gotNext)
						}
						if kind != "pure" {
							if len(o.symbols) != len(original.symbols) || len(o.sections) != len(original.sections) || len(o.groups) != len(original.groups) {
								t.Fatal("mixed member indices or groups changed")
							}
							for i, s := range original.symbols {
								if s.section > 0 && o.sections[s.section] != nil && o.symbols[i] != s {
									t.Fatal("ordinary symbol changed", i)
								}
							}
							if !reflect.DeepEqual(o.groups, original.groups) {
								t.Fatal("ordinary groups changed")
							}
						}
					}
					if !reflect.DeepEqual(original, before) {
						t.Fatal("source snapshot changed")
					}
				})
			}
		}
	}
}

func TestCOFFCompatLongImportValidation(t *testing.T) {
	for _, target := range []string{"i686-pc-windows-msvc", "i686-w64-windows-gnu"} {
		parts := longImportParts(t, target, "provider.dll")
		for _, kind := range []string{"different_name", "different_offset", "different_section", "weak_alias"} {
			t.Run(target+"/"+kind, func(t *testing.T) {
				_, o := coffAssembly(t, target, compatLongImportAssembly(target, longImportFixture{"add", "add", 0, false}))
				index := -1
				for i, s := range o.symbols {
					if s.global && s.name == "__imp_add" {
						index = i
					}
				}
				if index < 0 {
					t.Fatal("missing compatibility alias")
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
				}
				before := copyDirectoryObject(o)
				var objects []*object
				for _, b := range parts {
					f, err := parse("helper.obj", b)
					if err != nil {
						t.Fatal(err)
					}
					objects = append(objects, f.obj)
				}
				if _, ok := newCOFFImportGraph(append(objects, o)).convert(o); ok {
					t.Fatal("conflicting compatibility alias accepted")
				}
				if !reflect.DeepEqual(o, before) {
					t.Fatal("failed conversion changed snapshot")
				}
			})
		}
	}
}

func TestGNUCompatImportLibraryFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/coffimports/windows_386_compat.a")
	if err != nil {
		t.Fatal(err)
	}
	f, err := parse("compat.a", b)
	if err != nil {
		t.Fatal(err)
	}
	imports, aliases := 0, 0
	for _, m := range f.members {
		imports += len(m.obj.info.Imports)
		if len(m.obj.info.Imports) == 0 {
			continue
		}
		if len(m.obj.info.Unsupported) != 0 {
			t.Fatal(m.obj.info)
		}
		if _, err := definitions([]*object{m.obj}); err != nil {
			t.Fatal(err)
		}
		for _, s := range m.obj.symbols {
			if s.imported != nil && s.imported.indirect {
				aliases++
			}
		}
	}
	if imports != 3 || aliases != 6 {
		t.Fatal("genuine compatibility imports lost", imports, aliases)
	}
}

func TestNativeGNUCompatImportLibraries(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "386" {
		t.Skip("GNU compatibility IAT spellings require Windows 386")
	}
	needNative(t)
	dll, _, _ := nativeImportFixture(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "compat.c")
			code := `typedef int (*Entry)(int,int);
extern Entry legacy __asm__("___imp_add");
extern Entry modern __asm__("__imp__add");
__declspec(dllimport) int numbered(int,int);
__declspec(dllimport) extern int value;
int compat_imported(int a,int b) {
    Entry *volatile legacy_slot = &legacy, *volatile modern_slot = &modern;
    if (legacy_slot != modern_slot) return -1000;
    return (*legacy_slot)(a,b) + (*modern_slot)(a,b) + numbered(a,b) + value - 126;
}`
			if err := os.WriteFile(src, []byte(code), 0600); err != nil {
				t.Fatal(err)
			}
			consumer := compile(t, src, src+".obj", opt)
			s := New(Options{LibraryPaths: []string{filepath.Dir(dll)}})
			defer s.Close()
			load(t, s, consumer, "testdata/coffimports/windows_386_compat.a")
			if err := s.Link("compat_imported"); err != nil {
				t.Fatal(err)
			}
			call(t, s, "compat_imported", 20, 22, 42)
			call(t, s, "compat_imported", 21, 22, 45)
			if len(s.libs) != 1 || len(s.image.objects) != 4 {
				t.Fatal("compatibility aliases extracted metadata or duplicated providers")
			}
		})
	}
}

func TestNativeCOFFCompatMixedImports(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "386" {
		t.Skip("GNU compatibility IAT spellings require Windows 386")
	}
	needNative(t)
	const target = "i686-pc-windows-msvc"
	dll, _, _ := nativeImportFixture(t)
	for _, selection := range []string{"ordinary", "comdat", "discard"} {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(selection+"/"+opt, func(t *testing.T) {
				dir := t.TempDir()
				source := filepath.Join(dir, "consumer.c")
				code := "extern int (**compat_slots[2])(int,int); int compat_indirect(int a,int b){if(compat_slots[0]!=compat_slots[1])return -1000; return (*compat_slots[0])(a,b)+(*compat_slots[1])(a,b);}\n"
				if err := os.WriteFile(source, []byte(code), 0600); err != nil {
					t.Fatal(err)
				}
				consumer := compile(t, source, source+".obj", opt)
				depSource := filepath.Join(dir, "dependency.c")
				if err := os.WriteFile(depSource, []byte("int dependency(int a,int b){return a+b+1;}"), 0600); err != nil {
					t.Fatal(err)
				}
				dependency := compile(t, depSource, depSource+".obj", opt)
				extra := mixedImportAssembly(target)
				if selection != "ordinary" {
					extra = mixedCOMDATImportAssembly(target)
				}
				extra += ".section .data$slots,\"dw\"\n.p2align 2\n.global _compat_slots\n_compat_slots:\n.long ___imp_add, __imp__add\n"
				parts := longImportParts(t, target, "provider.dll")
				data, _ := coffAssembly(t, target, compatLongImportAssembly(target, longImportFixture{"add", "add", 0, false})+extra)
				for _, mode := range []string{"object", "archive", "retry", "unused"} {
					t.Run(mode, func(t *testing.T) {
						s := New(Options{LibraryPaths: []string{filepath.Dir(dll)}})
						defer s.Close()
						if selection == "discard" {
							override, _ := coffAssembly(t, target, ".section .data$mix,\"dw\",discard,_mixed_bias\n.global _mixed_bias\n_mixed_bias:\n.long 7\n")
							load(t, s, writeComponentObjects(t, t.TempDir(), [][]byte{override})...)
						}
						members := append(append([][]byte(nil), parts...), data)
						if mode == "object" {
							members = parts
							load(t, s, writeComponentObjects(t, t.TempDir(), [][]byte{data})...)
						}
						lib := filepath.Join(t.TempDir(), "compat.lib")
						if err := os.WriteFile(lib, longImportArchive(members), 0600); err != nil {
							t.Fatal(err)
						}
						load(t, s, lib)
						if err := os.Remove(lib); err != nil {
							t.Fatal(err)
						}
						if mode == "retry" {
							if err := s.Link("mixed"); err == nil || !strings.Contains(err.Error(), "dependency") || s.image != nil {
								t.Fatal("missing dependency bypassed", err)
							}
						}
						load(t, s, dependency)
						if mode == "unused" {
							if err := s.Link("dependency"); err != nil || len(s.libs) != 0 {
								t.Fatal("unused compatibility member opened provider", err)
							}
							call(t, s, "dependency", 20, 22, 43)
							return
						}
						load(t, s, consumer)
						if err := s.Link("mixed", "compat_indirect"); err != nil {
							t.Fatal(err)
						}
						read := int32(62)
						if selection == "discard" {
							read = 7
						}
						call(t, s, "mixed_read", 0, 0, read)
						call(t, s, "mixed_dependency", 20, 22, 43)
						call(t, s, "mixed", 20, 22, 42)
						call(t, s, "compat_indirect", 20, 22, 84)
						if len(s.libs) != 1 {
							t.Fatal("compatibility aliases duplicated providers")
						}
						if err := s.Close(); err != nil {
							t.Fatal(err)
						}
					})
				}
			})
		}
	}
}
