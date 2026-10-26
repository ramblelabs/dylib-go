package dylib

import (
	"os"
	"path/filepath"
	"testing"
)

const machoPublicBSSSource = `.globl _public_a,_public_b
.weak_definition _public_a,_public_b
.zerofill __DATA,__bss,_public_a,24,4
_public_b = _public_a
.alt_entry lpublic_middle
lpublic_middle = _public_a + 8
.alt_entry lpublic_end
lpublic_end = _public_a + 24
.data
.p2align 3
.globl _zero_refs
_zero_refs:
.quad _public_a,_public_b,lpublic_middle,lpublic_end
.subsections_via_symbols
`

func TestMachOPublicAliasBSS(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		t.Run(target, func(t *testing.T) {
			path := machoCoalescedAssembly(t, target, machoPublicBSSSource)
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			weak := parseMachoAliases(t, b)
			if len(weak.machoAtoms) != 1 {
				t.Fatal("BSS public group missing")
			}
			atom := weak.machoAtoms[0]
			if weak.sections[atom.section].data != nil {
				t.Fatal("BSS unexpectedly file backed")
			}
			for _, mode := range []string{"retained", "replaced"} {
				for _, order := range []string{"weak_first", "strong_first"} {
					t.Run(mode+"/"+order, func(t *testing.T) {
						inputs := []*object{weak}
						if mode == "replaced" {
							p := &object{info: weak.info, symbols: []symbol{{name: "public_b", section: -1, value: 0x123400, global: true}}}
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
						value, err := im.lookup("public_b")
						if err != nil || a != value {
							t.Fatal("BSS public address identity", a, value, err)
						}
						refs, err := im.lookup("zero_refs")
						if err != nil {
							t.Fatal(err)
						}
						for i, want := range []uintptr{value, value, value + 8, value + 24} {
							if got := le.Uint64(im.mem[refs-im.base+uintptr(i)*8:]); got != uint64(want) {
								t.Fatal("BSS alias pointer", i, got, want)
							}
						}
					})
				}
			}
		})
	}
}

func TestNativeMachOPublicAliasBSS(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			weak := machoCoalescedAssembly(t, target, machoPublicBSSSource)
			providers := machoPublicProviders(t, target)
			src := filepath.Join(t.TempDir(), "zero.c")
			text := `#include <stdint.h>
extern long public_a[],public_b[],*zero_refs[];
int zero_check(int mode,int value){
 long *volatile pa=public_a,*volatile pb=public_b;
 if(pa!=pb || zero_refs[0]!=pa || zero_refs[1]!=pb || (uintptr_t)zero_refs[2]!=(uintptr_t)pb+8 || (uintptr_t)zero_refs[3]!=(uintptr_t)pb+24)return -1;
 if(public_a[0]!=(mode?84:0) || public_b[0]!=(mode?84:0))return -2;
 return value;
}`
			if err := os.WriteFile(src, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			probe := compile(t, src, src+".o", opt, "--target="+target)
			for _, mode := range []string{"retained", "replaced"} {
				for _, input := range []string{"objects", "archive"} {
					t.Run(mode+"/"+input, func(t *testing.T) {
						path := weak
						if input == "archive" {
							path = machoCoalescedArchive(t, weak)
						}
						inputs := []string{probe, path}
						code := int32(0)
						if mode == "replaced" {
							inputs = append([]string{providers[1]}, inputs...)
							code = 1
						}
						s := New(Options{})
						defer s.Close()
						load(t, s, inputs...)
						for n := int32(0); n < 5; n++ {
							call(t, s, "zero_check", code, 42+n, 42+n)
						}
					})
				}
			}
		})
	}
}
