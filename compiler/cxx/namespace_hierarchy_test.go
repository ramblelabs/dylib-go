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

const namespaceHierarchyHeader = "testdata/namespace_hierarchy.hpp"

func TestNamespaceHierarchyInputs(t *testing.T) {
	for _, mode := range []string{"empty_root", "keyword_root", "injected_root", "long_root", "pointer_root", "no_namespaces", "too_many_namespaces", "empty_namespace", "keyword_namespace", "nul_namespace", "duplicate_namespace", "long_namespace", "nil_context", "cancelled", "missing_compiler"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			root, namespaces := "hierarchy_api::Root", []string{"hierarchy_catalog"}
			switch mode {
			case "empty_root":
				root = ""
			case "keyword_root":
				root = "class"
			case "injected_root":
				root = "Root; injected"
			case "long_root":
				root = strings.Repeat("a", 257)
			case "pointer_root":
				root = "Root*"
			case "no_namespaces":
				namespaces = nil
			case "too_many_namespaces":
				namespaces = make([]string, 17)
			case "empty_namespace":
				namespaces[0] = ""
			case "keyword_namespace":
				namespaces[0] = "namespace"
			case "nul_namespace":
				namespaces[0] = "bad\x00namespace"
			case "duplicate_namespace":
				namespaces = append(namespaces, namespaces[0])
			case "long_namespace":
				namespaces[0] = strings.Repeat("a", 257)
			case "nil_context":
				ctx = nil
			case "cancelled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			opts := cxx.DiscoveryOptions{Compiler: filepath.Join(t.TempDir(), "missing")}
			got, err := cxx.DiscoverNamespaceHierarchy(ctx, namespaceHierarchyHeader, root, namespaces, opts)
			if got != nil || err == nil || mode == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestNativeNamespaceHierarchyAcrossTargets(t *testing.T) {
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			opts := cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding"}}
			t.Run("diamond", func(t *testing.T) {
				got, err := cxx.DiscoverNamespaceHierarchy(context.Background(), namespaceHierarchyHeader, "hierarchy_api::Root", []string{"hierarchy_catalog"}, opts)
				if err != nil {
					t.Fatal(err)
				}
				want := []string{"hierarchy_api::Root", "hierarchy_catalog::Counter", "hierarchy_catalog::CounterAlias", "hierarchy_catalog::Padding", "hierarchy_catalog::Left", "hierarchy_catalog::Right"}
				if !reflect.DeepEqual(hierarchyNames(got), want) || len(got.Conversions) != 9 || len(got.Classes[0].Bases) != 5 {
					t.Fatal("catalog aliases, declaration order and non-public metadata", got)
				}
				for _, edge := range got.Conversions {
					if strings.HasSuffix(edge.Derived, "CounterAlias") && strings.HasSuffix(edge.Base, "Counter") || strings.HasSuffix(edge.Derived, "Counter") && strings.HasSuffix(edge.Base, "CounterAlias") {
						t.Fatal("alias-self conversion published", edge)
					}
				}
			})
			t.Run("target", func(t *testing.T) {
				got, err := cxx.DiscoverNamespaceHierarchy(context.Background(), namespaceHierarchyHeader, "hierarchy_api::Target", []string{"hierarchy_catalog"}, opts)
				if err != nil {
					t.Fatal(err)
				}
				middle := "Right"
				if strings.HasPrefix(target, "i686-") {
					middle = "Left"
				}
				want := []string{"hierarchy_api::Target", "hierarchy_catalog::Counter", "hierarchy_catalog::CounterAlias", "hierarchy_catalog::" + middle}
				if !reflect.DeepEqual(hierarchyNames(got), want) || len(got.Conversions) != 5 {
					t.Fatal("target-dependent catalog closure", got)
				}
			})
		})
	}
}

func TestNativeNamespaceHierarchyForms(t *testing.T) {
	for _, tc := range []struct {
		name, root  string
		namespaces  []string
		bases       []string
		conversions int
		private     bool
	}{
		{name: "template_alias", root: "Templated", bases: []string{"IntBox"}, conversions: 1},
		{name: "nested_alias", root: "NestedBase", bases: []string{"Nested"}, conversions: 1},
		{name: "ambiguous", root: "Ambiguous", bases: []string{"PlainLeft", "PlainRight", "Counter", "CounterAlias"}, conversions: 6},
		{name: "leaf", root: "Leaf"},
		{name: "private", root: "Conditional", private: true},
		{name: "empty_catalog", root: "Root", namespaces: []string{"hierarchy_empty"}},
		{name: "namespace_alias", root: "Root", namespaces: []string{"hierarchy_catalog_alias"}, bases: []string{"Counter", "CounterAlias", "Padding", "Left", "Right"}, conversions: 9},
		{name: "overlap", root: "Root", namespaces: []string{"hierarchy_catalog", "hierarchy_catalog::extra"}, bases: []string{"Counter", "CounterAlias", "Padding", "Left", "Right"}, conversions: 9},
		{name: "catalog_scope", root: "Root", namespaces: []string{"hierarchy_api"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			namespaces := tc.namespaces
			if namespaces == nil {
				namespaces = []string{"hierarchy_catalog"}
			}
			opts := cxx.DiscoveryOptions{Compiler: cppTool(t)}
			if tc.private {
				opts.Flags = []string{"-DDYLIB_HIERARCHY_PRIVATE"}
			}
			root := "hierarchy_api::" + tc.root
			got, err := cxx.DiscoverNamespaceHierarchy(context.Background(), namespaceHierarchyHeader, root, namespaces, opts)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{root}
			for _, base := range tc.bases {
				want = append(want, namespaces[0]+"::"+base)
			}
			if !reflect.DeepEqual(hierarchyNames(got), want) || len(got.Conversions) != tc.conversions {
				t.Fatal("catalog closure", got)
			}
			if tc.name == "ambiguous" {
				for _, edge := range got.Conversions {
					if edge.Derived == root && strings.Contains(edge.Base, "Counter") {
						t.Fatal("ambiguous root conversion published", edge)
					}
				}
			}
			if tc.private && (len(got.Classes[0].Bases) != 1 || got.Classes[0].Bases[0].Access != "private") {
				t.Fatal("private base metadata lost", got)
			}
		})
	}
	t.Run("ordered_alias_catalogs", func(t *testing.T) {
		got, err := cxx.DiscoverNamespaceHierarchy(context.Background(), namespaceHierarchyHeader, "hierarchy_api::Root", []string{"hierarchy_catalog_alias", "hierarchy_catalog"}, cxx.DiscoveryOptions{Compiler: cppTool(t)})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"hierarchy_api::Root"}
		for _, prefix := range []string{"hierarchy_catalog_alias", "hierarchy_catalog"} {
			for _, base := range []string{"Counter", "CounterAlias", "Padding", "Left", "Right"} {
				want = append(want, prefix+"::"+base)
			}
		}
		if !reflect.DeepEqual(hierarchyNames(got), want) || len(got.Conversions) != 26 {
			t.Fatal("namespace order and distinct aliases", got)
		}
	})
	t.Run("missing_namespace", func(t *testing.T) {
		got, err := cxx.DiscoverNamespaceHierarchy(context.Background(), namespaceHierarchyHeader, "hierarchy_api::Root", []string{"hierarchy_catalog", "not_installed"}, cxx.DiscoveryOptions{Compiler: cppTool(t)})
		if got != nil || err == nil {
			t.Fatal("failed catalog published a partial graph", got, err)
		}
	})
}

func TestNativeNamespaceHierarchyBudgets(t *testing.T) {
	opts := cxx.DiscoveryOptions{Compiler: cppTool(t)}
	var chain strings.Builder
	fmt.Fprintln(&chain, "namespace chain { struct C0 {};")
	for i := 1; i < 33; i++ {
		fmt.Fprintf(&chain, "struct C%d : C%d {};\n", i, i-1)
	}
	fmt.Fprintln(&chain, "}")
	header := filepath.Join(t.TempDir(), "chain.hpp")
	write(t, header, []byte(chain.String()))
	t.Run("selected_at_limit", func(t *testing.T) {
		got, err := cxx.DiscoverNamespaceHierarchy(context.Background(), header, "chain::C31", []string{"chain"}, opts)
		if err != nil || got == nil || len(got.Classes) != 32 || len(got.Conversions) != 496 || got.Classes[1].Name != "chain::C0" || got.Classes[31].Name != "chain::C30" {
			t.Fatal(got, err)
		}
	})
	t.Run("selected_exceeded", func(t *testing.T) {
		got, err := cxx.DiscoverNamespaceHierarchy(context.Background(), header, "chain::C32", []string{"chain"}, opts)
		if got != nil || err == nil || !strings.Contains(err.Error(), "32 selected names") {
			t.Fatal(got, err)
		}
	})
	var catalogs strings.Builder
	fmt.Fprintln(&catalogs, "struct Root {}; namespace one {")
	for i := 0; i < 128; i++ {
		fmt.Fprintf(&catalogs, "struct C%d {};\n", i)
	}
	fmt.Fprintln(&catalogs, "} namespace two {")
	for i := 0; i < 128; i++ {
		fmt.Fprintf(&catalogs, "struct C%d {};\n", i)
	}
	fmt.Fprintln(&catalogs, "} namespace three { struct Extra {}; }")
	catalogHeader := filepath.Join(t.TempDir(), "catalogs.hpp")
	write(t, catalogHeader, []byte(catalogs.String()))
	t.Run("candidates_at_limit", func(t *testing.T) {
		got, err := cxx.DiscoverNamespaceHierarchy(context.Background(), catalogHeader, "Root", []string{"one", "two"}, opts)
		if err != nil || got == nil || len(got.Classes) != 1 || len(got.Conversions) != 0 {
			t.Fatal(got, err)
		}
	})
	t.Run("candidates_exceeded", func(t *testing.T) {
		got, err := cxx.DiscoverNamespaceHierarchy(context.Background(), catalogHeader, "Root", []string{"one", "two", "three"}, opts)
		if got != nil || err == nil || !strings.Contains(err.Error(), "256 candidate classes") {
			t.Fatal(got, err)
		}
	})
	var baseSource strings.Builder
	fmt.Fprintln(&baseSource, "namespace empty {}")
	for i := 0; i < 257; i++ {
		fmt.Fprintf(&baseSource, "struct B%d {};\n", i)
	}
	for _, n := range []int{256, 257} {
		fmt.Fprintf(&baseSource, "class Root%d : ", n)
		for i := 0; i < n; i++ {
			if i != 0 {
				fmt.Fprint(&baseSource, ",")
			}
			fmt.Fprintf(&baseSource, "B%d", i)
		}
		fmt.Fprintln(&baseSource, " {};")
	}
	baseHeader := filepath.Join(t.TempDir(), "bases.hpp")
	write(t, baseHeader, []byte(baseSource.String()))
	for _, n := range []int{256, 257} {
		t.Run(fmt.Sprintf("direct_bases_%d", n), func(t *testing.T) {
			got, err := cxx.DiscoverNamespaceHierarchy(context.Background(), baseHeader, fmt.Sprintf("Root%d", n), []string{"empty"}, opts)
			if n == 256 {
				if err != nil || got == nil || len(got.Classes) != 1 || len(got.Classes[0].Bases) != 256 {
					t.Fatal(got, err)
				}
			} else if got != nil || err == nil || !strings.Contains(err.Error(), "256 direct base declarations") {
				t.Fatal(got, err)
			}
		})
	}
	t.Run("namespaces_at_limit", func(t *testing.T) {
		var source strings.Builder
		fmt.Fprintln(&source, "struct Root {};")
		var namespaces []string
		for i := 0; i < 16; i++ {
			name := fmt.Sprintf("n%d", i)
			namespaces = append(namespaces, name)
			fmt.Fprintf(&source, "namespace %s {}\n", name)
		}
		path := filepath.Join(t.TempDir(), "namespaces.hpp")
		write(t, path, []byte(source.String()))
		got, err := cxx.DiscoverNamespaceHierarchy(context.Background(), path, "Root", namespaces, opts)
		if err != nil || got == nil || len(got.Classes) != 1 {
			t.Fatal(got, err)
		}
	})
}

func TestNamespaceHierarchyFacadeAcrossTargets(t *testing.T) {
	header, err := filepath.Abs(namespaceHierarchyHeader)
	if err != nil {
		t.Fatal(err)
	}
	facade, err := cxxexample.DiscoverNamespaceHierarchy(context.Background(), filepath.ToSlash(header), cxx.DiscoveryOptions{Compiler: cppTool(t)})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "namespace_hierarchy.cpp")
	write(t, source, facade.CXXSource)
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			if output, err := exec.Command(cppTool(t), "--target="+target, "-ffreestanding", "-std=c++17", "-fsyntax-only", source).CombinedOutput(); err != nil {
				t.Fatalf("namespace hierarchy facade: %v\n%s", err, output)
			}
		})
	}
}
