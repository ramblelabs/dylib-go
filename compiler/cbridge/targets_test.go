package cbridge_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cbridge"
	"github.com/ramblelabs/dylib-go/compiler/clang"
)

func clangTool(t *testing.T) string {
	t.Helper()
	compiler := os.Getenv("CLANG")
	if compiler == "" {
		compiler = "clang"
	}
	path, err := exec.LookPath(compiler)
	if err != nil {
		if os.Getenv("DYLIB_TEST_REQUIRE_TOOLS") == "1" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	return path
}

func TestHeaderBridgeAcrossTargets(t *testing.T) {
	compiler := clangTool(t)
	header, err := filepath.Abs("testdata/exports.h")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		t.Run(target, func(t *testing.T) {
			names := []string{"sum", "float_gap", "empty_lane", "float_tail", "packed_bits", "large", "envelope", "exhausted"}
			h, err := clang.Parse(context.Background(), header, clang.Options{Compiler: compiler, Target: target, Functions: names, NominalRecords: true, Flags: []string{"-ffreestanding"}})
			if err != nil {
				t.Fatal(err)
			}
			functions := []cbridge.Function{}
			exports := []string{}
			for _, name := range names {
				functions = append(functions, cbridge.Function{Name: name, Symbol: "bridge_" + name})
				exports = append(exports, "bridge_"+name)
			}
			f, err := cbridge.Generate(filepath.ToSlash(header), h, functions)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			source, path := filepath.Join(dir, "facade.c"), filepath.Join(dir, "facade.h")
			if err := os.WriteFile(source, f.CSource, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, f.CHeader, 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command(compiler, "--target="+target, "-std=c17", "-ffreestanding", "-fsyntax-only", source).CombinedOutput(); err != nil {
				t.Fatalf("native facade: %v\n%s", err, output)
			}
			// Consumers parse only the standalone primitive pointer interface, with no
			// dependency on or reconstruction of the original record declarations.
			consumer, err := clang.Parse(context.Background(), path, clang.Options{Compiler: compiler, Target: target, Functions: exports, Flags: []string{"-ffreestanding"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(consumer.Records) != 0 {
				t.Fatal("attributed records crossed the public facade", consumer.Records)
			}
			if _, err := consumer.CgoSource("bindings", "Bindings"); err != nil {
				t.Fatal(err)
			}
			if h.Target.Arch != "386" {
				if _, err := consumer.LLGoSource("bindings", "Direct"); err != nil {
					t.Fatal(err)
				}
			}
			if h.Target.Arch == "amd64" && h.Target.OS != "windows" {
				if _, err := h.CgoSource("bindings", "Old"); err == nil || !strings.Contains(err.Error(), "implicit-padding") {
					t.Fatal("fixture must retain an existing rejected native layout", err)
				}
			}
		})
	}
}

func TestHeaderBridgeNativeCompilerRejectsConstInput(t *testing.T) {
	compiler := clangTool(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "const.h")
	if err := os.WriteFile(path, []byte("typedef struct { const int value; } Constant; Constant read_only(Constant);\n"), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := clang.Parse(context.Background(), path, clang.Options{Compiler: compiler, Functions: []string{"read_only"}, NominalRecords: true})
	if err != nil {
		t.Fatal(err)
	}
	f, err := cbridge.Generate(filepath.ToSlash(path), h, []cbridge.Function{{Name: "read_only", Symbol: "bridge_read_only"}})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "facade.c")
	if err := os.WriteFile(source, f.CSource, 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(compiler, "-std=c17", "-fsyntax-only", source).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "const-qualified") {
		t.Fatalf("const input was not rejected: %v\n%s", err, output)
	}
}
