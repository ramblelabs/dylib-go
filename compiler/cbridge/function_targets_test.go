package cbridge_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cbridge"
	"github.com/ramblelabs/dylib-go/compiler/clang"
)

var entryNames = []string{"entry_factory", "entry_slot", "entry_higher", "entry_from_factory", "entry_apply", "entry_indirect", "entry_tail_factory", "entry_tail_apply", "entry_scalar_apply", "entry_native_control"}

func entryModel(t *testing.T, compiler, target string, flags ...string) (*clang.Header, *cbridge.Facade) {
	t.Helper()
	header, err := filepath.Abs("testdata/entries.h")
	if err != nil {
		t.Fatal(err)
	}
	h, err := clang.Parse(context.Background(), header, clang.Options{Compiler: compiler, Target: target, Flags: flags, Functions: entryNames, NominalRecords: true})
	if err != nil {
		t.Fatal(err)
	}
	functions := make([]cbridge.Function, len(entryNames))
	for i, name := range entryNames {
		functions[i] = cbridge.Function{Name: name, Symbol: "bridge_" + name}
	}
	f, err := cbridge.Generate(filepath.ToSlash(header), h, functions)
	if err != nil {
		t.Fatal(err)
	}
	return h, f
}

func TestHeaderBridgeFunctionPointerTargets(t *testing.T) {
	compiler := clangTool(t)
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		t.Run(target, func(t *testing.T) {
			for _, packing := range []string{"default", "packed"} {
				t.Run(packing, func(t *testing.T) {
					flags := []string{"-ffreestanding"}
					if packing == "packed" {
						flags = append(flags, "-fpack-struct=1")
					}
					_, f := entryModel(t, compiler, target, flags...)
					for _, b := range f.Functions {
						if b.Name == "entry_higher" {
							p, err := b.LookupFunctionPointer(-1)
							if err != nil {
								t.Fatal(err)
							}
							if _, err := p.LookupFunctionPointer(-1); err != nil {
								t.Fatal("missing factory result prototype", err)
							}
						}
					}
					source := filepath.Join(t.TempDir(), "facade.c")
					if err := os.WriteFile(source, f.CSource, 0600); err != nil {
						t.Fatal(err)
					}
					args := append([]string{"--target=" + target, "-std=c17", "-Wall", "-Wextra", "-Werror", "-fsyntax-only"}, flags...)
					if output, err := exec.Command(compiler, append(args, source)...).CombinedOutput(); err != nil {
						t.Fatalf("native function-pointer facade: %v\n%s", err, output)
					}
				})
			}
		})
	}
}
