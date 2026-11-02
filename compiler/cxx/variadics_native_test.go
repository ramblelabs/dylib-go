//go:build cgo && (linux || darwin || windows)

package cxx_test

import (
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func generateVariadics(header string) (*cxx.Facade, error) {
	return cxx.Generate(header, cxxexample.VariadicClasses())
}

func TestNativeGeneratedCgoVariadicFacade(t *testing.T) {
	testGeneratedFacadeHeader(t, false, "testdata/variadics.hpp", "testdata/variadics_main.go", generateVariadics)
}

func TestNativeGeneratedDirectLLGoVariadicFacade(t *testing.T) {
	if !llgoHost {
		t.Skip("the direct caller requires an llgo host")
	}
	testGeneratedFacadeHeader(t, true, "testdata/variadics.hpp", "testdata/variadics_main.go", generateVariadics)
}
