package cxx_test

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

const headerHierarchyHeader = "testdata/header_hierarchy.hpp"

func TestHeaderHierarchyInputs(t *testing.T) {
	for _, mode := range []string{"empty", "keyword", "injected", "long", "pointer", "nil_context", "cancelled", "missing_compiler"} {
		t.Run(mode, func(t *testing.T) {
			root := "RootAlias"
			ctx := context.Background()
			switch mode {
			case "empty":
				root = ""
			case "keyword":
				root = "class"
			case "injected":
				root = "Root;bad"
			case "long":
				root = strings.Repeat("a", 257)
			case "pointer":
				root = "Root*"
			case "nil_context":
				ctx = nil
			case "cancelled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			got, err := cxx.DiscoverHeaderHierarchy(ctx, headerHierarchyHeader, root, cxx.DiscoveryOptions{Compiler: filepath.Join(t.TempDir(), "missing")})
			if got != nil || err == nil || mode == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestNativeHeaderHierarchyAcrossTargets(t *testing.T) {
	for _, target := range freeTargets {
		t.Run(target, func(t *testing.T) {
			got, err := cxx.DiscoverHeaderHierarchy(context.Background(), headerHierarchyHeader, "Target", cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding"}})
			if err != nil {
				t.Fatal(err)
			}
			branch := "branches::Right"
			if strings.HasPrefix(target, "i686-") {
				branch = "branches::Left"
			}
			want := []string{"Target", "header_types::Counter", "GlobalCounter", branch}
			if !reflect.DeepEqual(hierarchyNames(got), want) || len(got.Conversions) != 5 {
				t.Fatal("target-dependent global/header closure", got)
			}
		})
	}
}

func TestNativeHeaderHierarchyForms(t *testing.T) {
	for _, tc := range []struct {
		name, root  string
		classes     []string
		conversions int
		private     bool
	}{
		{name: "diamond", root: "RootAlias", classes: []string{"header_types::Counter", "GlobalCounter", "GlobalPad", "branches::Left", "branches::Right"}, conversions: 9},
		{name: "ambiguous", root: "Ambiguous", classes: []string{"PlainLeft", "PlainRight", "header_types::Counter", "GlobalCounter"}, conversions: 6},
		{name: "template_alias", root: "Templated", classes: []string{"IntBox"}, conversions: 1},
		{name: "nested", root: "NestedBase", classes: []string{"Nested::Base"}, conversions: 1},
		{name: "conditional", root: "Conditional", classes: []string{"header_types::Counter", "GlobalCounter", "branches::Left"}, conversions: 5},
		{name: "private", root: "Conditional", private: true},
		{name: "leaf", root: "Leaf"},
		{name: "lambda_alias", root: "Lambda"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := cxx.DiscoveryOptions{Compiler: cppTool(t)}
			if tc.private {
				opts.Flags = []string{"-DHEADER_HIERARCHY_PRIVATE"}
			}
			got, err := cxx.DiscoverHeaderHierarchy(context.Background(), headerHierarchyHeader, tc.root, opts)
			if err != nil {
				t.Fatal(err)
			}
			want := append([]string{tc.root}, tc.classes...)
			if !reflect.DeepEqual(hierarchyNames(got), want) || len(got.Conversions) != tc.conversions {
				t.Fatal(got, want)
			}
			if tc.name == "diamond" && len(got.Classes[0].Bases) != 5 {
				t.Fatal("non-public bases lost", got.Classes[0])
			}
			if tc.private && (len(got.Classes[0].Bases) != 1 || got.Classes[0].Bases[0].Access != "private") {
				t.Fatal(got.Classes[0])
			}
			for _, r := range got.Conversions {
				if r.Derived == tc.root && tc.name == "ambiguous" && (r.Base == "GlobalCounter" || r.Base == "header_types::Counter") {
					t.Fatal("ambiguous root conversion", r)
				}
				if r.Derived == "header_types::Counter" && r.Base == "GlobalCounter" || r.Derived == "GlobalCounter" && r.Base == "header_types::Counter" {
					t.Fatal("canonical self alias conversion", r)
				}
			}
		})
	}
	for _, root := range []string{"Missing", "Box", "GlobalPad::Missing"} {
		t.Run("missing_"+root, func(t *testing.T) {
			if got, err := cxx.DiscoverHeaderHierarchy(context.Background(), headerHierarchyHeader, root, cxx.DiscoveryOptions{Compiler: cppTool(t)}); err == nil || got != nil {
				t.Fatal(got, err)
			}
		})
	}
}

func TestHeaderHierarchyCatalogBounds(t *testing.T) {
	for _, count := range []int{256, 257} {
		t.Run(fmt.Sprintf("candidates_%d", count), func(t *testing.T) {
			var source strings.Builder
			source.WriteString("struct Root {};\n")
			for i := 1; i < count; i++ {
				fmt.Fprintf(&source, "struct C%d {};\n", i)
			}
			header := filepath.Join(t.TempDir(), "catalog.hpp")
			write(t, header, []byte(source.String()))
			got, err := cxx.DiscoverHeaderHierarchy(context.Background(), header, "Root", cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if count == 256 {
				if err != nil || got == nil || len(got.Classes) != 1 {
					t.Fatal(got, err)
				}
			} else if err == nil || got != nil {
				t.Fatal(got, err)
			}
		})
	}
	for _, kind := range []string{"functions", "templates"} {
		t.Run("independent_"+kind, func(t *testing.T) {
			var source strings.Builder
			source.WriteString("struct Base {}; struct Root:Base {};\n")
			for i := 0; i < 257; i++ {
				if kind == "templates" {
					fmt.Fprint(&source, "template<class T> ")
				}
				fmt.Fprintf(&source, "int f%d(int);\n", i)
			}
			header := filepath.Join(t.TempDir(), "catalog.hpp")
			write(t, header, []byte(source.String()))
			opts := cxx.DiscoveryOptions{Compiler: cppTool(t)}
			if got, err := cxx.DiscoverHeader(context.Background(), header, opts); err == nil || got != nil {
				t.Fatal("broader catalog budget not enforced", got, err)
			}
			got, err := cxx.DiscoverHeaderHierarchy(context.Background(), header, "Root", opts)
			if err != nil || got == nil || !reflect.DeepEqual(hierarchyNames(got), []string{"Root", "Base"}) || len(got.Conversions) != 1 {
				t.Fatal("unrelated function count limited class closure", got, err)
			}
		})
	}
}

func TestHeaderHierarchyFacadeAcrossTargets(t *testing.T) {
	header, err := filepath.Abs(headerHierarchyHeader)
	if err != nil {
		t.Fatal(err)
	}
	f, err := cxxexample.DiscoverHeaderHierarchy(context.Background(), filepath.ToSlash(header), cxx.DiscoveryOptions{Compiler: cppTool(t)})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "facade.cpp")
	write(t, source, f.CXXSource)
	for _, target := range freeTargets {
		t.Run(target, func(t *testing.T) {
			if out, err := exec.Command(cppTool(t), "--target="+target, "-ffreestanding", "-std=c++17", "-fsyntax-only", source).CombinedOutput(); err != nil {
				t.Fatalf("header hierarchy facade: %v\n%s", err, out)
			}
		})
	}
}
