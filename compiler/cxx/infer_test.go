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
)

func inferredSelections() []cxx.FunctionSelection {
	names := []string{"add", "narrow", "wide", "native_long", "character", "code", "toggle", "plain", "boolean", "floating", "mixed", "checked", "constant", "pair", "echo", "store", "read", "entry", "apply", "Static::twice"}
	var out []cxx.FunctionSelection
	for _, name := range names {
		out = append(out, cxx.FunctionSelection{Name: "inferred_api::" + name, Symbol: "auto_" + strings.ReplaceAll(name, "::", "_")})
	}
	out = append(out, cxx.FunctionSelection{Name: "inferred_api::plus", Symbol: "auto_plus", TemplateArgs: []string{"int", "2"}})
	return out
}

func TestNativeInferredFunctionsAcrossTargets(t *testing.T) {
	header, err := filepath.Abs("testdata/inferred.hpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range freeTargets {
		t.Run(target, func(t *testing.T) {
			out, err := cxx.InferFunctions(context.Background(), header, inferredSelections(), cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding", "-fsigned-char"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Functions) != 21 || len(out.Facade.Functions) != 21 {
				t.Fatal(out)
			}
			long := abi.I64
			if strings.Contains(target, "windows") || strings.HasPrefix(target, "i686-") {
				long = abi.I32
			}
			plain := abi.U32
			if strings.Contains(target, "windows") {
				plain = abi.I32
			}
			results := []abi.Type{abi.I32, abi.I8, abi.U64, long, abi.I8, abi.U16, abi.Bool, plain, abi.Bool, abi.F32, abi.F64, abi.I32, abi.I32, abi.Pointer, abi.Pointer, abi.Void, abi.I32, abi.Pointer, abi.I32, abi.F64, abi.I32}
			for i, f := range out.Functions {
				if f.Transport.Result != results[i] {
					t.Fatal(i, f.Transport, results[i])
				}
				if err := f.Transport.Validate(); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(out.Functions[0].Transport.Args, []abi.Type{abi.I32, abi.I32}) || !reflect.DeepEqual(out.Functions[10].Transport.Args, []abi.Type{abi.F32, abi.F64}) || !reflect.DeepEqual(out.Functions[18].Transport.Args, []abi.Type{abi.Pointer, abi.I32}) {
				t.Fatal(out.Functions)
			}
		})
	}
}

func TestInferredFunctionInputs(t *testing.T) {
	for _, mode := range []string{"empty", "too_many", "name", "keyword", "long_name", "symbol", "long_symbol", "duplicate", "template_expression", "nil_context", "cancelled", "missing_compiler"} {
		t.Run(mode, func(t *testing.T) {
			input := []cxx.FunctionSelection{{Name: "inferred_api::add", Symbol: "auto_add"}}
			ctx := context.Background()
			switch mode {
			case "empty":
				input = nil
			case "too_many":
				input = make([]cxx.FunctionSelection, 257)
			case "name":
				input[0].Name = "f;injected"
			case "keyword":
				input[0].Name = "class"
			case "long_name":
				input[0].Name = strings.Repeat("a", 257)
			case "symbol":
				input[0].Symbol = "bad;"
			case "long_symbol":
				input[0].Symbol = strings.Repeat("a", 257)
			case "duplicate":
				input = append(input, input[0])
			case "template_expression":
				input[0].TemplateArgs = []string{"sizeof(int)"}
			case "nil_context":
				ctx = nil
			case "cancelled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			got, err := cxx.InferFunctions(ctx, "testdata/inferred.hpp", input, cxx.DiscoveryOptions{Compiler: filepath.Join(t.TempDir(), "missing")})
			if err == nil || got != nil {
				t.Fatal(got, err)
			}
		})
	}
}

func TestNativeInferredFunctionRejections(t *testing.T) {
	for _, name := range []string{"inferred_api::value", "inferred_api::reference", "inferred_api::referred", "inferred_api::extended", "inferred_api::variadic", "inferred_api::overloaded", "inferred_api::deleted", "inferred_api::plus", "inferred_bad::Method::call", "inferred_bad::data"} {
		t.Run(name, func(t *testing.T) {
			input := []cxx.FunctionSelection{{Name: "inferred_api::add", Symbol: "good"}, {Name: name, Symbol: "bad"}}
			if got, err := cxx.InferFunctions(context.Background(), "testdata/inferred.hpp", input, cxx.DiscoveryOptions{Compiler: cppTool(t)}); err == nil || got != nil {
				t.Fatal("unsupported native contract published partial output", got, err)
			}
		})
	}
}

// Compiler flags and target data models are part of the inferred contract.
func TestNativeInferredProducerGuards(t *testing.T) {
	target := "x86_64-unknown-linux-gnu"
	for _, mode := range []string{"signed_char", "enum_width", "native_type", "pointer_width"} {
		t.Run(mode, func(t *testing.T) {
			name := "character"
			if mode == "enum_width" {
				name = "plain"
			}
			if mode == "native_type" {
				name = "changing"
			}
			if mode == "pointer_width" {
				name = "echo"
			}
			flags := []string{"-ffreestanding", "-fsigned-char"}
			out, err := cxx.InferFunctions(context.Background(), "testdata/inferred.hpp", []cxx.FunctionSelection{{Name: "inferred_api::" + name, Symbol: "guarded"}}, cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: flags})
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "guard.cpp")
			write(t, source, out.Facade.CXXSource)
			args := []string{"--target=" + target, "-std=c++17", "-fsyntax-only"}
			args = append(args, flags...)
			args = append(args, source)
			if output, err := exec.Command(cppTool(t), args...).CombinedOutput(); err != nil {
				t.Fatalf("matching producer: %s: %v", output, err)
			}
			changed := []string{"--target=" + target, "-std=c++17", "-fsyntax-only", "-ffreestanding", "-fsigned-char"}
			switch mode {
			case "signed_char":
				changed = append(changed, "-funsigned-char")
			case "enum_width":
				changed = append(changed, "-fshort-enums")
			case "native_type":
				changed = append(changed, "-DINFERRED_WIDE")
			case "pointer_width":
				changed[0] = "--target=i686-unknown-linux-gnu"
			}
			changed = append(changed, source)
			output, err := exec.Command(cppTool(t), changed...).CombinedOutput()
			if err == nil || !strings.Contains(string(output), "inferred ") || !strings.Contains(string(output), "changed") {
				t.Fatalf("mismatched producer escaped guards: %s: %v", output, err)
			}
			if mode == "signed_char" || mode == "enum_width" {
				changedFlags := []string{"-ffreestanding", "-fsigned-char"}
				want := abi.U8
				if mode == "signed_char" {
					changedFlags = append(changedFlags, "-funsigned-char")
				} else {
					changedFlags = append(changedFlags, "-fshort-enums")
				}
				changedFacade, err := cxx.InferFunctions(context.Background(), "testdata/inferred.hpp", []cxx.FunctionSelection{{Name: "inferred_api::" + name, Symbol: "changed"}}, cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: changedFlags})
				if err != nil || changedFacade.Functions[0].Transport.Result != want || changedFacade.Functions[0].Transport.Args[0] != want {
					t.Fatal(changedFacade, err)
				}
			}
		})
	}
}

func TestNativeInferredSelectionLimits(t *testing.T) {
	t.Run("maximum_exports", func(t *testing.T) {
		input := make([]cxx.FunctionSelection, 256)
		for i := range input {
			input[i] = cxx.FunctionSelection{Name: "inferred_api::constant", Symbol: fmt.Sprintf("selected_%d", i)}
		}
		out, err := cxx.InferFunctions(context.Background(), "testdata/inferred.hpp", input, cxx.DiscoveryOptions{Compiler: cppTool(t)})
		if err != nil || len(out.Facade.Functions) != 256 {
			t.Fatal(out, err)
		}
	})
	for _, count := range []int{32, 33} {
		t.Run(fmt.Sprintf("arguments_%d", count), func(t *testing.T) {
			var args, body []string
			for i := 0; i < count; i++ {
				args = append(args, fmt.Sprintf("int p%d", i))
				body = append(body, fmt.Sprintf("p%d", i))
			}
			header := filepath.Join(t.TempDir(), "unguarded.hpp")
			write(t, header, []byte("struct Once {};\ninline int many("+strings.Join(args, ",")+"){return "+strings.Join(body, "+")+";}\n"))
			out, err := cxx.InferFunctions(context.Background(), header, []cxx.FunctionSelection{{Name: "many", Symbol: "selected"}}, cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if count == 33 {
				if err == nil || out != nil {
					t.Fatal(out, err)
				}
				return
			}
			if err != nil || len(out.Functions[0].Transport.Args) != 32 {
				t.Fatal(out, err)
			}
		})
	}
	t.Run("generated_source_collision", func(t *testing.T) {
		header := filepath.Join(t.TempDir(), "collision.hpp")
		write(t, header, []byte("inline int native(){return 42;}\ninline int selected=0;\n"))
		out, err := cxx.InferFunctions(context.Background(), header, []cxx.FunctionSelection{{Name: "native", Symbol: "selected"}}, cxx.DiscoveryOptions{Compiler: cppTool(t)})
		if err == nil || out != nil {
			t.Fatal("invalid final source published", out, err)
		}
	})
}
