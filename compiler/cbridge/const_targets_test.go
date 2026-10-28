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

func TestHeaderBridgeConstTargets(t *testing.T) {
	compiler := clangTool(t)
	header, err := filepath.Abs("testdata/exports.h")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		t.Run(target, func(t *testing.T) {
			for _, packing := range []string{"default", "packed"} {
				t.Run(packing, func(t *testing.T) {
					flags := []string{"-ffreestanding"}
					if packing == "packed" {
						flags = append(flags, "-fpack-struct=1")
					}
					names := []string{"const_pair", "const_packet", "const_stack", "const_blocks"}
					h, err := clang.Parse(context.Background(), header, clang.Options{Compiler: compiler, Target: target, Functions: names, NominalRecords: true, Flags: flags})
					if err != nil {
						t.Fatal(err)
					}
					functions := []cbridge.Function{}
					for _, name := range names {
						functions = append(functions, cbridge.Function{Name: name, Symbol: "bridge_" + name})
					}
					f, err := cbridge.Generate(filepath.ToSlash(header), h, functions)
					if err != nil {
						t.Fatal(err)
					}
					source := filepath.Join(t.TempDir(), "facade.c")
					if err := os.WriteFile(source, f.CSource, 0600); err != nil {
						t.Fatal(err)
					}
					args := append([]string{"--target=" + target, "-std=c17", "-Wall", "-Wextra", "-Werror", "-fsyntax-only"}, flags...)
					if output, err := exec.Command(compiler, append(args, source)...).CombinedOutput(); err != nil {
						t.Fatalf("native const initializer: %v\n%s", err, output)
					}
				})
			}
		})
	}
}
