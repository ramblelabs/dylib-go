package cxx_test

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func TestNativeInferredMethodsAcrossTargets(t *testing.T) {
	header, err := filepath.Abs("testdata/inferred_methods.hpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range freeTargets {
		t.Run(target, func(t *testing.T) {
			result, err := cxxexample.InferSelectedMethods(context.Background(), filepath.ToSlash(header), cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding", "-fsigned-char"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Methods) != 43 || len(result.Facade.Functions) != 47 {
				t.Fatal(len(result.Methods), len(result.Facade.Functions))
			}
			for i, m := range result.Methods {
				if err := m.Transport.Validate(); err != nil {
					t.Fatal(err)
				}
				if i < 24 {
					q := i % 12
					if m.Const != (q&1 != 0) || m.Volatile != (q&2 != 0) || m.Ref != cxx.RefQualifier(q/4) || m.Noexcept != (i >= 12) || m.Transport.Result != abi.I32 || len(m.Transport.Args) != 1 || m.Transport.Args[0] != abi.I32 {
						t.Fatal(i, m)
					}
				}
				if m.Selection.Name == "native_long" {
					want := abi.I64
					if strings.Contains(target, "windows") || strings.HasPrefix(target, "i686-") {
						want = abi.I32
					}
					if m.Transport.Result != want || m.Transport.Args[0] != want {
						t.Fatal(m, want)
					}
				}
				if m.Selection.Name == "self" || m.Selection.Name == "entry" {
					if m.Transport.Result != abi.Pointer {
						t.Fatal(m)
					}
				}
				if m.Selection.Name == "mark" && m.Transport.Result != abi.Void {
					t.Fatal(m)
				}
				if m.Selection.Name == "code" && m.Transport.Result != abi.U16 {
					t.Fatal(m)
				}
			}
		})
	}
}

func TestInferredMethodInputs(t *testing.T) {
	for _, mode := range []string{"empty", "too_many", "class", "class_long", "name", "name_long", "conversion", "symbol", "symbol_long", "duplicate", "template", "nil_context", "cancelled", "missing_compiler"} {
		t.Run(mode, func(t *testing.T) {
			input := []cxx.MethodSelection{{Class: "inferred_members::Receiver", Name: "read", Symbol: "read"}}
			ctx := context.Background()
			switch mode {
			case "empty":
				input = nil
			case "too_many":
				input = make([]cxx.MethodSelection, 257)
			case "class":
				input[0].Class = "R;evil"
			case "class_long":
				input[0].Class = strings.Repeat("R", 257)
			case "name":
				input[0].Name = "wrong;"
			case "name_long":
				input[0].Name = strings.Repeat("n", 257)
			case "conversion":
				input[0].Name = "operator int"
			case "symbol":
				input[0].Symbol = "int"
			case "symbol_long":
				input[0].Symbol = strings.Repeat("s", 257)
			case "duplicate":
				input = append(input, input[0])
			case "template":
				input[0].TemplateArgs = []string{"sizeof(int)"}
			case "nil_context":
				ctx = nil
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			out, err := cxx.InferMethods(ctx, "testdata/inferred_methods.hpp", input, cxx.DiscoveryOptions{Compiler: filepath.Join(t.TempDir(), "missing")})
			if err == nil || out != nil {
				t.Fatal(out, err)
			}
		})
	}
}

func TestNativeInferredMethodRejections(t *testing.T) {
	for _, name := range []string{"static_only", "overloaded", "value", "reference", "referred", "extended", "variadic", "deleted", "member_pointer", "state", "hidden", "protected_method", "missing", "Receiver"} {
		t.Run(name, func(t *testing.T) {
			selected := []cxx.MethodSelection{{Class: "inferred_members::Receiver", Name: "read", Symbol: "good"}, {Class: "inferred_members::Receiver", Name: name, Symbol: "bad"}}
			out, err := cxx.InferMethods(context.Background(), "testdata/inferred_methods.hpp", selected, cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if err == nil || out != nil {
				t.Fatal("unsupported method returned partial output", out, err)
			}
		})
	}
}

func TestNativeInferredMethodProducerGuards(t *testing.T) {
	for _, mode := range []string{"const", "noexcept"} {
		t.Run(mode, func(t *testing.T) {
			name, flag := "changing", "-DINFERRED_MEMBER_CONST"
			if mode == "noexcept" {
				name, flag = "changing_exception", "-DINFERRED_MEMBER_NOEXCEPT"
			}
			opts := cxx.DiscoveryOptions{Compiler: cppTool(t), Target: "x86_64-unknown-linux-gnu", Flags: []string{"-ffreestanding"}}
			selected := []cxx.MethodSelection{{Class: "inferred_members::Receiver", Name: name, Symbol: "guarded"}}
			out, err := cxx.InferMethods(context.Background(), "testdata/inferred_methods.hpp", selected, opts)
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "guard.cpp")
			write(t, source, out.Facade.CXXSource)
			args := []string{"--target=" + opts.Target, "-ffreestanding", "-std=c++17", "-fsyntax-only", source}
			if output, err := exec.Command(cppTool(t), args...).CombinedOutput(); err != nil {
				t.Fatalf("matching producer: %s: %v", output, err)
			}
			args = append(args, flag)
			if output, err := exec.Command(cppTool(t), args...).CombinedOutput(); err == nil || !strings.Contains(string(output), "inferred receiver qualifiers changed") {
				t.Fatalf("changed qualifiers escaped guard: %s: %v", output, err)
			}
			opts.Flags = append(opts.Flags, flag)
			changed, err := cxx.InferMethods(context.Background(), "testdata/inferred_methods.hpp", selected, opts)
			if err != nil || changed.Methods[0].Const != (mode == "const") || changed.Methods[0].Noexcept != (mode == "noexcept") {
				t.Fatal(changed, err)
			}
		})
	}
}

func TestNativeInferredMethodLimitsAndLambda(t *testing.T) {
	t.Run("maximum_exports", func(t *testing.T) {
		input := make([]cxx.MethodSelection, 256)
		for i := range input {
			input[i] = cxx.MethodSelection{Class: "inferred_members::Receiver", Name: "read", Symbol: fmt.Sprintf("selected_%d", i)}
		}
		out, err := cxx.InferMethods(context.Background(), "testdata/inferred_methods.hpp", input, cxx.DiscoveryOptions{Compiler: cppTool(t)})
		if err != nil || len(out.Methods) != 256 {
			t.Fatal(out, err)
		}
	})
	t.Run("generic_lambda", func(t *testing.T) {
		out, err := cxx.InferMethods(context.Background(), "testdata/inferred_methods.hpp", []cxx.MethodSelection{{Class: "inferred_members::Lambda", Name: "operator()", Symbol: "lambda_call", TemplateArgs: []string{"int"}}}, cxx.DiscoveryOptions{Compiler: cppTool(t)})
		if err != nil || !out.Methods[0].Const || out.Methods[0].Transport.Result != abi.I32 {
			t.Fatal(out, err)
		}
	})
	for _, n := range []int{32, 33} {
		t.Run(fmt.Sprintf("arguments_%d", n), func(t *testing.T) {
			var parameters []string
			for i := 0; i < n; i++ {
				parameters = append(parameters, fmt.Sprintf("int p%d", i))
			}
			header := filepath.Join(t.TempDir(), "unguarded.hpp")
			write(t, header, []byte("struct Once {}; struct Many {int call("+strings.Join(parameters, ",")+") const & {return p0;}};"))
			out, err := cxx.InferMethods(context.Background(), header, []cxx.MethodSelection{{Class: "Many", Name: "call", Symbol: "selected"}}, cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if n == 33 {
				if err == nil || out != nil {
					t.Fatal(out, err)
				}
				return
			}
			if err != nil || len(out.Methods[0].Transport.Args) != 32 {
				t.Fatal(out, err)
			}
		})
	}
}

func TestNativeInferredMethodReceiverRejections(t *testing.T) {
	for _, name := range []string{"Incomplete", "Scalar", "Pointer", "Hidden", "Ambiguous"} {
		t.Run(name, func(t *testing.T) {
			out, err := cxx.InferMethods(context.Background(), "testdata/inferred_methods.hpp", []cxx.MethodSelection{{Class: "inferred_member_bad::" + name, Name: "inherited", Symbol: "selected"}}, cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if err == nil || out != nil {
				t.Fatal(out, err)
			}
		})
	}
}
