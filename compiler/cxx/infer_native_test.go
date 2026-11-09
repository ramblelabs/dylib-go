//go:build cgo && (linux || darwin || windows)

package cxx_test

import (
	"context"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func generateInferredFunctions(t *testing.T) func(string) (*cxx.Facade, error) {
	return func(header string) (*cxx.Facade, error) {
		inferred, err := cxxexample.InferSelectedFunctions(context.Background(), header, cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
		if err != nil {
			return nil, err
		}
		return inferred.Facade, nil
	}
}
func TestNativeGeneratedCgoInferredFacade(t *testing.T) {
	testGeneratedFacadeHeader(t, false, "testdata/inferred.hpp", "testdata/inferred_main.go", generateInferredFunctions(t))
}
func TestNativeGeneratedDirectLLGoInferredFacade(t *testing.T) {
	if !llgoHost {
		t.Skip("the direct caller requires an llgo host")
	}
	testGeneratedFacadeHeader(t, true, "testdata/inferred.hpp", "testdata/inferred_main.go", generateInferredFunctions(t))
}
