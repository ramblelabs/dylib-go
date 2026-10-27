package dylib

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func directoryMixedAssembly(target string, comdat bool) string {
	extra := mixedImportAssembly(target)
	if comdat {
		extra = mixedCOMDATImportAssembly(target)
	}
	prefix := ""
	if strings.HasPrefix(target, "i686") {
		prefix = "_"
	}
	return strings.ReplaceAll(extra, prefix+"add\n", prefix+"first\n")
}

func TestCOFFImportDirectoryMixed(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		for _, kind := range []string{"ordinary", "comdat"} {
			t.Run(target+"/"+kind, func(t *testing.T) {
				assembly := importDirectoryAssembly(target, directoryGroups()) + directoryMixedAssembly(target, kind == "comdat")
				_, converted := coffAssembly(t, target, assembly)
				if len(converted.info.Imports) != 5 || len(converted.info.Unsupported) != 0 {
					t.Fatal(converted.info)
				}
				kept, life := 0, 0
				for _, s := range converted.sections {
					if s != nil && s.size != 0 {
						kept++
						if s.lifecycle != 0 {
							life++
						}
					}
				}
				if kept != 5 || life != 2 {
					t.Fatal("ordinary sections lost", kept, life)
				}
				if kind == "comdat" && len(converted.groups) != 4 {
					t.Fatal("COMDAT groups lost", converted.groups)
				}
				_, raw := coffAssembly(t, target, strings.Replace(assembly, ".idata$2", ".unused2", 1))
				for _, s := range raw.sections {
					if s != nil && s.name == ".unused2" {
						s.name = ".idata$2"
					}
				}
				before := copyDirectoryObject(raw)
				out, ok := newCOFFImportGraph([]*object{raw}).convert(raw)
				if !ok || len(out.symbols) != len(raw.symbols) || len(out.sections) != len(raw.sections) {
					t.Fatal("mixed conversion lost original indices")
				}
				if !reflect.DeepEqual(raw, before) {
					t.Fatal("mixed conversion changed staged input")
				}
				for _, name := range []string{"mixed", "mixed_read", "mixed_bias", "mixed_zero", "dependency"} {
					for i, s := range raw.symbols {
						if s.name == name && out.symbols[i] != s {
							t.Fatal("ordinary identity changed", name)
						}
					}
				}
			})
		}
	}
}

func TestNativeCOFFImportDirectoryMixed(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native import directories require Windows")
	}
	needNative(t)
	target := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH] + "-pc-windows-msvc"
	for _, selection := range []string{"ordinary", "comdat", "discard"} {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(selection+"/"+opt, func(t *testing.T) {
				dir := t.TempDir()
				source := filepath.Join(dir, "provider.c")
				if err := os.WriteFile(source, []byte("static int count; int add(int a,int b){++count;return a+b;} int events(int reset,int unused){if(reset)count=0;return count;} int value=42;"), 0600); err != nil {
					t.Fatal(err)
				}
				obj := compile(t, source, source+".obj", opt)
				def := filepath.Join(dir, "provider.def")
				if err := os.WriteFile(def, []byte("EXPORTS\nadd @7\nevents\nvalue DATA\n"), 0600); err != nil {
					t.Fatal(err)
				}
				first := filepath.Join(dir, "first.dll")
				command(t, "lld-link", "/dll", "/noentry", "/nodefaultlib", "/def:"+def, "/out:"+first, obj)
				b, err := os.ReadFile(first)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "second.dll"), b, 0600); err != nil {
					t.Fatal(err)
				}
				depSource := filepath.Join(dir, "dependency.c")
				if err := os.WriteFile(depSource, []byte("int dependency(int a,int b){return a+b+1;}"), 0600); err != nil {
					t.Fatal(err)
				}
				dependency := compile(t, depSource, depSource+".obj", opt)
				dependent, err := os.ReadFile(dependency)
				if err != nil {
					t.Fatal(err)
				}
				imports, _ := coffAssembly(t, target, importDirectoryAssembly(target, directoryGroups())+directoryMixedAssembly(target, selection != "ordinary"))
				poison, _ := coffAssembly(t, target, importDirectoryAssembly(target, []directoryFixture{{"not-installed-directory.dll", []longImportFixture{{"poison_directory", "absent", 0, false}}}}))
				var override string
				if selection == "discard" {
					key := "mixed_bias"
					if runtime.GOARCH == "386" {
						key = "_" + key
					}
					b, _ := coffAssembly(t, target, ".section .data$mix,\"dw\",discard,"+key+"\n.global "+key+"\n"+key+":\n.long 7\n")
					override = filepath.Join(dir, "override.obj")
					if err := os.WriteFile(override, b, 0600); err != nil {
						t.Fatal(err)
					}
				}
				for _, mode := range []string{"object", "archive", "retry"} {
					t.Run(mode, func(t *testing.T) {
						observer := New(Options{})
						defer observer.Close()
						load(t, observer, first)
						call(t, observer, "events", 1, 0, 0)
						s := New(Options{})
						defer s.Close()
						if override != "" {
							load(t, s, override)
						}
						path := filepath.Join(dir, mode+".lib")
						if mode == "object" {
							path = filepath.Join(dir, "mixed.obj")
							if err := os.WriteFile(path, imports, 0600); err != nil {
								t.Fatal(err)
							}
							load(t, s, path, dependency)
						} else {
							parts := [][]byte{imports, poison}
							if mode == "archive" {
								parts = append(parts, dependent)
							}
							if err := os.WriteFile(path, longImportArchive(parts), 0600); err != nil {
								t.Fatal(err)
							}
							load(t, s, path)
						}
						if mode == "retry" {
							if err := s.Link("mixed"); err == nil || !strings.Contains(err.Error(), "dependency") || s.image != nil {
								t.Fatal("expected unpublished dependency failure", err)
							}
							call(t, observer, "events", 0, 0, 0)
							load(t, s, dependency)
						}
						if err := s.Link("mixed"); err != nil {
							t.Fatal(err)
						}
						count, read := int32(1), int32(62)
						if selection == "discard" {
							count, read = 0, 7
						}
						call(t, observer, "events", 0, 0, count)
						call(t, s, "mixed_read", 0, 0, read)
						call(t, s, "mixed", 20, 22, 42)
						call(t, s, "mixed_dependency", 20, 22, 43)
						if len(s.libs) != 2 {
							t.Fatal("unused poison member loaded or DLL ownership lost", len(s.libs))
						}
						if err := s.Close(); err != nil {
							t.Fatal(err)
						}
						want := int32(3)
						if selection == "discard" {
							want = 1
						}
						call(t, observer, "events", 0, 0, want)
					})
				}
			})
		}
	}
}
