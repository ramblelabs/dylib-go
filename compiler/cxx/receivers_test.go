package cxx_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func TestReceiverManifestValidation(t *testing.T) {
	for name, patch := range map[string]func(*cxx.Method){
		"static_ref":      func(m *cxx.Method) { m.Static, m.Ref = true, cxx.RefLValue },
		"static_volatile": func(m *cxx.Method) { m.Static, m.Volatile = true, true },
		"invalid_ref":     func(m *cxx.Method) { m.Ref = cxx.RefQualifier(3) },
		"overflow_ref":    func(m *cxx.Method) { m.Ref = cxx.RefQualifier(255) },
	} {
		t.Run(name, func(t *testing.T) {
			classes := cxxexample.ReceiverClasses()
			patch(&classes[0].Methods[0])
			if got, err := cxx.Generate("receivers.hpp", classes); err == nil || got != nil {
				t.Fatal("invalid receiver manifest", got, err)
			}
		})
	}
}

func TestReceiverFacadeAcrossTargets(t *testing.T) {
	include, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := cxx.Generate("receivers.hpp", cxxexample.ReceiverClasses())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "receivers.cpp")
	write(t, source, facade.CXXSource)
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			if output, err := exec.Command(cppTool(t), "--target="+target, "-ffreestanding", "-std=c++17", "-fsyntax-only", "-I"+include, source).CombinedOutput(); err != nil {
				t.Fatalf("cv/ref-qualified C++ facade: %v\n%s", err, output)
			}
		})
	}
}

func TestNativeCompilerRejectsReceiverMismatch(t *testing.T) {
	include, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for name, patch := range map[string]func(*cxx.Method){
		"missing_ref":      func(m *cxx.Method) { m.Ref = cxx.RefNone },
		"wrong_ref":        func(m *cxx.Method) { m.Ref = cxx.RefRValue },
		"wrong_const":      func(m *cxx.Method) { m.Const = true },
		"wrong_volatile":   func(m *cxx.Method) { m.Volatile = true },
		"missing_volatile": func(m *cxx.Method) { m.Name = "volatile_only" },
	} {
		t.Run(name, func(t *testing.T) {
			classes := cxxexample.ReceiverClasses()
			m := &classes[0].Methods[0]
			m.Name, m.Ref = "only", cxx.RefLValue
			patch(m)
			facade, err := cxx.Generate("receivers.hpp", classes)
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "invalid.cpp")
			write(t, source, facade.CXXSource)
			output, err := exec.Command(cppTool(t), "-std=c++17", "-fsyntax-only", "-I"+include, source).CombinedOutput()
			if err == nil || !strings.Contains(string(output), "error:") {
				t.Fatalf("invalid receiver qualifiers accepted: %v\n%s", err, output)
			}
		})
	}
}
