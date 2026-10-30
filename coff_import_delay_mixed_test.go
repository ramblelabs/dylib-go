package dylib

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func delayMixedAssembly(target string, comdat bool) string {
	text := mixedImportAssembly(target)
	if comdat {
		text = mixedCOMDATImportAssembly(target)
	}
	prefix, word, alignment := "", ".quad ", "3"
	if strings.HasPrefix(target, "i686") {
		prefix, word, alignment = "_", ".long ", "2"
	}
	return text + ".section .rdata$mixiat,\"dr\"\n.p2align " + alignment + "\n.global " + prefix + "mixed_import_address\n" + prefix + "mixed_import_address:\n" + word + "__imp_" + prefix + "add\n"
}

func TestCOFFDelayMixed(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		for _, kind := range []string{"ordinary", "comdat"} {
			t.Run(target+"/"+kind, func(t *testing.T) {
				extra := delayMixedAssembly(target, kind == "comdat")
				parts, objects := delayFixturePartsExtra(t, target, delayEntries, extra)
				raw := objects[1]
				before := copyDirectoryObject(raw)
				out, ok := newCOFFImportGraph(objects).convert(raw)
				if !ok || len(out.info.Imports) != 1 || !out.info.Imports[0].Delay || len(out.info.Unsupported) != 0 || len(out.sections) != len(raw.sections) || len(out.symbols) != len(raw.symbols) {
					t.Fatal("mixed delay overlay lost imports or original indices", ok, out)
				}
				if !reflect.DeepEqual(raw, before) {
					t.Fatal("conversion changed input")
				}
				life := 0
				for i, sec := range out.sections {
					if sec != nil && sec.size != 0 {
						if !reflect.DeepEqual(sec, raw.sections[i]) {
							t.Fatal("ordinary section changed")
						}
						if sec.lifecycle != 0 {
							life++
						}
					}
				}
				if life != 2 || !reflect.DeepEqual(out.groups, raw.groups) {
					t.Fatal("lifecycle or groups lost", life)
				}
				for _, big := range []bool{false, true} {
					data := parts
					if big {
						data = nil
						for _, part := range parts {
							data = append(data, promoteBigCOFF(t, part))
						}
					}
					f, err := parse("mixed-delay.lib", longImportArchive(data))
					if err != nil || f == nil || !reflect.DeepEqual(f.members[1].info.Imports, out.info.Imports) {
						t.Fatal("archive changed mixed import", err, big)
					}
				}
			})
		}
	}
}

func TestCOFFDelayMixedValidation(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		for _, kind := range []string{"private_thunk", "private_iat", "private_lookup", "private_hint", "grouped_thunk", "associated_template"} {
			t.Run(target+"/"+kind, func(t *testing.T) {
				_, objects := delayFixturePartsExtra(t, target, delayEntries[:1], mixedCOMDATImportAssembly(target))
				o := objects[1]
				text, iat := directorySection(t, o, ".text"), directorySection(t, o, ".idata$5")
				if kind == "grouped_thunk" {
					o.groups = append(o.groups, sectionGroup{key: "add", sections: []int{text}, selection: 2})
				} else if kind == "associated_template" {
					for i := range o.groups {
						if o.groups[i].selection == 5 {
							o.groups[i].parent = iat
						}
					}
				} else {
					name := map[string]string{"private_thunk": ".text", "private_iat": ".idata$5", "private_lookup": ".idata$4", "private_hint": ".idata$6"}[kind]
					symbolIndex := len(o.symbols)
					o.symbols = append(o.symbols, symbol{name: "private_metadata", section: directorySection(t, o, name)})
					_, absolute, _ := delayRelocations(o.info.Arch)
					o.relocs = append(o.relocs, relocation{section: directorySection(t, o, ".data$mix"), symbol: symbolIndex, typ: absolute, pair: -1})
				}
				before := copyDirectoryObject(o)
				g := newCOFFImportGraph(objects)
				if _, ok := g.convert(o); ok || len(g.delays.helpers) != 0 {
					t.Fatal("invalid overlay consumed import or helper metadata")
				}
				if !reflect.DeepEqual(o, before) {
					t.Fatal("failed overlay changed input")
				}
			})
		}
	}
}

func TestCOFFDelayHelperCacheBounds(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			_, objects := delayFixtureParts(t, target, delayEntries[:1])
			cache := objects[0].sections[directorySection(t, objects[0], ".data")]
			for _, size := range []uint64{0, uint64(objects[0].info.Bits/8) - 1} {
				cache.size, cache.data = size, make([]byte, int(size))
				g := newCOFFImportGraph(objects)
				if _, ok := g.convert(objects[1]); ok || len(g.delays.helpers) != 0 {
					t.Fatal("descriptor module handle referenced storage without a complete pointer", size)
				}
			}
		})
	}
}

func TestNativeCOFFDelayMixed(t *testing.T) {
	testNativeCOFFDelayMixed(t, false)
}

func testNativeCOFFDelayMixed(t *testing.T, compat bool) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("native mixed delay imports require Windows")
	}
	needNative(t)
	target := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH] + "-pc-windows-msvc"
	for _, selection := range []string{"ordinary", "comdat", "discard"} {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(selection+"/"+opt, func(t *testing.T) {
				dir, dll := delayProvider(t, target, opt)
				source := filepath.Join(dir, "dependency.c")
				if err := os.WriteFile(source, []byte("int dependency(int a,int b){return a+b+1;}"), 0600); err != nil {
					t.Fatal(err)
				}
				dependency := compile(t, source, source+".obj", "--target="+target, opt)
				consumerSource := filepath.Join(dir, "indirect.c")
				consumerCode := "extern int (**mixed_import_address)(int,int); int mixed_indirect(int a,int b){return (*mixed_import_address)(a,b);}"
				indirectResult, indirectCalls := int32(42), int32(1)
				if compat {
					consumerCode = "extern int (**mixed_import_address)(int,int),(**mixed_compat_address)(int,int); int mixed_indirect(int a,int b){if(mixed_import_address!=mixed_compat_address)return -1000; return (*mixed_import_address)(a,b)+(*mixed_compat_address)(a,b);}"
					indirectResult, indirectCalls = 84, 2
				}
				if err := os.WriteFile(consumerSource, []byte(consumerCode), 0600); err != nil {
					t.Fatal(err)
				}
				consumer := compile(t, consumerSource, consumerSource+".obj", "--target="+target, opt)
				dep, err := os.ReadFile(dependency)
				if err != nil {
					t.Fatal(err)
				}
				extra := delayMixedAssembly(target, selection != "ordinary")
				if compat {
					extra += compatImportAlias("add") + ".section .rdata$mixiat,\"dr\"\n.p2align 2\n.global _mixed_compat_address\n_mixed_compat_address:\n.long ___imp_add\n"
				}
				parts, _ := delayFixturePartsExtra(t, target, []delayFixtureEntry{{"add", "add", 0}}, extra)
				var override []byte
				if selection == "discard" {
					key := "mixed_bias"
					if runtime.GOARCH == "386" {
						key = "_" + key
					}
					override, _ = coffAssembly(t, target, ".section .data$mix,\"dw\",discard,"+key+"\n.global "+key+"\n"+key+":\n.long 7\n")
				}
				for _, mode := range []string{"objects", "archive", "cross_archives", "retry", "unused"} {
					t.Run(mode, func(t *testing.T) {
						observer := New(Options{})
						defer observer.Close()
						load(t, observer, dll)
						call(t, observer, "provider_calls", 0, 0, 0)
						opts := Options{DelayImports: DelayImportsEager, LibraryPaths: []string{dir}}
						if mode == "unused" {
							opts.DelayImports = DelayImportsDisabled
						}
						s := New(opts)
						defer s.Close()
						if mode != "unused" {
							load(t, s, consumer)
						}
						if override != nil {
							load(t, s, writeComponentObjects(t, t.TempDir(), [][]byte{override})...)
						}
						stage := t.TempDir()
						if mode == "objects" {
							load(t, s, append(writeComponentObjects(t, stage, parts), dependency)...)
						} else {
							data := append([][]byte(nil), parts...)
							if mode != "retry" {
								data = append(data, dep)
							}
							subsets := [][][]byte{data}
							if mode == "cross_archives" {
								subsets = [][][]byte{data[:1], data[1:]}
							}
							for i, subset := range subsets {
								path := filepath.Join(stage, fmt.Sprintf("mixed%d.lib", i))
								if err := os.WriteFile(path, longImportArchive(subset), 0600); err != nil {
									t.Fatal(err)
								}
								load(t, s, path)
								if err := os.Remove(path); err != nil {
									t.Fatal(err)
								}
							}
						}
						if mode == "retry" {
							if err := s.Link("mixed"); err == nil || !strings.Contains(err.Error(), "dependency") || s.image != nil {
								t.Fatal("failed link published code", err)
							}
							call(t, observer, "provider_calls", 0, 0, 0)
							load(t, s, dependency)
						}
						if mode == "unused" {
							if err := s.Link("dependency"); err != nil || len(s.libs) != 0 {
								t.Fatal("unused mixed delay member opened provider", err)
							}
							call(t, s, "dependency", 20, 22, 43)
							if err := s.Close(); err != nil {
								t.Fatal(err)
							}
							call(t, observer, "provider_calls", 0, 0, 0)
							return
						}
						if err := s.Link("mixed"); err != nil {
							t.Fatal(err)
						}
						count, read := int32(1), int32(62)
						if selection == "discard" {
							count, read = 0, 7
						}
						call(t, observer, "provider_calls", 0, 0, count)
						call(t, s, "mixed_read", 0, 0, read)
						call(t, s, "mixed", 20, 22, 42)
						call(t, s, "mixed_indirect", 20, 22, indirectResult)
						call(t, s, "mixed_dependency", 20, 22, 43)
						if err := s.Close(); err != nil {
							t.Fatal(err)
						}
						want := 3 + indirectCalls
						if selection == "discard" {
							want = 1 + indirectCalls
						}
						call(t, observer, "provider_calls", 0, 0, want)
					})
				}
			})
		}
	}
}
