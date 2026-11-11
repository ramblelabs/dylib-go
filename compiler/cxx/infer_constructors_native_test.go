//go:build cgo && (linux || darwin || windows)

package cxx_test

import (
	"context"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func generateInferredConstructors(t *testing.T) func(string) (*cxx.Facade, error) {
	return func(header string) (*cxx.Facade, error) {
		out, err := cxxexample.InferSelectedConstructors(context.Background(), header, cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
		if err != nil {
			return nil, err
		}
		return out.Facade, nil
	}
}

func TestNativeGeneratedCgoInferredConstructors(t *testing.T) {
	testGeneratedFacadeHeader(t, false, "testdata/inferred_constructors.hpp", "testdata/inferred_constructors_main.go", generateInferredConstructors(t))
}

func TestNativeGeneratedDirectLLGoInferredConstructors(t *testing.T) {
	if !llgoHost {
		t.Skip("the direct caller requires an llgo host")
	}
	testGeneratedFacadeHeader(t, true, "testdata/inferred_constructors.hpp", "testdata/inferred_constructors_main.go", generateInferredConstructors(t))
}
