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

func TestVariadicFunctionFacadeAcrossTargets(t *testing.T) {
	include, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := cxx.Generate("variadic_functions.hpp", cxxexample.VariadicFunctionClasses())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(facade.CHeader), "...") {
		t.Fatal("variadic C carrier")
	}
	source := filepath.Join(t.TempDir(), "variadic_functions.cpp")
	write(t, source, facade.CXXSource)
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			if output, err := exec.Command(cppTool(t), "--target="+target, "-ffreestanding", "-std=c++17", "-fsyntax-only", "-I"+include, source).CombinedOutput(); err != nil {
				t.Fatalf("variadic function facade: %v\n%s", err, output)
			}
		})
	}
}

func TestVariadicFunctionManifestValidation(t *testing.T) {
	for name, patch := range map[string]func([]cxx.Class){
		"constructor_tail": func(c []cxx.Class) {
			c[0].Constructors[0].FunctionArgs[0].Signature.Args = append(c[0].Constructors[0].FunctionArgs[0].Signature.Args, abi.I32)
		},
		"argument_tail": func(c []cxx.Class) {
			c[0].Methods[4].FunctionArgs[0].Signature.Args = append(c[0].Methods[4].FunctionArgs[0].Signature.Args, abi.F32)
		},
		"result_tail": func(c []cxx.Class) {
			c[0].Methods[0].FunctionResult.Args = append(c[0].Methods[0].FunctionResult.Args, abi.I32)
		},
		"zero_prefix": func(c []cxx.Class) { c[0].Constructors[0].FunctionArgs[0].Signature.FixedArgs = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			c := cxxexample.VariadicFunctionClasses()
			patch(c)
			if got, err := cxx.Generate("variadic_functions.hpp", c); err == nil || got != nil {
				t.Fatal("invalid variadic function descriptor", got, err)
			}
		})
	}
}

func TestNativeCompilerRejectsVariadicFunctionMismatch(t *testing.T) {
	include, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for name, patch := range map[string]func([]cxx.Class){
		"fixed_constructor": func(c []cxx.Class) {
			s := &c[0].Constructors[0].FunctionArgs[0].Signature
			s.Variadic = false
			s.FixedArgs = 0
		},
		"fixed_argument": func(c []cxx.Class) {
			s := &c[0].Methods[4].FunctionArgs[0].Signature
			s.Variadic = false
			s.FixedArgs = 0
		},
		"fixed_result": func(c []cxx.Class) { s := c[0].Methods[0].FunctionResult; s.Variadic = false; s.FixedArgs = 0 },
		"prefix":       func(c []cxx.Class) { c[0].Constructors[0].FunctionArgs[0].Signature.Args = []abi.Type{abi.I64} },
		"result":       func(c []cxx.Class) { c[0].Methods[0].FunctionResult.Result = abi.F64 },
	} {
		t.Run(name, func(t *testing.T) {
			c := cxxexample.VariadicFunctionClasses()
			patch(c)
			facade, err := cxx.Generate("variadic_functions.hpp", c)
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "invalid.cpp")
			write(t, source, facade.CXXSource)
			output, err := exec.Command(cppTool(t), "-std=c++17", "-fsyntax-only", "-I"+include, source).CombinedOutput()
			if err == nil || !strings.Contains(string(output), "error:") {
				t.Fatalf("invalid variadic function prototype accepted: %v\n%s", err, output)
			}
		})
	}
}
