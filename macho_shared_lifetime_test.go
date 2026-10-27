package dylib

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeMachOSharedPublicLease(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		for _, kind := range []string{"regular", "coalesced"} {
			for _, form := range []int{0, 1} {
				t.Run(opt+"/"+kind+"/"+machoSharedForms[form], func(t *testing.T) {
					flags := machoSharedFlags(kind, form, opt)
					weak, _ := machoCoalescedFixture(t, target, "macho_shared_aliases.S", flags...)
					providers := machoSharedProviders(t, target)
					owner := New(Options{})
					defer owner.Close()
					inputs := []string{weak, providers[0]}
					if form%2 != 0 {
						inputs = append(inputs, providers[2])
					}
					load(t, owner, inputs...)
					symbol, err := owner.Resolve("shared_zalias")
					if err != nil {
						t.Fatal(err)
					}
					consumer := New(Options{})
					defer consumer.Close()
					if err := consumer.DefineSymbol("borrowed", symbol); err != nil {
						t.Fatal(err)
					}
					src := filepath.Join(t.TempDir(), "consumer.c")
					if err := os.WriteFile(src, []byte("extern long borrowed[]; int read_alias(int a,int b){return borrowed[0]==42?a+b:-1;}"), 0600); err != nil {
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
						t.Fatal("shared alias provider retired with a live consumer")
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
}

func TestNativeMachOSharedPublicLifecycle(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		for _, kind := range []string{"regular", "coalesced"} {
			for _, form := range []int{0, 1, 4, 5} {
				t.Run(opt+"/"+kind+"/"+machoSharedForms[form], func(t *testing.T) {
					flags := machoSharedFlags(kind, form, opt)
					weak, _ := machoCoalescedFixture(t, target, "macho_shared_aliases.S", flags...)
					probe, _ := machoCoalescedFixture(t, target, "macho_shared_aliases.c", append(flags, "-DDYLIB_SHARED_LIFECYCLE")...)
					provider, _ := machoCoalescedFixture(t, target, "macho_shared_lifecycle.c", opt)
					var before string
					if form%2 != 0 {
						before = machoCoalescedAssembly(t, target, ".data\n.globl _shared_before\n_shared_before:\n.quad 99,100,101\n")
					}
					for _, input := range []string{"objects", "archive"} {
						t.Run(input, func(t *testing.T) {
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
							path := weak
							if input == "archive" {
								path = machoCoalescedArchive(t, weak)
							}
							inputs := []string{probe, path, provider}
							if before != "" {
								inputs = append(inputs, before)
							}
							load(t, s, inputs...)
							if err := s.Link("shared_check"); err != nil {
								t.Fatal(err)
							}
							expectEvents(t, read, 1, 2)
							call(t, s, "shared_check", 1, 42, 42)
							if err := s.Close(); err != nil {
								t.Fatal(err)
							}
							expectEvents(t, read, 1, 2, 3, 4)
						})
					}
				})
			}
		}
	}
}
