//go:build cgo && (linux || darwin || windows)

package cxx_test

import (
	"context"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

// The guarded fixture header permits composing its owner/function/member TUs.
func generateInferredOverloads(t *testing.T) func(string) (*cxx.Facade, error) {
	return func(header string) (*cxx.Facade, error) {
		opts := cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()}
		functions, err := cxxexample.InferSelectedFunctionOverloads(context.Background(), header, opts)
		if err != nil {
			return nil, err
		}
		methods, err := cxxexample.InferSelectedMethodOverloads(context.Background(), header, opts)
		if err != nil {
			return nil, err
		}
		return &cxx.Facade{
			CHeader:   append(functions.Facade.CHeader, methods.Facade.CHeader...),
			CXXSource: append(functions.Facade.CXXSource, methods.Facade.CXXSource...),
			Functions: append(functions.Facade.Functions, methods.Facade.Functions...),
		}, nil
	}
}

func TestNativeGeneratedCgoInferredOverloads(t *testing.T) {
	testGeneratedFacadeHeader(t, false, "testdata/inferred_overloads.hpp", "testdata/inferred_overloads_main.go", generateInferredOverloads(t))
}

func TestNativeGeneratedDirectLLGoInferredOverloads(t *testing.T) {
	if !llgoHost {
		t.Skip("the direct caller requires an llgo host")
	}
	testGeneratedFacadeHeader(t, true, "testdata/inferred_overloads.hpp", "testdata/inferred_overloads_main.go", generateInferredOverloads(t))
}
