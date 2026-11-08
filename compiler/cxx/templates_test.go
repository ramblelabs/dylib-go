package cxx_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func TestNativeDiscoverFunctionTemplatesAcrossTargets(t *testing.T) {
	header, err := filepath.Abs("testdata/templates.hpp")
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"add", "choose", "count", "boxed", "unnamed", "forward", "forward", "deleted", "sum", "signed_limit", "unsigned_limit", "pointer", "identity", "read", "factory", "invoke", "child::echo", "reopened"}
	for _, target := range freeTargets {
		t.Run(target, func(t *testing.T) {
			for _, scope := range []string{"templates", "template_alias", "templates::child", "template_empty", "template_imports"} {
				t.Run(scope, func(t *testing.T) {
					got, err := cxx.DiscoverFunctionTemplates(context.Background(), header, scope, cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding"}})
					if err != nil {
						t.Fatal(err)
					}
					names := expected
					if scope == "templates::child" {
						names = []string{"echo"}
					}
					if scope == "template_empty" || scope == "template_imports" {
						names = nil
					}
					var actual, want []string
					for _, name := range names {
						want = append(want, scope+"::"+name)
					}
					for _, d := range got {
						actual = append(actual, d.Function.Name)
						if len(d.Parameters) == 0 || d.Function.NativeType == "" {
							t.Fatal("missing template", d)
						}
						switch {
						case strings.HasSuffix(d.Function.Name, "::add"):
							if len(d.Parameters) != 2 || d.Parameters[0].Kind != cxx.TemplateTypeParameter || d.Parameters[1].Kind != cxx.TemplateValueParameter || !d.Parameters[1].HasDefault || d.Parameters[1].NativeType != "int" {
								t.Fatal(d)
							}
						case strings.HasSuffix(d.Function.Name, "::count"):
							if !d.Parameters[0].Pack {
								t.Fatal(d)
							}
						case strings.HasSuffix(d.Function.Name, "::boxed"):
							if d.Parameters[0].Kind != cxx.TemplateTemplateParameter || len(d.Parameters[0].Parameters) != 1 || !d.Parameters[1].HasDefault {
								t.Fatal(d)
							}
						case strings.HasSuffix(d.Function.Name, "::unnamed"):
							if d.Parameters[0].Name != "" || d.Parameters[1].Name != "" {
								t.Fatal(d)
							}
						case strings.HasSuffix(d.Function.Name, "::deleted"):
							if !d.Function.Deleted {
								t.Fatal(d)
							}
						case strings.HasSuffix(d.Function.Name, "::sum"):
							if !d.Function.Variadic {
								t.Fatal(d)
							}
						}
					}
					if !reflect.DeepEqual(actual, want) {
						t.Fatalf("got %v want %v", actual, want)
					}
				})
			}
		})
	}
}

func TestTemplateFacadeAcrossTargets(t *testing.T) {
	include, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	functions, err := cxx.GenerateFunctions("templates.hpp", cxxexample.TemplateFunctions())
	if err != nil {
		t.Fatal(err)
	}
	classes, err := cxx.Generate("templates.hpp", cxxexample.TemplateClasses())
	if err != nil {
		t.Fatal(err)
	}
	for label, facade := range map[string]*cxx.Facade{"functions": functions, "classes": classes} {
		source := filepath.Join(t.TempDir(), label+".cpp")
		write(t, source, facade.CXXSource)
		for _, target := range freeTargets {
			t.Run(label+"/"+target, func(t *testing.T) {
				if output, err := exec.Command(cppTool(t), "--target="+target, "-ffreestanding", "-std=c++17", "-fsyntax-only", "-I"+include, source).CombinedOutput(); err != nil {
					t.Fatalf("template facade: %v\n%s", err, output)
				}
			})
		}
	}
}

func TestTemplateManifestValidation(t *testing.T) {
	for name, patch := range map[string]func(*cxx.Function){
		"expression":        func(f *cxx.Function) { f.TemplateArgs = []string{"sizeof(int)"} },
		"nested_type":       func(f *cxx.Function) { f.TemplateArgs = []string{"Box<int>"} },
		"injection":         func(f *cxx.Function) { f.TemplateArgs = []string{"int>; x(); //"} },
		"unsigned_negative": func(f *cxx.Function) { f.TemplateArgs = []string{"-1ULL"} },
	} {
		t.Run(name, func(t *testing.T) {
			f := cxxexample.TemplateFunctions()[0]
			patch(&f)
			if v, err := cxx.GenerateFunctions("templates.hpp", []cxx.Function{f}); err == nil || v != nil {
				t.Fatal(v, err)
			}
		})
	}
	t.Run("conversion", func(t *testing.T) {
		classes := []cxx.Class{{Name: "C", Methods: []cxx.Method{{Conversion: true, Symbol: "convert", Signature: abi.Signature{Result: abi.I32}, TemplateArgs: []string{}}}}}
		if v, err := cxx.Generate("c.hpp", classes); err == nil || v != nil {
			t.Fatal(v, err)
		}
	})
}

func TestNativeCompilerRejectsTemplateMismatch(t *testing.T) {
	include, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for name, patch := range map[string]func(*cxx.Function){
		"missing_type":     func(f *cxx.Function) { f.TemplateArgs = []string{"missing"} },
		"wrong_type":       func(f *cxx.Function) { f.TemplateArgs = []string{"double", "2"} },
		"wrong_value":      func(f *cxx.Function) { f.TemplateArgs = []string{"int32_t", "templates::Box"} },
		"excess_arguments": func(f *cxx.Function) { f.TemplateArgs = []string{"int32_t", "2", "3"} },
		"deleted":          func(f *cxx.Function) { f.Name = "templates::deleted"; f.TemplateArgs = []string{"int32_t"} },
	} {
		t.Run(name, func(t *testing.T) {
			f := cxxexample.TemplateFunctions()[0]
			patch(&f)
			facade, err := cxx.GenerateFunctions("templates.hpp", []cxx.Function{f})
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "bad.cpp")
			write(t, source, facade.CXXSource)
			if _, err := exec.Command(cppTool(t), "-std=c++17", "-fsyntax-only", "-I"+include, source).CombinedOutput(); err == nil {
				t.Fatal("invalid template accepted")
			}
		})
	}
}

func TestFunctionTemplateDiscoveryBounds(t *testing.T) {
	header, err := filepath.Abs("testdata/templates.hpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "::templates", "templates;", "missing", "templates::Counter", strings.Repeat("a", 257)} {
		t.Run(fmt.Sprintf("namespace_%q", name), func(t *testing.T) {
			if v, err := cxx.DiscoverFunctionTemplates(context.Background(), header, name, cxx.DiscoveryOptions{Compiler: cppTool(t)}); err == nil || v != nil {
				t.Fatal(v, err)
			}
		})
	}
	t.Run("nil_context", func(t *testing.T) {
		if v, err := cxx.DiscoverFunctionTemplates(nil, header, "templates", cxx.DiscoveryOptions{}); err == nil || v != nil {
			t.Fatal(v, err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if v, err := cxx.DiscoverFunctionTemplates(ctx, header, "templates", cxx.DiscoveryOptions{}); err != context.Canceled || v != nil {
			t.Fatal(v, err)
		}
	})
	for _, count := range []int{256, 257} {
		t.Run(fmt.Sprintf("declarations_%d", count), func(t *testing.T) {
			var text strings.Builder
			text.WriteString("namespace bounded {\n")
			for i := 0; i < count; i++ {
				fmt.Fprintf(&text, "template<class T> T f%d(T);\n", i)
			}
			text.WriteString("}\n")
			path := filepath.Join(t.TempDir(), "bounded.hpp")
			write(t, path, []byte(text.String()))
			v, err := cxx.DiscoverFunctionTemplates(context.Background(), path, "bounded", cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if count == 256 {
				if err != nil || len(v) != 256 {
					t.Fatal(len(v), err)
				}
			} else if err == nil || v != nil {
				t.Fatal(v, err)
			}
		})
	}
}

func TestTemplateArgumentJSON(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprint(empty), func(t *testing.T) {
			f := cxxexample.TemplateFunctions()[2]
			if !empty {
				f.TemplateArgs = nil
			}
			data, err := json.Marshal(f)
			if err != nil {
				t.Fatal(err)
			}
			var restored cxx.Function
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			if (restored.TemplateArgs != nil) != empty {
				t.Fatal("template selection lost in JSON", string(data))
			}
			facade, err := cxx.GenerateFunctions("templates.hpp", []cxx.Function{restored})
			if err != nil {
				t.Fatal(err)
			}
			selector := "&templates::choose"
			if empty {
				selector += "<>"
			}
			selector += ");"
			if !strings.Contains(string(facade.CXXSource), selector) {
				t.Fatal("wrong overload set", string(facade.CXXSource))
			}
		})
	}
}

func TestNativeFunctionTemplateParameterBounds(t *testing.T) {
	for _, count := range []int{64, 65} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var s strings.Builder
			s.WriteString("namespace bounded { template<")
			for i := 0; i < count; i++ {
				if i != 0 {
					s.WriteString(",")
				}
				fmt.Fprintf(&s, "class T%d", i)
			}
			s.WriteString("> int f(int); }\n")
			header := filepath.Join(t.TempDir(), "parameters.hpp")
			write(t, header, []byte(s.String()))
			v, err := cxx.DiscoverFunctionTemplates(context.Background(), header, "bounded", cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if count == 64 {
				if err != nil || len(v) != 1 || len(v[0].Parameters) != 64 {
					t.Fatal(v, err)
				}
			} else if err == nil || v != nil {
				t.Fatal(v, err)
			}
		})
	}
}
