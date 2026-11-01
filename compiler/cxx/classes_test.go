package cxx_test

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

func TestNativeDiscoverClassesAcrossTargets(t *testing.T) {
	header, err := filepath.Abs("testdata/classes.hpp")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"included::Imported", "Forward", "Counter", "Counter::Nested", "Counter::Self", "Access", "Access::Public", "Access::View", "Alias", "OldAlias", "Anonymous", "Union::Nested", "Selected", "Abstract", "Lambda", "child::Child", "child::class_catalog::Other", "v1::Versioned", "Reopened"}
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			for _, scope := range []string{"class_catalog", "catalog_alias", "class_catalog::child", "class_catalog::v1", "empty_catalog"} {
				t.Run(scope, func(t *testing.T) {
					got, err := cxx.DiscoverClasses(context.Background(), header, scope, cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding"}})
					if err != nil {
						t.Fatal(err)
					}
					var want []string
					if scope != "empty_catalog" {
						selected := names
						if scope == "class_catalog::child" {
							selected = []string{"Child", "class_catalog::Other"}
						} else if scope == "class_catalog::v1" {
							selected = []string{"Versioned"}
						}
						for _, name := range selected {
							want = append(want, scope+"::"+name)
						}
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("class declarations:\ngot  %v\nwant %v", got, want)
					}
				})
			}
		})
	}
	t.Run("preprocessing", func(t *testing.T) {
		got, err := cxx.DiscoverClasses(context.Background(), header, "class_catalog", cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: []string{"-DCLASS_CATALOG_EXTRA=1"}})
		if err != nil || len(got) != len(names)+1 || got[len(got)-2] != "class_catalog::Conditional" {
			t.Fatal("original header preprocessing", got, err)
		}
	})
}

func TestNativeDiscoverClassesBoundsAndErrors(t *testing.T) {
	header, err := filepath.Abs("testdata/classes.hpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"", "::class_catalog", "class_catalog;", "class_catalog::", strings.Repeat("x", 257), "missing", "class_catalog::Counter"} {
		t.Run(fmt.Sprintf("namespace_%q", scope), func(t *testing.T) {
			got, err := cxx.DiscoverClasses(context.Background(), header, scope, cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if err == nil || got != nil {
				t.Fatal("invalid namespace accepted", got, err)
			}
		})
	}
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if got, err := cxx.DiscoverClasses(ctx, header, "class_catalog", cxx.DiscoveryOptions{}); err != context.Canceled || got != nil {
			t.Fatal(got, err)
		}
	})
	t.Run("nil_context", func(t *testing.T) {
		if got, err := cxx.DiscoverClasses(nil, header, "class_catalog", cxx.DiscoveryOptions{}); err == nil || got != nil {
			t.Fatal(got, err)
		}
	})
	t.Run("candidate_limit", func(t *testing.T) {
		var source strings.Builder
		source.WriteString("namespace catalog {\n")
		for i := 0; i < 257; i++ {
			fmt.Fprintf(&source, "struct Class%d {};\n", i)
		}
		source.WriteString("}\n")
		path := filepath.Join(t.TempDir(), "bounded.hpp")
		write(t, path, []byte(source.String()))
		got, err := cxx.DiscoverClasses(context.Background(), path, "catalog", cxx.DiscoveryOptions{Compiler: cppTool(t)})
		if err == nil || got != nil || !strings.Contains(err.Error(), "256 candidate") {
			t.Fatal("partial/unbounded discovery", got, err)
		}
	})
	t.Run("candidate_boundary", func(t *testing.T) {
		var source strings.Builder
		source.WriteString("namespace catalog {\n")
		for i := 0; i < 256; i++ {
			fmt.Fprintf(&source, "struct Class%d {};\n", i)
		}
		source.WriteString("}\n")
		path := filepath.Join(t.TempDir(), "boundary.hpp")
		write(t, path, []byte(source.String()))
		got, err := cxx.DiscoverClasses(context.Background(), path, "catalog", cxx.DiscoveryOptions{Compiler: cppTool(t)})
		if err != nil || len(got) != 256 || got[255] != "catalog::Class255" {
			t.Fatal("supported discovery boundary", len(got), err)
		}
	})
}
