package cxx_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func TestNativeInferredConversionsAcrossTargets(t *testing.T) {
	header, err := filepath.Abs("testdata/inferred_conversions.hpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range freeTargets {
		t.Run(target, func(t *testing.T) {
			out, err := cxxexample.InferSelectedConversions(context.Background(), filepath.ToSlash(header), cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Methods) != 41 || len(out.Facade.Functions) != 55 {
				t.Fatal(len(out.Methods), len(out.Facade.Functions))
			}
			for i, m := range out.Methods {
				if err := m.Transport.Validate(); err != nil {
					t.Fatal(err)
				}
				if i < 24 {
					q := i % 12
					if m.Const != (q&1 != 0) || m.Volatile != (q&2 != 0) || m.Ref != cxx.RefQualifier(q/4) || m.Noexcept != (i >= 12) || m.Transport.Result != abi.I32 {
						t.Fatal(i, m)
					}
				}
				if m.Selection.ConversionType != "" && len(m.Transport.Args) != 0 {
					t.Fatal(m)
				}
				want := m.Transport.Result
				switch m.Selection.Symbol {
				case "convert_auto_bool":
					want = abi.Bool
				case "convert_auto_narrow":
					want = abi.I8
				case "convert_auto_wide":
					want = abi.U64
				case "convert_auto_code":
					want = abi.U16
				case "convert_auto_entry", "convert_auto_pointer", "convert_auto_const_pointer", "convert_auto_self":
					want = abi.Pointer
				case "convert_auto_double", "convert_template_double":
					want = abi.F64
				case "convert_auto_long":
					want = abi.I64
					if strings.Contains(target, "windows") || strings.HasPrefix(target, "i686-") {
						want = abi.I32
					}
				}
				if m.Transport.Result != want {
					t.Fatal(m, want)
				}
			}
		})
	}
}

func TestInferredConversionInputs(t *testing.T) {
	for _, mode := range []string{"empty", "void", "expression", "raw_pointer", "type_budget", "name", "template", "parameters"} {
		t.Run(mode, func(t *testing.T) {
			s := cxx.MethodSelection{Class: "R", ConversionType: "int", Symbol: "selected"}
			switch mode {
			case "empty":
				s.ConversionType = ""
			case "void":
				s.ConversionType = "void"
			case "expression":
				s.ConversionType = "decltype(f())"
			case "raw_pointer":
				s.ConversionType = "int*"
			case "type_budget":
				s.ConversionType = strings.Repeat("T", 257)
			case "name":
				s.Name = "read"
			case "template":
				s.TemplateArgs = []string{}
			case "parameters":
				s.Overload = &cxx.MethodOverload{ParameterTypes: []string{"int"}}
			}
			out, err := cxx.InferMethods(context.Background(), "testdata/inferred_conversions.hpp", []cxx.MethodSelection{s}, cxx.DiscoveryOptions{Compiler: filepath.Join(t.TempDir(), "missing")})
			if err == nil || out != nil || !strings.Contains(err.Error(), "invalid inferred") {
				t.Fatal(out, err)
			}
		})
	}
}

func TestNativeInferredConversionRejections(t *testing.T) {
	for _, mode := range []string{"Reference", "Extended", "Nothing", "Record", "Member", "char", "bool", "missing", "hidden", "ambiguous", "overloaded", "wrong_cv", "wrong_ref"} {
		t.Run(mode, func(t *testing.T) {
			bad := cxx.MethodSelection{Class: "inferred_conversion_bad::Unsupported", ConversionType: "inferred_conversion_bad::" + mode, Symbol: "bad"}
			switch mode {
			case "char", "bool":
				bad.ConversionType = mode
			case "hidden", "ambiguous", "overloaded", "wrong_cv", "wrong_ref":
				bad.Class, bad.ConversionType = "inferred_conversions::Receiver", "int"
				if mode == "hidden" {
					bad.Class = "inferred_conversion_bad::Hidden"
				}
				if mode == "ambiguous" {
					bad.Class = "inferred_conversion_bad::Ambiguous"
				}
				if mode == "wrong_cv" {
					bad.Overload = &cxx.MethodOverload{Volatile: true, Ref: cxx.RefLValue}
				}
				if mode == "wrong_ref" {
					bad.Overload = &cxx.MethodOverload{Const: true}
				}
			}
			selected := []cxx.MethodSelection{{Class: "inferred_conversions::Receiver", Name: "read", Symbol: "good"}, bad}
			out, err := cxx.InferMethods(context.Background(), "testdata/inferred_conversions.hpp", selected, cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if err == nil || out != nil {
				t.Fatal("unsupported conversion published partial output", out, err)
			}
		})
	}
}

func TestNativeInferredConversionGlobalTypes(t *testing.T) {
	for _, target := range []string{"Pointer", "C0"} {
		t.Run(target, func(t *testing.T) {
			s := cxx.MethodSelection{Class: "inferred_conversions::Receiver", ConversionType: target, Symbol: "selected"}
			want := abi.Pointer
			if target == "C0" {
				s.Overload = &cxx.MethodOverload{Const: true, Ref: cxx.RefLValue}
				want = abi.I32
			}
			out, err := cxx.InferMethods(context.Background(), "testdata/inferred_conversions.hpp", []cxx.MethodSelection{s}, cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if err != nil || out.Methods[0].Transport.Result != want {
				t.Fatal(out, err)
			}
		})
	}
}

func TestNativeInferredConversionProducerGuard(t *testing.T) {
	opts := cxx.DiscoveryOptions{Compiler: cppTool(t), Target: "x86_64-unknown-linux-gnu", Flags: []string{"-ffreestanding"}}
	selected := []cxx.MethodSelection{{Class: "inferred_conversions::Changing", ConversionType: "int", Symbol: "guarded"}}
	out, err := cxx.InferMethods(context.Background(), "testdata/inferred_conversions.hpp", selected, opts)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "guard.cpp")
	write(t, source, out.Facade.CXXSource)
	args := []string{"--target=" + opts.Target, "-ffreestanding", "-std=c++17", "-fsyntax-only", source}
	if output, err := exec.Command(cppTool(t), args...).CombinedOutput(); err != nil {
		t.Fatalf("matching producer: %s: %v", output, err)
	}
	args = append(args, "-DINFERRED_CONVERSION_CONST")
	if output, err := exec.Command(cppTool(t), args...).CombinedOutput(); err == nil || !strings.Contains(string(output), "inferred receiver qualifiers changed") {
		t.Fatalf("changed qualifiers escaped guard: %s: %v", output, err)
	}
}

func TestNativeInferredConversionUnguardedHeader(t *testing.T) {
	header := filepath.Join(t.TempDir(), "unguarded.hpp")
	write(t, header, []byte("struct Once {}; using Integer=int; struct R {explicit operator Integer() const noexcept {return 42;}};"))
	out, err := cxx.InferMethods(context.Background(), header, []cxx.MethodSelection{{Class: "R", ConversionType: "Integer", Symbol: "selected"}}, cxx.DiscoveryOptions{Compiler: cppTool(t)})
	if err != nil || !out.Methods[0].Const || !out.Methods[0].Noexcept || out.Methods[0].Transport.Result != abi.I32 {
		t.Fatal(out, err)
	}
}

func TestNativeInferredConversionQualifiedResult(t *testing.T) {
	for _, mode := range []string{"inferred", "selected"} {
		t.Run(mode, func(t *testing.T) {
			header := filepath.Join(t.TempDir(), "qualified.hpp")
			write(t, header, []byte("using Constant=const int; struct R {explicit operator Constant() const & noexcept {return 42;}};"))
			s := cxx.MethodSelection{Class: "R", ConversionType: "Constant", Symbol: "selected"}
			if mode == "selected" {
				s.Overload = &cxx.MethodOverload{Const: true, Ref: cxx.RefLValue}
			}
			out, err := cxx.InferMethods(context.Background(), header, []cxx.MethodSelection{s}, cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if err != nil || !out.Methods[0].Const || out.Methods[0].Ref != cxx.RefLValue || !out.Methods[0].Noexcept || out.Methods[0].Transport.Result != abi.I32 {
				t.Fatal(out, err)
			}
		})
	}
}
