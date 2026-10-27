package dylib

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeMachOTerminalLifecycle(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		for _, kind := range []string{"regular", "coalesced"} {
			for form, name := range machoTerminalForms[:2] {
				t.Run(opt+"/"+kind+"/"+name, func(t *testing.T) {
					mode := 3
					var extra []string
					if form == 1 {
						mode, extra = 5, []string{"-DDYLIB_TERM_LIFE_PAIR"}
					}
					flags := machoTerminalFlags(kind, form, mode, opt)
					weak, _ := machoCoalescedFixture(t, target, "macho_terminal.S", flags...)
					probe, _ := machoCoalescedFixture(t, target, "macho_terminal.c", append(flags, "-DDYLIB_TERM_LIFECYCLE")...)
					provider, _ := machoCoalescedFixture(t, target, "macho_terminal_lifecycle.c", append([]string{opt}, extra...)...)
					owner := machoTerminalProviders(t, target, false)[0]
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
							load(t, s, probe, path, owner, provider)
							if err := s.Link("term_check"); err != nil {
								t.Fatal(err)
							}
							expectEvents(t, read, 1, 2)
							call(t, s, "term_check", int32(mode), 42, 42)
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

func TestNativeMachOTerminalLease(t *testing.T) {
	target := needMachOCoalescedNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		for _, kind := range []string{"regular", "coalesced"} {
			t.Run(opt+"/"+kind, func(t *testing.T) {
				weak, _ := machoCoalescedFixture(t, target, "macho_terminal.S", machoTerminalFlags(kind, 0, 0, opt)...)
				provider := New(Options{})
				defer provider.Close()
				load(t, provider, machoCoalescedArchive(t, weak), machoTerminalProviders(t, target, false)[0])
				end, err := provider.Resolve("term_marker")
				if err != nil {
					t.Fatal(err)
				}
				consumer := New(Options{})
				defer consumer.Close()
				if err := consumer.DefineSymbol("borrowed_end", end); err != nil {
					t.Fatal(err)
				}
				src := filepath.Join(t.TempDir(), "consumer.c")
				if err := os.WriteFile(src, []byte("#include <stdint.h>\nextern char borrowed_end[]; int consume_end(int a,int b){uintptr_t volatile p=(uintptr_t)borrowed_end; return p?a+b:-1;}\n"), 0600); err != nil {
					t.Fatal(err)
				}
				load(t, consumer, compile(t, src, src+".o", "--target="+target, opt))
				call(t, consumer, "consume_end", 20, 22, 42)
				closed := make(chan error, 1)
				go func() { closed <- provider.Close() }()
				waitSessionRetired(t, provider)
				call(t, consumer, "consume_end", 21, 22, 43)
				select {
				case <-closed:
					t.Fatal("zero-byte symbol lost its storage owner during retirement")
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
