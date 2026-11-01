//go:build cgo && (linux || darwin || windows)

package cxx_test

import (
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func generateReceivers(header string) (*cxx.Facade, error) {
	return cxx.Generate(header, cxxexample.ReceiverClasses())
}

func TestNativeGeneratedCgoReceiverFacade(t *testing.T) {
	testGeneratedFacadeHeader(t, false, "testdata/receivers.hpp", "testdata/receivers_main.go", generateReceivers)
}

func TestNativeGeneratedDirectLLGoReceiverFacade(t *testing.T) {
	if !llgoHost {
		t.Skip("the direct caller requires an llgo host")
	}
	testGeneratedFacadeHeader(t, true, "testdata/receivers.hpp", "testdata/receivers_main.go", generateReceivers)
}
