//go:build cgo && (linux || darwin || windows)

package cxx_test

import (
	"context"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func generateDirectBases(t *testing.T) func(string) (*cxx.Facade, error) {
	return func(header string) (*cxx.Facade, error) {
		return cxxexample.DiscoverDirectBases(context.Background(), header, cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
	}
}

func TestNativeGeneratedCgoDirectBaseFacade(t *testing.T) {
	testGeneratedFacadeHeader(t, false, "testdata/direct_bases.hpp", "testdata/direct_bases_main.go", generateDirectBases(t))
}

func TestNativeGeneratedDirectLLGoDirectBaseFacade(t *testing.T) {
	if !llgoHost {
		t.Skip("the direct caller requires an llgo host")
	}
	testGeneratedFacadeHeader(t, true, "testdata/direct_bases.hpp", "testdata/direct_bases_main.go", generateDirectBases(t))
}
