package cxx_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func moveMethod(t *testing.T, classes []cxx.Class, symbol string) *cxx.Method {
	t.Helper()
	for i := range classes[0].Methods {
		if classes[0].Methods[i].Symbol == symbol {
			return &classes[0].Methods[i]
		}
	}
	t.Fatalf("missing move fixture method %s", symbol)
	return nil
}

func TestMoveObjectFacadeAcrossTargets(t *testing.T) {
	include, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := cxx.Generate("moves.hpp", cxxexample.MoveClasses())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "moves.cpp")
	write(t, source, facade.CXXSource)
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			if output, err := exec.Command(cppTool(t), "--target="+target, "-ffreestanding", "-std=c++17", "-fsyntax-only", "-I"+include, source).CombinedOutput(); err != nil {
				t.Fatalf("rvalue/CV object facade: %v\n%s", err, output)
			}
		})
	}
}

func TestMoveObjectManifestValidation(t *testing.T) {
	for name, patch := range map[string]func(*testing.T, []cxx.Class){
		"volatile_value_argument": func(t *testing.T, c []cxx.Class) {
			m := moveMethod(t, c, "move_consume")
			m.ObjectArgs[0].Type.Kind = cxx.ObjectValue
			m.ObjectArgs[0].Type.Volatile = true
		},
		"volatile_value_result": func(t *testing.T, c []cxx.Class) {
			m := moveMethod(t, c, "move_self0")
			m.ObjectResult.Kind = cxx.ObjectValue
			m.ObjectResult.Volatile = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := cxxexample.MoveClasses()
			patch(t, c)
			if got, err := cxx.Generate("moves.hpp", c); err == nil || got != nil {
				t.Fatal("cv-qualified value accepted", got, err)
			}
		})
	}
}

func TestNativeCompilerRejectsMoveObjectMismatch(t *testing.T) {
	include, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for name, patch := range map[string]func(*testing.T, []cxx.Class){
		"lvalue_argument": func(t *testing.T, c []cxx.Class) {
			moveMethod(t, c, "move_consume").ObjectArgs[0].Type.Kind = cxx.ObjectReference
		},
		"copy_argument": func(t *testing.T, c []cxx.Class) { c[0].Constructors[1].ObjectArgs[0].Type.Kind = cxx.ObjectValue },
		"lvalue_result": func(t *testing.T, c []cxx.Class) {
			moveMethod(t, c, "move_self0").ObjectResult.Kind = cxx.ObjectReference
		},
		"result_const":    func(t *testing.T, c []cxx.Class) { moveMethod(t, c, "move_self0").ObjectResult.Const = true },
		"result_volatile": func(t *testing.T, c []cxx.Class) { moveMethod(t, c, "move_self0").ObjectResult.Volatile = true },
	} {
		t.Run(name, func(t *testing.T) {
			c := cxxexample.MoveClasses()
			patch(t, c)
			facade, err := cxx.Generate("moves.hpp", c)
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "invalid.cpp")
			write(t, source, facade.CXXSource)
			output, err := exec.Command(cppTool(t), "-std=c++17", "-fsyntax-only", "-I"+include, source).CombinedOutput()
			if err == nil || !strings.Contains(string(output), "error:") {
				t.Fatalf("invalid move contract: %v\n%s", err, output)
			}
		})
	}
}
