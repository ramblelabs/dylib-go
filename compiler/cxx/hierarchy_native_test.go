//go:build cgo && (linux || darwin || windows)

package cxx_test

import (
	"context"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func generateHierarchy(t *testing.T) func(string) (*cxx.Facade, error) {
	return func(header string) (*cxx.Facade, error) {
		return cxxexample.DiscoverHierarchy(context.Background(), header, cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
	}
}
func TestNativeGeneratedCgoHierarchyFacade(t *testing.T) {
	testGeneratedFacadeHeader(t, false, "testdata/hierarchy.hpp", "testdata/hierarchy_main.go", generateHierarchy(t))
}
func TestNativeGeneratedDirectLLGoHierarchyFacade(t *testing.T) {
	if !llgoHost {
		t.Skip("the direct caller requires an llgo host")
	}
	testGeneratedFacadeHeader(t, true, "testdata/hierarchy.hpp", "testdata/hierarchy_main.go", generateHierarchy(t))
}
