package cxx_test

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

var freeTargets = []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"}

func TestNativeDiscoverFunctionsAcrossTargets(t *testing.T) {
	header, err := filepath.Abs("testdata/free_functions.hpp")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"included", "add", "add", "forward", "forward", "defaults", "internal", "deleted", "child::add", "v1::versioned", "c_entry", "sum", "factory", "invoke", "borrowed", "read", "assign", "reopened"}
	for _, target := range freeTargets {
		t.Run(target, func(t *testing.T) {
			for _, scope := range []string{"free_catalog", "free_alias", "free_catalog::child", "free_empty", "free_imports"} {
				t.Run(scope, func(t *testing.T) {
					declarations, err := cxx.DiscoverFunctions(context.Background(), header, scope, cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding"}})
					if err != nil {
						t.Fatal(err)
					}
					var got, want []string
					selected := names
					if scope == "free_catalog::child" {
						selected = []string{"add"}
					}
					if scope == "free_empty" || scope == "free_imports" {
						selected = nil
					}
					for _, n := range selected {
						want = append(want, scope+"::"+n)
					}
					for _, f := range declarations {
						got = append(got, f.Name)
						if f.NativeType == "" {
							t.Fatal("missing native prototype", f)
						}
						if strings.HasSuffix(f.Name, "::defaults") && (len(f.Parameters) != 1 || !f.Parameters[0].HasDefault || f.Parameters[0].Name != "value") {
							t.Fatal("default parameter", f)
						}
						if strings.HasSuffix(f.Name, "::internal") && !f.Internal || strings.HasSuffix(f.Name, "::deleted") && !f.Deleted || strings.HasSuffix(f.Name, "::sum") && !f.Variadic {
							t.Fatal("missing function flags", f)
						}
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("declarations:\ngot %v\nwant %v", got, want)
					}
				})
			}
		})
	}
}

func TestDiscoverFunctionsBounds(t *testing.T) {
	header, err := filepath.Abs("testdata/free_functions.hpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"", "::free_catalog", "free_catalog;", "free_catalog::", strings.Repeat("x", 257), "missing", "free_catalog::Token"} {
		t.Run(fmt.Sprintf("namespace_%q", scope), func(t *testing.T) {
			if v, err := cxx.DiscoverFunctions(context.Background(), header, scope, cxx.DiscoveryOptions{Compiler: cppTool(t)}); err == nil || v != nil {
				t.Fatal(v, err)
			}
		})
	}
	t.Run("nil_context", func(t *testing.T) {
		if v, err := cxx.DiscoverFunctions(nil, header, "free_catalog", cxx.DiscoveryOptions{}); err == nil || v != nil {
			t.Fatal(v, err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if v, err := cxx.DiscoverFunctions(ctx, header, "free_catalog", cxx.DiscoveryOptions{}); err != context.Canceled || v != nil {
			t.Fatal(v, err)
		}
	})
	for _, count := range []int{256, 257} {
		t.Run(fmt.Sprintf("declarations_%d", count), func(t *testing.T) {
			var s strings.Builder
			s.WriteString("namespace bounded {\n")
			for i := 0; i < count; i++ {
				fmt.Fprintf(&s, "int f%d(int);\n", i)
			}
			s.WriteString("}\n")
			path := filepath.Join(t.TempDir(), "bounded.hpp")
			write(t, path, []byte(s.String()))
			v, err := cxx.DiscoverFunctions(context.Background(), path, "bounded", cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if count == 256 {
				if err != nil || len(v) != 256 {
					t.Fatal(len(v), err)
				}
			} else if err == nil || v != nil {
				t.Fatal("partial result", v, err)
			}
		})
	}
	t.Run("preprocessing", func(t *testing.T) {
		v, err := cxx.DiscoverFunctions(context.Background(), header, "free_catalog", cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: []string{"-DFREE_CATALOG_EXTRA"}})
		if err != nil || len(v) != 19 || v[17].Name != "free_catalog::conditional" {
			t.Fatal(v, err)
		}
	})
}

func TestFreeFunctionFacadeAcrossTargets(t *testing.T) {
	include, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := cxx.GenerateFunctions("free_functions.hpp", cxxexample.FreeFunctions())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(facade.CHeader), "...") {
		t.Fatal("facade must be fixed")
	}
	source := filepath.Join(t.TempDir(), "free.cpp")
	write(t, source, facade.CXXSource)
	for _, target := range freeTargets {
		t.Run(target, func(t *testing.T) {
			output, err := exec.Command(cppTool(t), "--target="+target, "-ffreestanding", "-std=c++17", "-fsyntax-only", "-I"+include, source).CombinedOutput()
			if err != nil {
				t.Fatalf("facade: %v\n%s", err, output)
			}
		})
	}
}

func TestFreeFunctionManifestValidation(t *testing.T) {
	for name, patch := range map[string]func([]cxx.Function) []cxx.Function{
		"empty":            func(f []cxx.Function) []cxx.Function { return nil },
		"invalid_name":     func(f []cxx.Function) []cxx.Function { f[0].Name = "x;"; return f },
		"leading_scope":    func(f []cxx.Function) []cxx.Function { f[0].Name = "::free_global"; return f },
		"template":         func(f []cxx.Function) []cxx.Function { f[0].Name = "f<int>"; return f },
		"duplicate_export": func(f []cxx.Function) []cxx.Function { f[1].Symbol = f[0].Symbol; return f },
		"stdcall":          func(f []cxx.Function) []cxx.Function { f[0].Signature.Convention = abi.StdCall; return f },
		"record":           func(f []cxx.Function) []cxx.Function { f[0].Signature.Args[0] = abi.Struct; return f },
		"zero_fixed":       func(f []cxx.Function) []cxx.Function { f[5].Signature.FixedArgs = 0; return f },
		"invalid_object": func(f []cxx.Function) []cxx.Function {
			f[0].ObjectArgs = []cxx.ObjectArg{{Index: 0, Type: cxx.ObjectType{Name: "Token", Kind: cxx.ObjectReference}}}
			return f
		},
		"excessive": func(f []cxx.Function) []cxx.Function { return make([]cxx.Function, 257) },
	} {
		t.Run(name, func(t *testing.T) {
			if v, err := cxx.GenerateFunctions("free_functions.hpp", patch(cxxexample.FreeFunctions())); err == nil || v != nil {
				t.Fatal(v, err)
			}
		})
	}
	t.Run("invalid_header", func(t *testing.T) {
		if v, err := cxx.GenerateFunctions("bad\n", cxxexample.FreeFunctions()); err == nil || v != nil {
			t.Fatal(v, err)
		}
	})
	t.Run("class_global_rejected", func(t *testing.T) {
		if v, err := cxx.Generate("free_functions.hpp", []cxx.Class{{Methods: []cxx.Method{{Name: "f", Symbol: "f", Static: true}}}}); err == nil || v != nil {
			t.Fatal(v, err)
		}
	})
}

func TestNativeCompilerRejectsFreeFunctionMismatch(t *testing.T) {
	include, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for name, patch := range map[string]func(*cxx.Function){
		"result":  func(f *cxx.Function) { f.Signature.Result = abi.Bool },
		"missing": func(f *cxx.Function) { f.Name = "free_catalog::missing" },
		"deleted": func(f *cxx.Function) {
			f.Name = "free_catalog::deleted"
			f.Signature.Args = []abi.Type{abi.I32}
			f.Signature.Result = abi.Void
		},
		"data_member": func(f *cxx.Function) { f.Name = "free_catalog::Token::value" },
	} {
		t.Run(name, func(t *testing.T) {
			f := cxxexample.FreeFunctions()[0]
			patch(&f)
			facade, err := cxx.GenerateFunctions("free_functions.hpp", []cxx.Function{f})
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "bad.cpp")
			write(t, source, facade.CXXSource)
			if _, err := exec.Command(cppTool(t), "-std=c++17", "-fsyntax-only", "-I"+include, source).CombinedOutput(); err == nil {
				t.Fatal("native type mismatch accepted")
			}
		})
	}
}

func TestFreeFunctionFacadeLimit(t *testing.T) {
	functions := make([]cxx.Function, 256)
	for i := range functions {
		functions[i] = cxx.Function{Name: "global", Symbol: fmt.Sprintf("f%d", i), Signature: abi.Signature{Result: abi.I32}}
	}
	facade, err := cxx.GenerateFunctions("functions.hpp", functions)
	if err != nil || len(facade.Functions) != 256 {
		t.Fatal(facade, err)
	}
}
