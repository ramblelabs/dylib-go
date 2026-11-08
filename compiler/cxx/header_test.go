package cxx_test

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func TestNativeDiscoverHeaderAcrossTargets(t *testing.T) {
	header, err := filepath.Abs("testdata/header.hpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range freeTargets {
		t.Run(target, func(t *testing.T) {
			for _, feature := range []bool{false, true} {
				t.Run(fmt.Sprint(feature), func(t *testing.T) {
					flags := []string{"-ffreestanding"}
					if feature {
						flags = append(flags, "-DHEADER_FEATURE")
					}
					got, err := cxx.DiscoverHeader(context.Background(), header, cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: flags})
					if err != nil {
						t.Fatal(err)
					}
					classes := []string{"hdr::Included", "HeaderCounter", "HeaderCounter::Nested", "CounterAlias", "GlobalLambda", "SelectedBox", "Sources", "hdr::Entry"}
					width := "Width64"
					if strings.HasPrefix(target, "i686-") {
						width = "Width32"
					}
					classes = append(classes, width)
					if feature {
						classes = append(classes, "Feature")
					}
					if !reflect.DeepEqual(got.Classes, classes) {
						t.Fatal("classes", got.Classes, classes)
					}
					functions := []string{"hdr::included", "header_factory", "global_add", "global_choose", "global_choose", "local_global", "header_c", "hdr::scale", "hdr::inlined::offset"}
					if feature {
						functions = append(functions, "feature")
					}
					var names []string
					for _, d := range got.Functions {
						names = append(names, d.Name)
						if d.NativeType == "" || strings.HasPrefix(d.Name, "::") {
							t.Fatal(d)
						}
						if d.Name == "local_global" && !d.Internal {
							t.Fatal(d)
						}
					}
					if !reflect.DeepEqual(names, functions) {
						t.Fatal("functions", names, functions)
					}
					names = nil
					for _, d := range got.FunctionTemplates {
						names = append(names, d.Function.Name)
					}
					if !reflect.DeepEqual(names, []string{"header_plus", "header_count", "hdr::identity"}) || !got.FunctionTemplates[0].Parameters[1].HasDefault || !got.FunctionTemplates[1].Parameters[0].Pack {
						t.Fatal(got.FunctionTemplates)
					}
				})
			}
		})
	}
}

func TestHeaderFacadeAcrossTargets(t *testing.T) {
	header, err := filepath.Abs("testdata/header.hpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range freeTargets {
		t.Run(target, func(t *testing.T) {
			functions, classes, err := cxxexample.HeaderSelections(context.Background(), header, cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding"}})
			if err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"functions", "classes"} {
				t.Run(kind, func(t *testing.T) {
					var f *cxx.Facade
					var err error
					if kind == "functions" {
						f, err = cxx.GenerateFunctions(filepath.ToSlash(header), functions)
					} else {
						f, err = cxx.Generate(filepath.ToSlash(header), classes)
					}
					if err != nil {
						t.Fatal(err)
					}
					source := filepath.Join(t.TempDir(), "facade.cpp")
					write(t, source, f.CXXSource)
					if out, err := exec.Command(cppTool(t), "--target="+target, "-ffreestanding", "-std=c++17", "-fsyntax-only", source).CombinedOutput(); err != nil {
						t.Fatalf("header selection: %v\n%s", err, out)
					}
				})
			}
		})
	}
}

func TestHeaderDiscoveryBounds(t *testing.T) {
	header, err := filepath.Abs("testdata/header.hpp")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("nil_context", func(t *testing.T) {
		if v, err := cxx.DiscoverHeader(nil, header, cxx.DiscoveryOptions{}); err == nil || v != nil {
			t.Fatal(v, err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if v, err := cxx.DiscoverHeader(ctx, header, cxx.DiscoveryOptions{}); err != context.Canceled || v != nil {
			t.Fatal(v, err)
		}
	})
	for _, kind := range []string{"classes", "functions", "templates"} {
		t.Run(kind, func(t *testing.T) {
			for _, count := range []int{256, 257} {
				t.Run(fmt.Sprint(count), func(t *testing.T) {
					var s strings.Builder
					for i := 0; i < count; i++ {
						switch kind {
						case "classes":
							fmt.Fprintf(&s, "struct C%d {};\n", i)
						case "functions":
							fmt.Fprintf(&s, "int f%d(int);\n", i)
						case "templates":
							fmt.Fprintf(&s, "template<class T> T f%d(T);\n", i)
						}
					}
					path := filepath.Join(t.TempDir(), "bounded.hpp")
					write(t, path, []byte(s.String()))
					got, err := cxx.DiscoverHeader(context.Background(), path, cxx.DiscoveryOptions{Compiler: cppTool(t)})
					if count == 257 {
						if err == nil || got != nil {
							t.Fatal(got, err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					n := len(got.Classes)
					if kind == "functions" {
						n = len(got.Functions)
					}
					if kind == "templates" {
						n = len(got.FunctionTemplates)
					}
					if n != 256 {
						t.Fatal(n)
					}
				})
			}
		})
	}
	t.Run("name_256", func(t *testing.T) {
		name := strings.Repeat("a", 256)
		path := filepath.Join(t.TempDir(), "name.hpp")
		write(t, path, []byte("struct "+name+" {};"))
		got, err := cxx.DiscoverHeader(context.Background(), path, cxx.DiscoveryOptions{Compiler: cppTool(t)})
		if err != nil || got == nil || len(got.Classes) != 1 || got.Classes[0] != name {
			t.Fatal(got, err)
		}
	})
	for name, source := range map[string]string{
		"empty": "", "nonclass_aliases": "using A=int;union U{int x;};struct Forward;", "syntax": "struct {", "long_name": "struct " + strings.Repeat("a", 257) + " {};",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.hpp")
			write(t, path, []byte(source))
			got, err := cxx.DiscoverHeader(context.Background(), path, cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if name == "empty" || name == "nonclass_aliases" {
				if err != nil || got == nil || got.Classes != nil || got.Functions != nil || got.FunctionTemplates != nil {
					t.Fatal(got, err)
				}
			} else if err == nil || got != nil {
				t.Fatal(got, err)
			}
		})
	}
}
