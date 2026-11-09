//go:build cgo && (linux || darwin || windows)

package cxx_test

import (
	"context"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func generateHeaderHierarchy(t *testing.T) func(string) (*cxx.Facade, error) {
	return func(header string) (*cxx.Facade, error) {
		return cxxexample.DiscoverHeaderHierarchy(context.Background(), header, cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
	}
}
func TestNativeGeneratedCgoHeaderHierarchyFacade(t *testing.T) {
	testGeneratedFacadeHeader(t, false, "testdata/header_hierarchy.hpp", "testdata/hierarchy_main.go", generateHeaderHierarchy(t))
}
func TestNativeGeneratedDirectLLGoHeaderHierarchyFacade(t *testing.T) {
	if !llgoHost {
		t.Skip("the direct caller requires an llgo host")
	}
	testGeneratedFacadeHeader(t, true, "testdata/header_hierarchy.hpp", "testdata/hierarchy_main.go", generateHeaderHierarchy(t))
}
