//go:build cgo && (linux || darwin || windows)

package cxx_test

import (
	"context"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func generateInferredMethods(t *testing.T) func(string) (*cxx.Facade, error) {
	return func(header string) (*cxx.Facade, error) {
		out, err := cxxexample.InferSelectedMethods(context.Background(), header, cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
		if err != nil {
			return nil, err
		}
		return out.Facade, nil
	}
}
func TestNativeGeneratedCgoInferredMethods(t *testing.T) {
	testGeneratedFacadeHeader(t, false, "testdata/inferred_methods.hpp", "testdata/inferred_methods_main.go", generateInferredMethods(t))
}
func TestNativeGeneratedDirectLLGoInferredMethods(t *testing.T) {
	if !llgoHost {
		t.Skip("the direct caller requires an llgo host")
	}
	testGeneratedFacadeHeader(t, true, "testdata/inferred_methods.hpp", "testdata/inferred_methods_main.go", generateInferredMethods(t))
}
