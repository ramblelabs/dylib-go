package dylib

import (
	"debug/macho"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestMachOSharedPublicNeighbors(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		for _, kind := range []string{"regular", "coalesced"} {
			for _, neighbor := range []string{"strong", "private"} {
				t.Run(target+"/"+kind+"/"+neighbor, func(t *testing.T) {
					source := ".section __DATA,__shared," + kind + "\n"
					if neighbor == "strong" {
						source += ".globl _before\n_before:\n"
					} else {
						source += "lbefore:\n"
					}
					source += ".quad _before_missing\n.globl _owner,_zalias\n.weak_definition _owner,_zalias\n_owner:\n.alt_entry _zalias\n_zalias:\n.quad 9\n.subsections_via_symbols\n"
					path := machoCoalescedAssembly(t, target, source)
					b, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					o := parseMachoAliases(t, b)
					if len(o.machoAtoms) != 1 || o.symbols[o.machoAtoms[0].key].name != "owner" || o.machoAtoms[0].start != 8 {
						t.Fatal("alternate claimed its preceding neighbor", o.machoAtoms)
					}
					beforeSymbols := append([]symbol(nil), o.symbols...)
					beforeRelocs := append([]relocation(nil), o.relocs...)
					provider := &object{info: o.info, symbols: []symbol{{name: "owner", global: true, section: -1, value: 0x123400}}}
					selected, err := coalesceObjects([]*object{o, provider})
					if err != nil {
						t.Fatal(err)
					}
					refs, err := references(selected[0])
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, ref := range refs {
						found = found || ref.name == "before_missing"
					}
					if !found {
						t.Fatal("preceding neighbor lost its fixup", refs)
					}
					if !reflect.DeepEqual(beforeSymbols, o.symbols) || !reflect.DeepEqual(beforeRelocs, o.relocs) {
						t.Fatal("neighbor selection changed staged input")
					}
				})
			}
		}
	}
}

func TestMachOSharedPublicSubsections(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		for _, kind := range []string{"regular", "coalesced"} {
			t.Run(target+"/"+kind, func(t *testing.T) {
				_, b := machoCoalescedFixture(t, target, "macho_shared_aliases.S", machoSharedFlags(kind, 0, "-O0")...)
				le.PutUint32(b[24:], le.Uint32(b[24:])&^uint32(macho.FlagSubsectionsViaSymbols))
				o := parseMachoAliases(t, b)
				want := 0
				if kind == "coalesced" {
					want = 1
				}
				if len(o.machoAtoms) != want {
					t.Fatal("whole-section shared ownership", o.machoAtoms)
				}
			})
		}
	}
}

func TestNativeMachOSharedPublicRetry(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		providers := machoSharedProviders(t, target)
		for _, kind := range []string{"regular", "coalesced"} {
			for _, mode := range []string{"owner", "before"} {
				forms := []int{0, 1, 4, 5}
				if mode == "before" {
					forms = []int{1, 3, 5}
				}
				for _, form := range forms {
					t.Run(opt+"/"+kind+"/"+mode+"/"+machoSharedForms[form], func(t *testing.T) {
						flags := machoSharedFlags(kind, form, opt)
						if mode == "owner" {
							flags = append(flags, "-DDYLIB_SHARED_POISON")
						}
						weak, _ := machoCoalescedFixture(t, target, "macho_shared_aliases.S", flags...)
						probe, _ := machoCoalescedFixture(t, target, "macho_shared_aliases.c", flags...)
						s := New(Options{})
						defer s.Close()
						load(t, s, probe, machoCoalescedArchive(t, weak))
						callMode := int32(0)
						missing := "shared_owner_missing"
						if mode == "owner" {
							if form%2 != 0 {
								load(t, s, providers[2])
							}
							if form < 4 {
								load(t, s, providers[1])
								callMode = 2
							}
						} else {
							missing = "shared_before_missing"
							if form != 3 {
								load(t, s, providers[0])
								callMode = 1
							}
						}
						if err := s.Link("shared_check"); err == nil || !strings.Contains(err.Error(), missing) || s.image != nil {
							t.Fatal("retained neighbor/owner dependency lost", err)
						}
						if mode == "owner" {
							load(t, s, providers[0])
							callMode |= 1
						} else {
							load(t, s, providers[2])
						}
						if err := s.Link("shared_check"); err != nil {
							t.Fatal("failed shared alias retry", err)
						}
						for n := int32(0); n < 5; n++ {
							call(t, s, "shared_check", callMode, 42+n, 42+n)
						}
					})
				}
			}
		}
	}
}
