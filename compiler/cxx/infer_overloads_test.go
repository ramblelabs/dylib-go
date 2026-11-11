package cxx_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func TestNativeInferredOverloadsAcrossTargets(t *testing.T) {
	header, err := filepath.Abs("testdata/inferred_overloads.hpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range freeTargets {
		t.Run(target, func(t *testing.T) {
			opts := cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding"}}
			functions, err := cxxexample.InferSelectedFunctionOverloads(context.Background(), filepath.ToSlash(header), opts)
			if err != nil {
				t.Fatal(err)
			}
			methods, err := cxxexample.InferSelectedMethodOverloads(context.Background(), filepath.ToSlash(header), opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(functions.Functions) != 21 || len(methods.Methods) != 47 || len(methods.Facade.Functions) != 50 {
				t.Fatal(len(functions.Functions), len(methods.Methods), len(methods.Facade.Functions))
			}
			longType := abi.I64
			if strings.Contains(target, "windows") || strings.HasPrefix(target, "i686-") {
				longType = abi.I32
			}
			if functions.Functions[1].Transport.Args[0] != longType {
				t.Fatal(functions.Functions[1])
			}
			for i, m := range methods.Methods {
				if err := m.Transport.Validate(); err != nil {
					t.Fatal(err)
				}
				if i < 24 {
					q := i % 12
					if m.Const != (q&1 != 0) || m.Volatile != (q&2 != 0) || m.Ref != cxx.RefQualifier(q/4) || m.Noexcept != (i >= 12) || m.Transport.Result != abi.I32 || m.Transport.Args[0] != abi.I32 {
						t.Fatal(i, m)
					}
				}
				if m.Selection.Symbol == "pick_zero_const" && (!m.Const || !m.Noexcept || len(m.Transport.Args) != 0) {
					t.Fatal(m)
				}
				if m.Selection.Symbol == "pick_native_long" && m.Transport.Args[0] != longType {
					t.Fatal(m)
				}
			}
		})
	}
}

func TestNativeInferredOverloadRejections(t *testing.T) {
	for _, mode := range []string{"convertible_only", "missing_defaulted", "variadic", "deleted", "reference", "missing_type", "wrong_arity", "static_member", "wrong_cv", "wrong_ref"} {
		t.Run(mode, func(t *testing.T) {
			selected := cxx.FunctionSelection{Name: "inferred_overloads::pick", Symbol: "rejected", Overload: &cxx.FunctionOverload{ParameterTypes: []string{"bool"}}}
			switch mode {
			case "missing_defaulted":
				selected.Name = "inferred_overloads::defaults"
				selected.Overload.ParameterTypes = []string{"int"}
			case "variadic":
				selected.Name = "inferred_overloads::variadic"
				selected.Overload.ParameterTypes = []string{"int"}
			case "deleted":
				selected.Name = "inferred_overloads::deleted"
				selected.Overload.ParameterTypes = []string{"int"}
			case "reference":
				selected.Name = "inferred_overloads::reference"
				selected.Overload.ParameterTypes = []string{"inferred_overloads::Ref"}
			case "missing_type":
				selected.Overload.ParameterTypes = []string{"MissingType"}
			case "wrong_arity":
				selected.Overload.ParameterTypes = []string{"int", "int"}
			}
			opts := cxx.DiscoveryOptions{Compiler: cppTool(t)}
			if mode == "static_member" || mode == "wrong_cv" || mode == "wrong_ref" {
				method := cxx.MethodSelection{Class: "inferred_overloads::Receiver", Name: "static_only", Symbol: "rejected", Overload: &cxx.MethodOverload{ParameterTypes: []string{"int"}}}
				if mode == "wrong_cv" {
					method.Name = "plus"
					method.TemplateArgs = []string{"int"}
					method.Overload.Ref = cxx.RefLValue
				}
				if mode == "wrong_ref" {
					method.Name = "plain"
					method.Overload.Ref = cxx.RefRValue
				}
				out, err := cxx.InferMethods(context.Background(), "testdata/inferred_overloads.hpp", []cxx.MethodSelection{method}, opts)
				if err == nil || out != nil {
					t.Fatal(out, err)
				}
				return
			}
			out, err := cxx.InferFunctions(context.Background(), "testdata/inferred_overloads.hpp", []cxx.FunctionSelection{{Name: "inferred_overloads::empty", Symbol: "good", Overload: &cxx.FunctionOverload{}}, selected}, opts)
			if err == nil || out != nil {
				t.Fatal("invalid overload published partial output", out, err)
			}
		})
	}
}

func TestNativeInferredOverloadArityLimits(t *testing.T) {
	for _, count := range []int{32, 33} {
		t.Run(fmt.Sprintf("arguments_%d", count), func(t *testing.T) {
			var declarations []string
			types := make([]string, count)
			for i := range types {
				types[i] = "int"
				declarations = append(declarations, fmt.Sprintf("int p%d", i))
			}
			header := filepath.Join(t.TempDir(), "unguarded.hpp")
			write(t, header, []byte("struct Once {}; inline int many("+strings.Join(declarations, ",")+"){return p0;} struct R {int many("+strings.Join(declarations, ",")+") const & noexcept {return p0;}};"))
			opts := cxx.DiscoveryOptions{Compiler: cppTool(t)}
			functions, fe := cxx.InferFunctions(context.Background(), header, []cxx.FunctionSelection{{Name: "many", Symbol: "call", Overload: &cxx.FunctionOverload{ParameterTypes: types}}}, opts)
			methods, me := cxx.InferMethods(context.Background(), header, []cxx.MethodSelection{{Class: "R", Name: "many", Symbol: "call", Overload: &cxx.MethodOverload{ParameterTypes: types, Const: true, Ref: cxx.RefLValue}}}, opts)
			if count == 33 {
				if fe == nil || me == nil || functions != nil || methods != nil {
					t.Fatal(functions, methods, fe, me)
				}
				return
			}
			if fe != nil || me != nil || len(functions.Functions[0].Transport.Args) != 32 || len(methods.Methods[0].Transport.Args) != 32 || !methods.Methods[0].Noexcept {
				t.Fatal(functions, methods, fe, me)
			}
		})
	}
}

func TestNativeInferredOverloadGlobalTypes(t *testing.T) {
	for _, name := range []string{"Pointer", "Same", "C0"} {
		t.Run(name, func(t *testing.T) {
			opts := cxx.DiscoveryOptions{Compiler: cppTool(t)}
			functions, err := cxx.InferFunctions(context.Background(), "testdata/inferred_overloads.hpp", []cxx.FunctionSelection{{Name: "inferred_overloads::collision", Symbol: "selected", Overload: &cxx.FunctionOverload{ParameterTypes: []string{name}}}}, opts)
			if err != nil {
				t.Fatal(err)
			}
			want := abi.I32
			if name == "Pointer" {
				want = abi.Pointer
			}
			if functions.Functions[0].Transport.Args[0] != want {
				t.Fatal(functions)
			}
			if name == "C0" {
				methods, err := cxx.InferMethods(context.Background(), "testdata/inferred_overloads.hpp", []cxx.MethodSelection{{Class: "inferred_overloads::Receiver", Name: "plain", Symbol: "selected", Overload: &cxx.MethodOverload{ParameterTypes: []string{name}}}}, opts)
				if err != nil || methods.Methods[0].Transport.Args[0] != abi.I32 {
					t.Fatal(methods, err)
				}
			}
		})
	}
}
