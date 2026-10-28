package cbridge_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cbridge"
	"github.com/ramblelabs/dylib-go/compiler/clang"
)

type conventionCase struct {
	prefix string
	want   abi.Convention
	ir     string
}

func conventionCases(target clang.Target) []conventionCase {
	cases := []conventionCase{{"cc_cdecl", abi.CDecl, ""}}
	if target.Arch == "amd64" {
		ms, sysv := conventionCase{"cc_ms", abi.Win64, "win64cc"}, conventionCase{"cc_sysv", abi.SysV64, "x86_64_sysvcc"}
		// Clang normalizes an explicit spelling of the target's default ABI.
		if target.OS == "windows" {
			ms.want, ms.ir = abi.CDecl, ""
		} else {
			sysv.want, sysv.ir = abi.CDecl, ""
		}
		cases = append(cases, ms, sysv)
	}
	if target.OS == "windows" && target.Arch == "386" {
		cases = append(cases, conventionCase{"cc_std", abi.StdCall, "x86_stdcallcc"}, conventionCase{"cc_fast", abi.FastCall, "x86_fastcallcc"}, conventionCase{"cc_this", abi.ThisCall, "x86_thiscallcc"})
	}
	if target.OS == "windows" && (target.Arch == "amd64" || target.Arch == "386") {
		cases = append(cases, conventionCase{"cc_vector", abi.VectorCall, "x86_vectorcallcc"})
	}
	return cases
}

var conventionSuffixes = []string{"scalar", "pointer", "pair", "large", "hfa", "spill", "factory", "call", "forward", "hfa_factory", "hfa_call", "control"}

func conventionModel(t *testing.T, compiler, target string, flags ...string) (*clang.Header, *cbridge.Facade) {
	t.Helper()
	header, err := filepath.Abs("testdata/conventions.h")
	if err != nil {
		t.Fatal(err)
	}
	options := clang.Options{Compiler: compiler, Target: target, Flags: flags, NominalRecords: true, Functions: []string{"cc_state"}}
	probe, err := clang.Parse(context.Background(), header, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, cc := range conventionCases(probe.Target) {
		for _, suffix := range conventionSuffixes {
			options.Functions = append(options.Functions, cc.prefix+"_"+suffix)
		}
	}
	h, err := clang.Parse(context.Background(), header, options)
	if err != nil {
		t.Fatal(err)
	}
	functions := make([]cbridge.Function, len(h.Functions))
	for i, declaration := range h.Functions {
		functions[i] = cbridge.Function{Name: declaration.Name, Symbol: "bridge_" + declaration.Name}
	}
	f, err := cbridge.Generate(filepath.ToSlash(header), h, functions)
	if err != nil {
		t.Fatal(err)
	}
	return h, f
}

func checkConventionMetadata(t *testing.T, h *clang.Header, f *cbridge.Facade) {
	t.Helper()
	bindings := map[string]cbridge.Binding{}
	for _, b := range f.Functions {
		bindings[b.Name] = b
	}
	for _, cc := range conventionCases(h.Target) {
		for _, suffix := range conventionSuffixes {
			b := bindings[cc.prefix+"_"+suffix]
			want := cc.want
			switch suffix {
			case "factory", "hfa_factory", "call", "hfa_call", "control":
				want = abi.CDecl
			}
			if b.Signature().Convention != want {
				t.Fatalf("%s convention %v; want %v", b.Name, b.Signature().Convention, want)
			}
			position := -2
			switch suffix {
			case "factory", "hfa_factory":
				position = -1
			case "call", "hfa_call":
				position = 0
			case "forward":
				position = 1
			}
			if position != -2 {
				p, err := b.LookupFunctionPointer(position)
				if err != nil || p.Signature.Convention != cc.want {
					t.Fatalf("%s entry convention: %+v %v; want %v", b.Name, p, err, cc.want)
				}
			}
		}
	}
}

func TestHeaderBridgeCallingConventionTargets(t *testing.T) {
	compiler := clangTool(t)
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		t.Run(target, func(t *testing.T) {
			for _, packing := range []string{"default", "packed"} {
				t.Run(packing, func(t *testing.T) {
					flags := []string{"-ffreestanding"}
					if packing == "packed" {
						flags = append(flags, "-fpack-struct=1")
					}
					h, f := conventionModel(t, compiler, target, flags...)
					checkConventionMetadata(t, h, f)
					dir := t.TempDir()
					source, header := filepath.Join(dir, "facade.c"), filepath.Join(dir, "facade.h")
					if err := os.WriteFile(source, f.CSource, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(header, f.CHeader, 0600); err != nil {
						t.Fatal(err)
					}
					args := append([]string{"--target=" + target, "-std=c17", "-Wall", "-Wextra", "-Werror", "-O0", "-S", "-emit-llvm"}, flags...)
					out, err := exec.Command(compiler, append(args, source, "-o", "-")...).CombinedOutput()
					if err != nil {
						t.Fatalf("original calling convention facade: %v\n%s", err, out)
					}
					for _, cc := range conventionCases(h.Target) {
						if cc.ir != "" && !bytes.Contains(out, []byte("call "+cc.ir)) {
							t.Fatalf("missing original native call %s", cc.ir)
						}
					}
					exports := make([]string, len(f.Functions))
					for i, binding := range f.Functions {
						exports[i] = binding.Symbol
					}
					consumer, err := clang.Parse(context.Background(), header, clang.Options{Compiler: compiler, Target: target, Flags: []string{"-ffreestanding"}, Functions: exports})
					if err != nil {
						t.Fatal(err)
					}
					if len(consumer.Records) != 0 {
						t.Fatal("native records crossed the carrier interface", consumer.Records)
					}
					for _, d := range consumer.Functions {
						if d.Signature.Convention != abi.CDecl || d.Signature.Result != abi.I32 || len(d.Signature.Args) != 3 {
							t.Fatal("native convention leaked into carrier interface", d)
						}
						for _, argument := range d.Signature.Args {
							if argument != abi.Pointer {
								t.Fatal("native value crossed the carrier interface", d)
							}
						}
					}
				})
			}
		})
	}
}
