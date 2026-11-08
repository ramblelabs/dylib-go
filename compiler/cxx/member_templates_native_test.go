//go:build cgo && (linux || darwin || windows)

package cxx_test

import (
	"context"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
	"testing"
)

func generateMemberTemplates(t *testing.T) func(string) (*cxx.Facade, error) {
	return func(header string) (*cxx.Facade, error) {
		classes, err := cxxexample.DiscoverTemplateClasses(context.Background(), header, cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
		if err != nil {
			return nil, err
		}
		return cxx.Generate(header, classes)
	}
}

func TestNativeGeneratedCgoMemberTemplates(t *testing.T) {
	testGeneratedFacadeHeader(t, false, "testdata/member_templates.hpp", "testdata/member_templates_main.go", generateMemberTemplates(t))
}
func TestNativeGeneratedDirectLLGoMemberTemplates(t *testing.T) {
	if !llgoHost {
		t.Skip("the direct caller requires an llgo host")
	}
	testGeneratedFacadeHeader(t, true, "testdata/member_templates.hpp", "testdata/member_templates_main.go", generateMemberTemplates(t))
}
