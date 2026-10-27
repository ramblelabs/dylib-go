package dylib

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func machoPublicRawSource(kind, name string) string {
	return ".section __DATA,__public," + kind + "\n.globl " + name + ",plain\n.weak_definition " + name + ",plain\n" + name + ":\nplain:\n.quad 9\n.subsections_via_symbols\n"
}

func TestMachOPublicAliasRawNames(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		for _, kind := range []string{"regular", "coalesced"} {
			for _, name := range []string{"_z", "_z.name"} {
				t.Run(target+"/"+kind+"/"+name, func(t *testing.T) {
					path := machoCoalescedAssembly(t, target, machoPublicRawSource(kind, name))
					b, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					weak := parseMachoAliases(t, b)
					if len(weak.machoAtoms) != 1 || weak.symbols[weak.machoAtoms[0].key].name != "plain" {
						t.Fatal("storage ordered by normalized API name")
					}
					provider := &object{info: weak.info, symbols: []symbol{{name: "plain", section: -1, value: 42, global: true}}}
					selected, err := coalesceObjects([]*object{weak, provider})
					if err != nil {
						t.Fatal(err)
					}
					if selected[0].sections[weak.machoAtoms[0].section] != nil {
						t.Fatal("discarded public alias group still owns storage")
					}
					defs, err := definitions(selected)
					if err != nil {
						t.Fatal(err)
					}
					im := &image{defs: defs}
					for _, api := range []string{"plain", strings.TrimPrefix(name, "_")} {
						address, err := im.lookup(api)
						if err != nil || address != 42 {
							t.Fatal(api, address, err)
						}
					}
				})
			}
			t.Run(target+"/"+kind+"/normalized_collision", func(t *testing.T) {
				source := ".section __DATA,__public," + kind + "\n.globl _z,z\n.weak_definition _z,z\n_z:\nz:\n.quad 9\n.subsections_via_symbols\n"
				path := machoCoalescedAssembly(t, target, source)
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				f, err := parse("collision.o", b)
				if kind == "coalesced" {
					if err == nil && len(f.info.Unsupported) == 0 {
						t.Fatal("ambiguous normalized alias names accepted")
					}
				} else if err != nil || len(f.info.Unsupported) != 0 || len(f.obj.machoAtoms) != 0 {
					t.Fatal("unqualified ordinary storage changed", err)
				}
			})
		}
	}
}

func TestNativeMachOPublicAliasRawNames(t *testing.T) {
	target := needMachOCoalescedNative(t)
	sdk := machoCoalescedSDK(t)
	for _, opt := range []string{"-O0", "-O2"} {
		for _, kind := range []string{"regular", "coalesced"} {
			for _, name := range []string{"_z", "_z.name"} {
				t.Run(opt+"/"+kind+"/"+name, func(t *testing.T) {
					weak := machoCoalescedAssembly(t, target, machoPublicRawSource(kind, name))
					provider := machoCoalescedAssembly(t, target, ".data\n.globl plain\nplain:\n.quad 42\n")
					dir := t.TempDir()
					src := filepath.Join(dir, "probe.c")
					text := fmt.Sprintf("extern long a[] __asm__(%q),b[] __asm__(\"plain\");int raw_check(int x,int y){return a[0]==42&&b[0]==42?x+y:-1;}\n#ifdef STANDALONE\nint main(){return raw_check(20,22)==42?0:1;}\n#endif\n", name)
					if err := os.WriteFile(src, []byte(text), 0600); err != nil {
						t.Fatal(err)
					}
					probe := compile(t, src, src+".o", opt, "--target="+target)
					s := New(Options{})
					defer s.Close()
					load(t, s, probe, weak, provider)
					call(t, s, "raw_check", 20, 22, 42)
					exe := filepath.Join(dir, "control")
					command(t, compiler(), "--target="+target, "-isysroot", sdk, "-Wl,-syslibroot,"+sdk, "-Wl,-dead_strip", opt, "-DSTANDALONE", src, weak, provider, "-o", exe)
					command(t, exe)
				})
			}
		}
	}
}
