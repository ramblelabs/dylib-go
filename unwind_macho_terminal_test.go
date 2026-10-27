package dylib

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	examplecall "github.com/ramblelabs/dylib-go/examples/call"
)

func machoTerminalFrames(t *testing.T, target, kind, opt, mode string, extra ...string) (string, *object) {
	t.Helper()
	assembly := filepath.Join(t.TempDir(), "frame.s")
	flags := []string{"--target=" + target, opt, "-S", "-fexceptions", "-fno-stack-protector", "-DDYLIB_TERM_KIND=" + kind}
	if mode == "compact" {
		flags = append(flags, "-DDYLIB_CXX_COMPACT")
	} else {
		flags = append(flags, "-femit-dwarf-unwind=always")
	}
	flags = append(flags, extra...)
	command(t, "clang++", append(flags, "testdata/macho_terminal_frames.cpp", "-o", assembly)...)
	b, err := os.ReadFile(assembly)
	if err != nil {
		t.Fatal(err)
	}
	// Append after Clang has emitted the whole function and its unwind data.
	// The marker is a genuine public, non-alternate, zero-byte definition.
	b = append(b, []byte("\n.section __TEXT,__term_frame,"+kind+",pure_instructions\n.globl _term_frame_marker\n.weak_definition _term_frame_marker\n_term_frame_marker:\n.alt_entry lterm_frame_end\nlterm_frame_end:\n.data\n.p2align 3\n.globl _term_frame_pointer\n_term_frame_pointer:\n.quad lterm_frame_end\n.subsections_via_symbols\n")...)
	if err := os.WriteFile(assembly, b, 0600); err != nil {
		t.Fatal(err)
	}
	path := compile(t, assembly, assembly+".o", "--target="+target)
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	o := parseMachoAliases(t, b)
	found := false
	for _, a := range o.machoAtoms {
		if o.symbols[a.key].name == "term_frame_marker" {
			found = a.start == a.end && a.start == o.sections[a.section].size && o.symbols[a.key].global && o.symbols[a.key].weak
		}
	}
	if !found {
		t.Fatal("C++ fixture has no public terminal storage owner")
	}
	return path, o
}

func TestMachOTerminalFrames(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		for _, kind := range []string{"regular", "coalesced"} {
			for _, opt := range []string{"-O0", "-O2"} {
				for _, mode := range []string{"dwarf", "compact"} {
					t.Run(target+"/"+kind+"/"+opt+"/"+mode, func(t *testing.T) {
						_, weak := machoTerminalFrames(t, target, kind, opt, mode)
						before := append([]relocation(nil), weak.relocs...)
						for _, replace := range []bool{false, true} {
							for _, order := range []string{"weak_first", "strong_first"} {
								t.Run(fmt.Sprintf("replace_%t/%s", replace, order), func(t *testing.T) {
									inputs := []*object{weak}
									if replace {
										provider := &object{info: weak.info, symbols: []symbol{{name: "term_frame_marker", section: -1, value: 0x123400, global: true}}}
										if order == "strong_first" {
											inputs = append([]*object{provider}, inputs...)
										} else {
											inputs = append(inputs, provider)
										}
									}
									selected, err := coalesceObjects(inputs)
									if err != nil {
										t.Fatal(err)
									}
									index := 0
									if replace && order == "strong_first" {
										index = 1
									}
									for i, s := range weak.sections {
										if s != nil && (s.name == "__eh_frame" || s.name == "__compact_unwind" || s.name == "__gcc_except_tab") {
											got := selected[index].sections[i]
											if got == nil || !bytes.Equal(got.data, s.data) {
												t.Fatal("zero-byte replacement changed retained unwind", s.name)
											}
										}
									}
									if !reflect.DeepEqual(before, weak.relocs) || !reflect.DeepEqual(before, selected[index].relocs) {
										t.Fatal("zero-byte selection changed unwind/code fixups")
									}
								})
							}
						}
					})
				}
			}
		}
	}
}

func TestNativeMachOTerminalFrames(t *testing.T) {
	target, sdk := needMachOCoalescedNative(t), machoCoalescedSDK(t)
	for _, kind := range []string{"regular", "coalesced"} {
		for _, opt := range []string{"-O0", "-O2"} {
			for _, mode := range []string{"dwarf", "compact"} {
				t.Run(kind+"/"+opt+"/"+mode, func(t *testing.T) {
					weak, _ := machoTerminalFrames(t, target, kind, opt, mode)
					body := "leal (%rdi,%rsi),%eax\nretq\n"
					if strings.HasPrefix(target, "arm64") {
						body = "add w0,w0,w1\nret\n"
					}
					provider := machoCoalescedAssembly(t, target, ".text\n.globl _term_frame_marker\n_term_frame_marker:\n"+body)
					host := filepath.Join(t.TempDir(), "host.dylib")
					flags := []string{"--target=" + target, "-isysroot", sdk, "-Wl,-syslibroot," + sdk, opt, "-dynamiclib", "-fexceptions", "testdata/unwind_cxx_host.cpp", "-o", host}
					if mode == "compact" {
						flags = append(flags, "-DDYLIB_CXX_COMPACT")
					}
					command(t, "clang++", flags...)
					observer := New(Options{})
					defer observer.Close()
					load(t, observer, host)
					for replace := 0; replace < 2; replace++ {
						for _, order := range []string{"weak_first", "strong_first"} {
							t.Run(fmt.Sprintf("replace%d/%s", replace, order), func(t *testing.T) {
								for _, input := range []string{"objects", "archive"} {
									t.Run(input, func(t *testing.T) {
										path := weak
										if input == "archive" {
											path = machoCoalescedArchive(t, weak)
										}
										inputs := []string{host, path}
										if replace != 0 {
											if order == "strong_first" {
												inputs = append([]string{provider}, inputs...)
											} else {
												inputs = append(inputs, provider)
											}
										}
										s := New(Options{RegisterUnwind: true})
										defer s.Close()
										load(t, s, inputs...)
										call(t, observer, "cxx_events", 1, 0, 0)
										call(t, s, "term_frame_check", int32(replace), 42, 42)
										call(t, observer, "cxx_events", 1, 0, 2)
										call(t, observer, "cxx_has_frame", 0, 0, 1)
										entry, err := s.Resolve("term_frame_root")
										if err != nil {
											t.Fatal(err)
										}
										lease, err := entry.Acquire()
										if err != nil {
											t.Fatal(err)
										}
										defer lease.Close()
										closed := make(chan error, 1)
										go func() { closed <- s.Close() }()
										waitSessionRetired(t, s)
										if err := lease.WithAddress(func(p uintptr) error {
											r, err := examplecall.Int32(p, -20, 62)
											if r != 42 {
												t.Errorf("retired terminal exception call: %d", r)
											}
											return err
										}); err != nil {
											t.Fatal(err)
										}
										lease.Close()
										if err := <-closed; err != nil {
											t.Fatal(err)
										}
										call(t, observer, "cxx_has_frame", 0, 0, 0)
										call(t, observer, "cxx_events", 1, 0, 1)
									})
								}
								t.Run("system_control", func(t *testing.T) {
									control, _ := machoTerminalFrames(t, target, kind, opt, mode, "-DDYLIB_TERM_STANDALONE", fmt.Sprintf("-DDYLIB_TERM_MODE=%d", replace))
									inputs := []string{control}
									if replace != 0 {
										if order == "strong_first" {
											inputs = append([]string{provider}, inputs...)
										} else {
											inputs = append(inputs, provider)
										}
									}
									exe := filepath.Join(t.TempDir(), "control")
									args := []string{"--target=" + target, "-isysroot", sdk, "-Wl,-syslibroot," + sdk, "-Wl,-dead_strip"}
									command(t, "clang++", append(append(args, inputs...), host, "-o", exe)...)
									command(t, exe)
								})
							})
						}
					}
				})
			}
		}
	}
}
