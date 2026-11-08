//go:build cgo && (linux || darwin || windows)

package cxx_test

import (
	"context"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
	"testing"
)

func generateTemplateFunctions(t *testing.T) func(string) (*cxx.Facade, error) {
	return func(header string) (*cxx.Facade, error) {
		declarations, err := cxx.DiscoverFunctionTemplates(context.Background(), header, "template_alias", cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
		if err != nil {
			return nil, err
		}
		found := false
		for _, d := range declarations {
			found = found || d.Function.Name == "template_alias::add" && len(d.Parameters) == 2
		}
		if !found {
			t.Fatal("missing selected template")
		}
		return cxx.GenerateFunctions(header, cxxexample.TemplateFunctions())
	}
}
func generateTemplateClasses(header string) (*cxx.Facade, error) {
	return cxx.Generate(header, cxxexample.TemplateClasses())
}

func TestNativeGeneratedCgoTemplateFacade(t *testing.T) {
	t.Run("functions", func(t *testing.T) {
		testGeneratedFacadeHeader(t, false, "testdata/templates.hpp", "testdata/templates_functions_main.go", generateTemplateFunctions(t))
	})
	t.Run("classes", func(t *testing.T) {
		testGeneratedFacadeHeader(t, false, "testdata/templates.hpp", "testdata/templates_classes_main.go", generateTemplateClasses)
	})
}
func TestNativeGeneratedDirectLLGoTemplateFacade(t *testing.T) {
	if !llgoHost {
		t.Skip("the direct caller requires an llgo host")
	}
	t.Run("functions", func(t *testing.T) {
		testGeneratedFacadeHeader(t, true, "testdata/templates.hpp", "testdata/templates_functions_main.go", generateTemplateFunctions(t))
	})
	t.Run("classes", func(t *testing.T) {
		testGeneratedFacadeHeader(t, true, "testdata/templates.hpp", "testdata/templates_classes_main.go", generateTemplateClasses)
	})
}
