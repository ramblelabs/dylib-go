package cxx_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func conversionMethod(t *testing.T, c []cxx.Class, symbol string) *cxx.Method {
	t.Helper()
	for i := range c[0].Methods {
		if c[0].Methods[i].Symbol == symbol {
			return &c[0].Methods[i]
		}
	}
	t.Fatalf("missing conversion fixture %s", symbol)
	return nil
}

func TestConversionFacadeAcrossTargets(t *testing.T) {
	include, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := cxx.Generate("conversions.hpp", cxxexample.ConversionClasses())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "conversions.cpp")
	write(t, source, facade.CXXSource)
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			if output, err := exec.Command(cppTool(t), "--target="+target, "-ffreestanding", "-std=c++17", "-fsyntax-only", "-I"+include, source).CombinedOutput(); err != nil {
				t.Fatalf("conversion facade: %v\n%s", err, output)
			}
		})
	}
}

func TestConversionManifestValidation(t *testing.T) {
	for name, patch := range map[string]func(*cxx.Method){
		"name":      func(m *cxx.Method) { m.Name = "operator int" },
		"static":    func(m *cxx.Method) { m.Static = true },
		"arguments": func(m *cxx.Method) { m.Signature.Args = []abi.Type{abi.I32} },
		"void":      func(m *cxx.Method) { m.Signature.Result = abi.Void },
	} {
		t.Run(name, func(t *testing.T) {
			c := cxxexample.ConversionClasses()
			patch(conversionMethod(t, c, "convert_bool"))
			if got, err := cxx.Generate("conversions.hpp", c); err == nil || got != nil {
				t.Fatal("invalid conversion", got, err)
			}
		})
	}
}

func TestNativeCompilerRejectsConversionMismatch(t *testing.T) {
	include, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for name, patch := range map[string]func(*testing.T, []cxx.Class){
		"numeric_chain": func(t *testing.T, c []cxx.Class) { conversionMethod(t, c, "convert_lvalue").Signature.Result = abi.I64 },
		"float_chain":   func(t *testing.T, c []cxx.Class) { conversionMethod(t, c, "convert_float").Signature.Result = abi.F32 },
		"const":         func(t *testing.T, c []cxx.Class) { conversionMethod(t, c, "convert_bool").Const = false },
		"ref":           func(t *testing.T, c []cxx.Class) { conversionMethod(t, c, "convert_lvalue").Ref = cxx.RefNone },
		"reference_kind": func(t *testing.T, c []cxx.Class) {
			conversionMethod(t, c, "convert_rvalue_reference").ObjectResult.Kind = cxx.ObjectReference
		},
		"function": func(t *testing.T, c []cxx.Class) {
			conversionMethod(t, c, "convert_entry").FunctionResult.Result = abi.F64
		},
		"private": func(t *testing.T, c []cxx.Class) {
			c[0].Name = "conversions::Hidden"
			c[0].Constructors = nil
			c[0].Delete = ""
			c[0].Bases = nil
			c[0].Methods = []cxx.Method{{Symbol: "hidden", Conversion: true, Const: true, Signature: abi.Signature{Result: abi.I32}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := cxxexample.ConversionClasses()
			patch(t, c)
			facade, err := cxx.Generate("conversions.hpp", c)
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "invalid.cpp")
			write(t, source, facade.CXXSource)
			output, err := exec.Command(cppTool(t), "-std=c++17", "-fsyntax-only", "-I"+include, source).CombinedOutput()
			if err == nil || !strings.Contains(string(output), "error:") {
				t.Fatalf("invalid conversion accepted: %v\n%s", err, output)
			}
		})
	}
}
