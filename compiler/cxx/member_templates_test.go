package cxx_test

import (
	"context"
	"fmt"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func TestNativeDiscoverMemberTemplatesAcrossTargets(t *testing.T) {
	header, err := filepath.Abs("testdata/member_templates.hpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range freeTargets {
		t.Run(target, func(t *testing.T) {
			for _, name := range []string{"member_templates::Counter", "member_templates::Alias", "member_templates::Selected", "member_templates::Lambda", "member_templates::Stored", "member_templates::Unnamed", "member_templates::Empty", "member_templates::Counter::Nested"} {
				t.Run(name, func(t *testing.T) {
					got, err := cxx.DiscoverMemberTemplates(context.Background(), header, name, cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding"}})
					if err != nil {
						t.Fatal(err)
					}
					switch name {
					case "member_templates::Counter", "member_templates::Alias":
						if len(got) != 9 {
							t.Fatal("member templates", got)
						}
						want := []string{"", "add", "add", "twice", "", "deleted", "boxed", "count", "sum"}
						for i, d := range got {
							if d.Callable.NativeType == "" || len(d.Parameters) == 0 {
								t.Fatal(d)
							}
							if want[i] != "" && d.Callable.Name != want[i] {
								t.Fatal("order", i, d)
							}
						}
						if got[0].Callable.Kind != cxx.ConstructorDeclaration || got[4].Callable.Kind != cxx.ConversionDeclaration || !got[3].Callable.Static || !got[5].Callable.Deleted || !got[8].Callable.Variadic {
							t.Fatal("native kinds/flags", got)
						}
						if !got[1].Parameters[1].HasDefault || !got[7].Parameters[0].Pack || got[6].Parameters[1].Kind != cxx.TemplateTemplateParameter || !got[6].Parameters[1].HasDefault || len(got[6].Parameters[1].Parameters) != 1 {
							t.Fatal("parameter metadata", got)
						}
						if !strings.Contains(got[1].Callable.NativeType, "const &") || !strings.Contains(got[2].Callable.NativeType, "&&") {
							t.Fatal("receiver qualifiers", got)
						}
					case "member_templates::Selected":
						if len(got) != 2 || got[0].Callable.Kind != cxx.ConstructorDeclaration || got[1].Callable.Name != "add" {
							t.Fatal(got)
						}
					case "member_templates::Lambda", "member_templates::Stored":
						if len(got) != 1 || got[0].Callable.Name != "operator()" || len(got[0].Parameters) != 1 || got[0].Parameters[0].Name != "" || got[0].Parameters[0].Pack != (name == "member_templates::Stored") {
							t.Fatal(got)
						}
					case "member_templates::Unnamed":
						if len(got) != 1 || len(got[0].Parameters) != 2 || got[0].Parameters[0].Name != "" || got[0].Parameters[1].Name != "" || !got[0].Parameters[1].Pack {
							t.Fatal(got)
						}
					case "member_templates::Empty":
						if got != nil {
							t.Fatal(got)
						}
					case "member_templates::Counter::Nested":
						if len(got) != 1 || got[0].Callable.Name != "nested" {
							t.Fatal(got)
						}
					}
				})
			}
		})
	}
}

func TestMemberTemplateDiscoveryBounds(t *testing.T) {
	header, err := filepath.Abs("testdata/member_templates.hpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "::member_templates::Counter", "member_templates;", strings.Repeat("x", 257), "member_templates::Missing", "member_templates::Box", "int32_t"} {
		t.Run(fmt.Sprintf("name_%q", name), func(t *testing.T) {
			if v, err := cxx.DiscoverMemberTemplates(context.Background(), header, name, cxx.DiscoveryOptions{Compiler: cppTool(t)}); err == nil || v != nil {
				t.Fatal(v, err)
			}
		})
	}
	t.Run("nil_context", func(t *testing.T) {
		if v, err := cxx.DiscoverMemberTemplates(nil, header, "member_templates::Counter", cxx.DiscoveryOptions{}); err == nil || v != nil {
			t.Fatal(v, err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if v, err := cxx.DiscoverMemberTemplates(ctx, header, "member_templates::Counter", cxx.DiscoveryOptions{}); err != context.Canceled || v != nil {
			t.Fatal(v, err)
		}
	})
	for _, count := range []int{256, 257} {
		t.Run(fmt.Sprintf("members_%d", count), func(t *testing.T) {
			var source strings.Builder
			source.WriteString("struct B {\n")
			for i := 0; i < count; i++ {
				fmt.Fprintf(&source, "template<class T> int f%d(T);\n", i)
			}
			source.WriteString("};\n")
			path := filepath.Join(t.TempDir(), "bounded.hpp")
			write(t, path, []byte(source.String()))
			v, err := cxx.DiscoverMemberTemplates(context.Background(), path, "B", cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if count == 256 {
				if err != nil || len(v) != 256 {
					t.Fatal(len(v), err)
				}
			} else if err == nil || v != nil {
				t.Fatal(v, err)
			}
		})
	}
	t.Run("ordinary_unchanged", func(t *testing.T) {
		v, err := cxx.DiscoverMembers(context.Background(), header, "member_templates::Alias", cxx.DiscoveryOptions{Compiler: cppTool(t)})
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range v.Callables {
			if d.Name == "add" || d.Name == "count" || d.Name == "twice" || d.Kind == cxx.ConversionDeclaration {
				t.Fatal("ordinary discovery included template", d)
			}
		}
	})
}

func TestMemberTemplateFacadeAcrossTargets(t *testing.T) {
	header, err := filepath.Abs("testdata/member_templates.hpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range freeTargets {
		t.Run(target, func(t *testing.T) {
			classes, err := cxxexample.DiscoverTemplateClasses(context.Background(), header, cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding"}})
			if err != nil {
				t.Fatal(err)
			}
			facade, err := cxx.Generate(filepath.ToSlash(header), classes)
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "facade.cpp")
			write(t, source, facade.CXXSource)
			if output, err := exec.Command(cppTool(t), "--target="+target, "-ffreestanding", "-std=c++17", "-fsyntax-only", source).CombinedOutput(); err != nil {
				t.Fatalf("member template facade: %v\n%s", err, output)
			}
		})
	}
}

func TestNativeCompilerRejectsMemberTemplateMismatch(t *testing.T) {
	header, err := filepath.Abs("testdata/member_templates.hpp")
	if err != nil {
		t.Fatal(err)
	}
	classes, err := cxxexample.DiscoverTemplateClasses(context.Background(), header, cxx.DiscoveryOptions{Compiler: cppTool(t)})
	if err != nil {
		t.Fatal(err)
	}
	for name, patch := range map[string]func(*cxx.Method){
		"private": func(m *cxx.Method) {
			m.Name = "hidden"
			m.Const = false
			m.Ref = cxx.RefNone
			m.TemplateArgs = []string{"int32_t"}
		},
		"protected": func(m *cxx.Method) {
			m.Name = "protected_member"
			m.Const = false
			m.Ref = cxx.RefNone
			m.TemplateArgs = []string{"int32_t"}
		},
		"deleted": func(m *cxx.Method) {
			m.Name = "deleted"
			m.Const = false
			m.Ref = cxx.RefNone
			m.TemplateArgs = []string{"int32_t"}
		},
		"wrong_type":       func(m *cxx.Method) { m.TemplateArgs = []string{"double", "2"} },
		"wrong_qualifier":  func(m *cxx.Method) { m.Const = false; m.Ref = cxx.RefLValue },
		"wrong_static":     func(m *cxx.Method) { m.Static = true; m.Const = false; m.Ref = cxx.RefNone },
		"excess_arguments": func(m *cxx.Method) { m.TemplateArgs = []string{"int32_t", "2", "3"} },
	} {
		t.Run(name, func(t *testing.T) {
			m := classes[0].Methods[0]
			patch(&m)
			selected := cxx.Class{Name: classes[0].Name, Methods: []cxx.Method{m}}
			facade, err := cxx.Generate(filepath.ToSlash(header), []cxx.Class{selected})
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "bad.cpp")
			write(t, source, facade.CXXSource)
			if _, err := exec.Command(cppTool(t), "-std=c++17", "-fsyntax-only", source).CombinedOutput(); err == nil {
				t.Fatal("invalid member template selection accepted")
			}
		})
	}
}
