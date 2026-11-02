//go:build cgo && (linux || darwin || windows)

package cxx_test

import (
	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
	"testing"
)

func generateConversions(header string) (*cxx.Facade, error) {
	return cxx.Generate(header, cxxexample.ConversionClasses())
}

func TestNativeGeneratedCgoConversionFacade(t *testing.T) {
	testGeneratedFacadeHeader(t, false, "testdata/conversions.hpp", "testdata/conversions_main.go", generateConversions)
}

func TestNativeGeneratedDirectLLGoConversionFacade(t *testing.T) {
	if !llgoHost {
		t.Skip("the direct caller requires an llgo host")
	}
	testGeneratedFacadeHeader(t, true, "testdata/conversions.hpp", "testdata/conversions_main.go", generateConversions)
}
