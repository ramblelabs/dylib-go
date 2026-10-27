package dylib

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func machoSharedBSS(t *testing.T, target, variant string) (string, []byte) {
	t.Helper()
	source := strings.Replace(machoPublicBSSSource, "_public_b =", ".alt_entry _public_b\n_public_b =", 1)
	path := machoCoalescedAssembly(t, target, source)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if variant == "gb" {
		header := machoPointerHeader(t, b, "__bss")
		le.PutUint32(b[header+64:], le.Uint32(b[header+64:])&^0xff|0xc)
		path = filepath.Join(t.TempDir(), "gb.o")
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return path, b
}

func TestMachOSharedPublicBSS(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		for _, variant := range []string{"zero", "gb"} {
			t.Run(target+"/"+variant, func(t *testing.T) {
				_, b := machoSharedBSS(t, target, variant)
				weak := parseMachoAliases(t, b)
				if len(weak.machoAtoms) != 1 || weak.symbols[weak.machoAtoms[0].key].name != "public_a" || weak.sections[weak.machoAtoms[0].section].data != nil {
					t.Fatal("shared BSS lost its non-alternate owner")
				}
				for mode := 0; mode < 4; mode++ {
					for _, order := range []string{"weak_first", "strong_first"} {
						t.Run(fmt.Sprintf("override%d/%s", mode, order), func(t *testing.T) {
							inputs := []*object{weak}
							for i, name := range []string{"public_a", "public_b"} {
								if mode&(1<<i) == 0 {
									continue
								}
								p := &object{info: weak.info, symbols: []symbol{{name: name, section: -1, value: uint64(0x123400 + 0x100*i), global: true}}}
								if order == "strong_first" {
									inputs = append([]*object{p}, inputs...)
								} else {
									inputs = append(inputs, p)
								}
							}
							selected, err := coalesceObjects(inputs)
							if err != nil {
								t.Fatal(err)
							}
							im := machoCoalescedImage(t, selected)
							a, err := im.lookup("public_a")
							if err != nil {
								t.Fatal(err)
							}
							b, err := im.lookup("public_b")
							if err != nil || mode&1 != 0 && a != 0x123400 || mode&2 != 0 && b != 0x123500 || mode&2 == 0 && b != a {
								t.Fatal("shared BSS public selection", a, b, err)
							}
							refs, err := im.lookup("zero_refs")
							if err != nil {
								t.Fatal(err)
							}
							for i, want := range []uintptr{a, b, a + 8, a + 24} {
								if got := le.Uint64(im.mem[refs-im.base+uintptr(i)*8:]); got != uint64(want) {
									t.Fatal("shared BSS private pointer", i, got, want)
								}
							}
						})
					}
				}
			})
		}
	}
}

func TestNativeMachOSharedPublicBSS(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			providers := machoPublicProviders(t, target)
			src := filepath.Join(t.TempDir(), "probe.c")
			text := `#include <stdint.h>
extern long public_a[],public_b[],*zero_refs[];
int zero_check(int mode,int value){
 long *volatile pa=public_a,*volatile pb=public_b;
 if(zero_refs[0]!=pa || zero_refs[1]!=pb || (uintptr_t)zero_refs[2]!=(uintptr_t)pa+8 || (uintptr_t)zero_refs[3]!=(uintptr_t)pa+24)return -1;
 if(public_a[0]!=(mode&1?42:0) || public_b[0]!=(mode&2?84:(mode&1?42:0)))return -2;
 if(!(mode&2) && pa!=pb)return -3;
 return value;
}`
			if err := os.WriteFile(src, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			probe := compile(t, src, src+".o", opt, "--target="+target)
			for _, variant := range []string{"zero", "gb"} {
				t.Run(variant, func(t *testing.T) {
					weak, b := machoSharedBSS(t, target, variant)
					// S_GB_ZEROFILL is rejected by older system archivers.
					archive := filepath.Join(t.TempDir(), "zero.a")
					data := append([]byte("!<arch>\n"), archiveMemberBytes("zero.o/", b)...)
					if err := os.WriteFile(archive, data, 0600); err != nil {
						t.Fatal(err)
					}
					for mode := 0; mode < 4; mode++ {
						for _, input := range []string{"objects", "archive"} {
							t.Run(fmt.Sprintf("override%d/%s", mode, input), func(t *testing.T) {
								path := weak
								if input == "archive" {
									path = archive
								}
								inputs := []string{probe, path}
								for i, provider := range providers {
									if mode&(1<<i) != 0 {
										if input == "archive" {
											inputs = append([]string{provider}, inputs...)
										} else {
											inputs = append(inputs, provider)
										}
									}
								}
								s := New(Options{})
								defer s.Close()
								load(t, s, inputs...)
								for n := int32(0); n < 5; n++ {
									call(t, s, "zero_check", int32(mode), 42+n, 42+n)
								}
							})
						}
					}
				})
			}
		})
	}
}
