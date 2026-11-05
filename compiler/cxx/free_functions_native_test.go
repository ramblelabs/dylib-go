//go:build cgo && (linux || darwin || windows)

package cxx_test

import (
	"context"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
	"testing"
)

func generateFreeFunctions(t *testing.T) func(string) (*cxx.Facade, error) {
	return func(header string) (*cxx.Facade, error) {
		// Discover against the actual facade producer's target/flags before selecting
		// explicit ABI transports; descriptive C++ types are never ABI guesses.
		declarations, err := cxx.DiscoverFunctions(context.Background(), header, "free_alias", cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
		if err != nil {
			return nil, err
		}
		found := false
		for _, d := range declarations {
			found = found || d.Name == "free_alias::add"
		}
		if !found {
			t.Fatal("selected namespace overload not discovered")
		}
		return cxx.GenerateFunctions(header, cxxexample.FreeFunctions())
	}
}

func TestNativeGeneratedCgoFreeFunctionFacade(t *testing.T) {
	testGeneratedFacadeHeader(t, false, "testdata/free_functions.hpp", "testdata/free_functions_main.go", generateFreeFunctions(t))
}
func TestNativeGeneratedDirectLLGoFreeFunctionFacade(t *testing.T) {
	if !llgoHost {
		t.Skip("the direct caller requires an llgo host")
	}
	testGeneratedFacadeHeader(t, true, "testdata/free_functions.hpp", "testdata/free_functions_main.go", generateFreeFunctions(t))
}
