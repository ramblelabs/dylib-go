//go:build cgo && (linux || darwin || windows)

package cxx_test

import (
	"context"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
	"testing"
)

func generateHeaderSelections(t *testing.T, kind string) func(string) (*cxx.Facade, error) {
	return func(header string) (*cxx.Facade, error) {
		functions, classes, err := cxxexample.HeaderSelections(context.Background(), header, cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
		if err != nil {
			return nil, err
		}
		if kind == "functions" {
			return cxx.GenerateFunctions(header, functions)
		}
		return cxx.Generate(header, classes)
	}
}
func TestNativeGeneratedCgoHeaderFacade(t *testing.T) {
	for _, kind := range []string{"functions", "classes"} {
		t.Run(kind, func(t *testing.T) {
			testGeneratedFacadeHeader(t, false, "testdata/header.hpp", "testdata/header_"+kind+"_main.go", generateHeaderSelections(t, kind))
		})
	}
}
func TestNativeGeneratedDirectLLGoHeaderFacade(t *testing.T) {
	if !llgoHost {
		t.Skip("the direct caller requires an llgo host")
	}
	for _, kind := range []string{"functions", "classes"} {
		t.Run(kind, func(t *testing.T) {
			testGeneratedFacadeHeader(t, true, "testdata/header.hpp", "testdata/header_"+kind+"_main.go", generateHeaderSelections(t, kind))
		})
	}
}
