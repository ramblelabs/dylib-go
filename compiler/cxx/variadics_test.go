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

func variadicMethod(t *testing.T, c []cxx.Class, symbol string) *cxx.Method {
	t.Helper()
	for i := range c[0].Methods {
		if c[0].Methods[i].Symbol == symbol {
			return &c[0].Methods[i]
		}
	}
	t.Fatalf("missing variadic fixture %s", symbol)
	return nil
}

func TestVariadicFacadeAcrossTargets(t *testing.T) {
	include, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := cxx.Generate("variadics.hpp", cxxexample.VariadicClasses())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(facade.CHeader), "...") {
		t.Fatal("variadic C carrier")
	}
	source := filepath.Join(t.TempDir(), "variadics.cpp")
	write(t, source, facade.CXXSource)
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			if output, err := exec.Command(cppTool(t), "--target="+target, "-ffreestanding", "-std=c++17", "-Werror=non-pod-varargs", "-fsyntax-only", "-I"+include, source).CombinedOutput(); err != nil {
				t.Fatalf("variadic facade: %v\n%s", err, output)
			}
		})
	}
}

func TestVariadicManifestValidation(t *testing.T) {
	for name, patch := range map[string]func(*cxx.Method){
		"zero_fixed":         func(m *cxx.Method) { m.Signature.FixedArgs = 0 },
		"excess_fixed":       func(m *cxx.Method) { m.Signature.FixedArgs = len(m.Signature.Args) + 1 },
		"fixed_signature":    func(m *cxx.Method) { m.Signature.Variadic = false },
		"stdcall":            func(m *cxx.Method) { m.Signature.Convention = abi.StdCall },
		"reference_tail":     func(m *cxx.Method) { m.ObjectArgs[0].Type.Kind = cxx.ObjectReference },
		"rvalue_tail":        func(m *cxx.Method) { m.ObjectArgs[0].Type.Kind = cxx.ObjectRValueReference },
		"value_tail":         func(m *cxx.Method) { m.ObjectArgs[0].Type.Kind = cxx.ObjectValue; m.ObjectArgs[0].Type.Const = false },
		"invalid_tail_index": func(m *cxx.Method) { m.ObjectArgs[0].Index = 30 },
	} {
		t.Run(name, func(t *testing.T) {
			c := cxxexample.VariadicClasses()
			patch(variadicMethod(t, c, "var_pointers"))
			if got, err := cxx.Generate("variadics.hpp", c); err == nil || got != nil {
				t.Fatal("invalid variadic selection", got, err)
			}
		})
	}
}

func TestNativeCompilerRejectsVariadicMismatch(t *testing.T) {
	include, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for name, patch := range map[string]func(*testing.T, []cxx.Class){
		"fixed_member": func(t *testing.T, c []cxx.Class) {
			m := variadicMethod(t, c, "var_call")
			m.Signature.Variadic = false
			m.Signature.FixedArgs = 0
		},
		"prefix_count": func(t *testing.T, c []cxx.Class) { variadicMethod(t, c, "var_promoted").Signature.FixedArgs = 2 },
		"prefix_type":  func(t *testing.T, c []cxx.Class) { variadicMethod(t, c, "var_empty").Signature.Args[0] = abi.I64 },
		"receiver":     func(t *testing.T, c []cxx.Class) { variadicMethod(t, c, "var_empty").Ref = cxx.RefRValue },
		"static": func(t *testing.T, c []cxx.Class) {
			m := variadicMethod(t, c, "var_empty")
			m.Static = true
			m.Const = false
			m.Ref = cxx.RefNone
		},
		"object_prefix": func(t *testing.T, c []cxx.Class) {
			variadicMethod(t, c, "var_prefix").ObjectArgs[0].Type.Kind = cxx.ObjectPointer
		},
		"function_prefix": func(t *testing.T, c []cxx.Class) {
			variadicMethod(t, c, "var_apply").FunctionArgs[0].Signature.Result = abi.F64
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := cxxexample.VariadicClasses()
			patch(t, c)
			facade, err := cxx.Generate("variadics.hpp", c)
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "invalid.cpp")
			write(t, source, facade.CXXSource)
			output, err := exec.Command(cppTool(t), "-std=c++17", "-fsyntax-only", "-I"+include, source).CombinedOutput()
			if err == nil || !strings.Contains(string(output), "error:") {
				t.Fatalf("invalid variadic prototype accepted: %v\n%s", err, output)
			}
		})
	}
}
