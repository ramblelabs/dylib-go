//go:build cgo && (linux || darwin || windows)

package cxx_test

import (
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func generateVariadicFunctions(header string) (*cxx.Facade, error) {
	return cxx.Generate(header, cxxexample.VariadicFunctionClasses())
}

func TestNativeGeneratedCgoVariadicFunctionFacade(t *testing.T) {
	testGeneratedFacadeHeader(t, false, "testdata/variadic_functions.hpp", "testdata/variadic_functions_main.go", generateVariadicFunctions)
}

func TestNativeGeneratedDirectLLGoVariadicFunctionFacade(t *testing.T) {
	if !llgoHost {
		t.Skip("the direct caller requires an llgo host")
	}
	testGeneratedFacadeHeader(t, true, "testdata/variadic_functions.hpp", "testdata/variadic_functions_main.go", generateVariadicFunctions)
}
