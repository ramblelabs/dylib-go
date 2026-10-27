package dylib

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMachOPublicAliasValidation(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		for _, kind := range []string{"regular", "coalesced"} {
			for _, mode := range []string{"shared_boundary", "shared_start", "missing_owner", "private_boundary", "private_start"} {
				t.Run(target+"/"+kind+"/"+mode, func(t *testing.T) {
					source := ".section __DATA,__public," + kind + "\n.globl _public_a,_public_b\n.weak_definition _public_a,_public_b\n"
					if mode == "shared_boundary" {
						source += "_public_a:\n.quad _missing\n.globl _public_next\n_public_next:\n.alt_entry _public_b\n_public_b:\n.quad _other_missing\n"
					} else if mode == "shared_start" {
						source += "_public_a:\n.alt_entry _public_b\n_public_b:\n.quad _missing\n.quad _other_missing\n"
					} else if mode == "private_boundary" {
						source += "_public_a:\n.quad _missing\nlprivate_next:\n.alt_entry _public_b\n_public_b:\n.quad _other_missing\n"
					} else if mode == "private_start" {
						source += "lprivate:\n.alt_entry _public_a\n_public_a:\n.alt_entry _public_b\n_public_b:\n.quad _missing\n.quad _other_missing\n"
					} else {
						source += "lprivate:\n.quad _missing\n.alt_entry _public_a\n_public_a:\n.alt_entry _public_b\n_public_b:\n.quad _other_missing\n"
					}
					source += ".subsections_via_symbols\n"
					path := machoCoalescedAssembly(t, target, source)
					b, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					f, err := parse("boundaries.o", b)
					shared := mode == "shared_boundary" || mode == "shared_start"
					if kind == "coalesced" && !shared {
						if err == nil && len(f.info.Unsupported) == 0 {
							t.Fatal("ambiguous explicit alias ownership accepted")
						}
						return
					}
					wantAtoms := 0
					if shared {
						wantAtoms = 1
						if mode == "shared_boundary" {
							wantAtoms = 2
						}
					}
					if err != nil || len(f.info.Unsupported) != 0 || len(f.obj.machoAtoms) != wantAtoms {
						t.Fatal("public boundary classification", err, f)
					}
					if shared {
						want := "public_a"
						if mode == "shared_boundary" {
							want = "public_next"
						}
						found := false
						for _, s := range f.obj.symbols {
							if s.name != "public_b" {
								continue
							}
							found = true
							if s.forward == nil || !s.forward.storage || s.forward.offset != 0 || f.obj.symbols[s.forward.target].name != want {
								t.Fatal("shared alias has wrong public owner", s)
							}
						}
						if !found {
							t.Fatal("shared alias missing")
						}
					}
					refs, err := references(f.obj)
					if err != nil {
						t.Fatal(err)
					}
					n := 0
					for _, r := range refs {
						if r.name == "missing" || r.name == "other_missing" {
							n++
						}
					}
					if n != 2 {
						t.Fatal("boundary normalization lost fixups", refs)
					}
				})
			}
		}
	}
}

func TestMachOPublicAliasResolution(t *testing.T) {
	for _, mode := range []string{"cycle", "offset_overflow", "address_overflow", "missing_storage", "tls_target", "native_tls_target", "indirect_chain"} {
		t.Run(mode, func(t *testing.T) {
			o := &object{info: Info{Format: "Mach-O"}, symbols: []symbol{
				{name: "a", global: true, section: -5, forward: &symbolForward{target: 1, offset: 8, storage: true}},
				{name: "b", global: true, section: -5, forward: &symbolForward{target: 2}},
				{name: "c", global: true, section: -1, value: 100},
			}}
			switch mode {
			case "cycle":
				o.symbols[1].forward = &symbolForward{target: 0}
			case "offset_overflow":
				o.symbols[1].forward = &symbolForward{target: 2, offset: ^uint64(0)}
			case "address_overflow":
				o.symbols[2].value = uint64(^uintptr(0)) - 4
			case "missing_storage":
				o.symbols[2].value = 0
			case "tls_target":
				o.symbols[2].tls = true
			case "native_tls_target":
				o.symbols[2].section, o.symbols[2].value = 1, 0
				o.sections = []*section{nil, {nativeTLS: 0x13, size: 24}}
			}
			defs, err := definitions([]*object{o})
			if err != nil {
				t.Fatal(err)
			}
			im := &image{defs: defs}
			address, err := im.lookup("a")
			if mode == "indirect_chain" {
				if err != nil || address != 108 {
					t.Fatal("N_INDR plus interior offset", address, err)
				}
			} else if err == nil {
				t.Fatal("invalid forwarding accepted", address)
			}
		})
	}
}

func TestNativeMachOPublicAliasRetry(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			providers := machoPublicProviders(t, target)
			for _, kind := range []string{"regular", "coalesced"} {
				for _, form := range []int{0, 3} {
					t.Run(kind+"/"+machoPublicForms[form], func(t *testing.T) {
						flags := machoPublicFlags(kind, form, opt)
						weak, _ := machoCoalescedFixture(t, target, "macho_public_aliases.S", append(flags, "-DDYLIB_PUBLIC_POISON")...)
						probe, _ := machoCoalescedFixture(t, target, "macho_public_aliases.c", flags...)
						owner := 1
						if form == 3 {
							owner = 0
						}
						s := New(Options{})
						defer s.Close()
						load(t, s, probe, providers[1-owner], machoCoalescedArchive(t, weak))
						if err := s.Link("public_check"); err == nil || !strings.Contains(err.Error(), "public_missing") {
							t.Fatal("retained alias storage lost its dependency", err)
						}
						if s.image != nil {
							t.Fatal("failed alias link published an image")
						}
						load(t, s, providers[owner])
						if err := s.Link("public_check"); err != nil {
							t.Fatal("discarded alias fixups or retry state survived", err)
						}
						for n := int32(0); n < 5; n++ {
							call(t, s, "public_check", 3, 42+n, 42+n)
						}
					})
				}
			}
		})
	}
}

func TestNativeMachOPublicAliasLease(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		for _, kind := range []string{"regular", "coalesced"} {
			t.Run(opt+"/"+kind, func(t *testing.T) {
				flags := machoPublicFlags(kind, 3, opt)
				weak, _ := machoCoalescedFixture(t, target, "macho_public_aliases.S", flags...)
				probe, _ := machoCoalescedFixture(t, target, "macho_public_aliases.c", flags...)
				providers := machoPublicProviders(t, target)
				owner := New(Options{})
				defer owner.Close()
				load(t, owner, weak, probe, providers[0])
				symbol, err := owner.Resolve("public_b")
				if err != nil {
					t.Fatal(err)
				}
				consumer := New(Options{})
				defer consumer.Close()
				if err := consumer.DefineSymbol("borrowed_b", symbol); err != nil {
					t.Fatal(err)
				}
				dir := t.TempDir()
				src := filepath.Join(dir, "consumer.c")
				if err := os.WriteFile(src, []byte("extern long borrowed_b[]; int read_alias(int a,int b){return borrowed_b[0]==43?a+b:-1;}"), 0600); err != nil {
					t.Fatal(err)
				}
				load(t, consumer, compile(t, src, src+".o", opt, "--target="+target))
				call(t, consumer, "read_alias", 20, 22, 42)
				closed := make(chan error, 1)
				go func() { closed <- owner.Close() }()
				waitSessionRetired(t, owner)
				call(t, consumer, "read_alias", 21, 22, 43)
				select {
				case <-closed:
					t.Fatal("public alias provider retired with a live consumer")
				default:
				}
				if err := consumer.Close(); err != nil {
					t.Fatal(err)
				}
				if err := <-closed; err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestNativeMachOPublicAliasReverseControl(t *testing.T) {
	target := needMachOCoalescedNative(t)
	sdk := machoCoalescedSDK(t)
	for _, opt := range []string{"-O0", "-O2"} {
		for _, kind := range []string{"regular", "coalesced"} {
			t.Run(opt+"/"+kind, func(t *testing.T) {
				flags := machoPublicFlags(kind, 0, opt)
				_, original := machoCoalescedFixture(t, target, "macho_public_aliases.S", flags...)
				weak := filepath.Join(t.TempDir(), "reversed.o")
				if err := os.WriteFile(weak, machoPublicReverse(t, original), 0600); err != nil {
					t.Fatal(err)
				}
				providers := machoPublicProviders(t, target)
				probe, _ := machoCoalescedFixture(t, target, "macho_public_aliases.c", flags...)
				s := New(Options{})
				defer s.Close()
				load(t, s, providers[1], machoCoalescedArchive(t, weak), probe)
				call(t, s, "public_check", 2, 42, 42)
				exe := filepath.Join(t.TempDir(), "control")
				args := []string{"--target=" + target, "-isysroot", sdk, "-Wl,-syslibroot," + sdk, "-Wl,-dead_strip", "testdata/macho_public_aliases.c", "-DDYLIB_PUBLIC_STANDALONE", "-DDYLIB_PUBLIC_OVERRIDE=2"}
				args = append(args, flags...)
				args = append(args, providers[1], weak, "-o", exe)
				command(t, compiler(), args...)
				command(t, exe)
			})
		}
	}
}

func TestNativeMachOPublicAliasLifecycle(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		for _, kind := range []string{"regular", "coalesced"} {
			for _, input := range []string{"objects", "archive"} {
				t.Run(opt+"/"+kind+"/"+input, func(t *testing.T) {
					flags := machoPublicFlags(kind, 0, opt)
					weak, _ := machoCoalescedFixture(t, target, "macho_public_aliases.S", flags...)
					probe, _ := machoCoalescedFixture(t, target, "macho_public_aliases.c", append(flags, "-DDYLIB_PUBLIC_LIFECYCLE")...)
					provider, _ := machoCoalescedFixture(t, target, "macho_public_lifecycle.c", opt)
					_, observer, read := lifecycleObserver(t, t.TempDir())
					event, err := observer.Resolve("record_event")
					if err != nil {
						t.Fatal(err)
					}
					s := New(Options{})
					defer s.Close()
					if err := s.DefineSymbol("record_event", event); err != nil {
						t.Fatal(err)
					}
					if input == "archive" {
						weak = machoCoalescedArchive(t, weak)
					}
					load(t, s, probe, weak, provider)
					if err := s.Link("public_check"); err != nil {
						t.Fatal(err)
					}
					expectEvents(t, read, 1, 2)
					call(t, s, "public_check", 2, 42, 42)
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					expectEvents(t, read, 1, 2, 3, 4)
				})
			}
		}
	}
}
