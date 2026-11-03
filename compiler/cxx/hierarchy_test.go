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

func hierarchyResolver(_ string, base cxx.BaseDeclaration) (string, error) {
	return "hierarchy::" + base.NativeType, nil
}
func hierarchyNames(graph *cxx.ClassHierarchy) []string {
	var names []string
	for _, c := range graph.Classes {
		names = append(names, c.Name)
	}
	return names
}
func TestNativeHierarchyAcrossTargets(t *testing.T) {
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			opts := cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding"}}
			t.Run("diamond", func(t *testing.T) {
				got, err := cxx.DiscoverHierarchy(context.Background(), "testdata/hierarchy.hpp", "hierarchy::Alias", hierarchyResolver, opts)
				if err != nil {
					t.Fatal(err)
				}
				wantNames := []string{"hierarchy::Alias", "hierarchy::Padding", "hierarchy::Left", "hierarchy::Right", "hierarchy::Counter"}
				want := []cxx.BaseRelation{{wantNames[0], wantNames[1]}, {wantNames[0], wantNames[2]}, {wantNames[0], wantNames[3]}, {wantNames[0], wantNames[4]}, {wantNames[2], wantNames[4]}, {wantNames[3], wantNames[4]}}
				if !reflect.DeepEqual(hierarchyNames(got), wantNames) || !reflect.DeepEqual(got.Conversions, want) || len(got.Classes[0].Bases) != 5 || !got.Classes[2].Bases[0].Virtual || !got.Classes[3].Bases[0].Virtual {
					t.Fatalf("selected breadth-first declarations and compiler conversions: %+v", got)
				}
			})
			t.Run("target", func(t *testing.T) {
				got, err := cxx.DiscoverHierarchy(context.Background(), "testdata/hierarchy.hpp", "hierarchy::Target", hierarchyResolver, opts)
				if err != nil {
					t.Fatal(err)
				}
				middle := "Right"
				if strings.HasPrefix(target, "i686-") {
					middle = "Left"
				}
				if !reflect.DeepEqual(hierarchyNames(got), []string{"hierarchy::Target", "hierarchy::" + middle, "hierarchy::Counter"}) || len(got.Conversions) != 3 {
					t.Fatal("target-dependent hierarchy", got)
				}
			})
		})
	}
}

func TestNativeHierarchyForms(t *testing.T) {
	opts := cxx.DiscoveryOptions{Compiler: cppTool(t)}
	for _, tc := range []struct {
		name        string
		names       []string
		conversions int
	}{
		{"Ambiguous", []string{"Ambiguous", "PlainLeft", "PlainRight", "Counter"}, 4},
		{"Aliased", []string{"Aliased", "CounterAlias"}, 1},
		{"Templated", []string{"Templated", "IntBox"}, 1},
		{"NestedBase", []string{"NestedBase", "Nested::Base"}, 1},
		{"Leaf", []string{"Leaf"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cxx.DiscoverHierarchy(context.Background(), "testdata/hierarchy.hpp", "hierarchy::"+tc.name, hierarchyResolver, opts)
			if err != nil {
				t.Fatal(err)
			}
			want := append([]string(nil), tc.names...)
			for i := range want {
				want[i] = "hierarchy::" + want[i]
			}
			if !reflect.DeepEqual(hierarchyNames(got), want) || len(got.Conversions) != tc.conversions {
				t.Fatal("hierarchy forms", got)
			}
			if tc.name == "Ambiguous" {
				for _, c := range got.Conversions {
					if c.Derived == want[0] && c.Base == want[3] {
						t.Fatal("ambiguous base conversion published", c)
					}
				}
			}
		})
	}
	t.Run("skip", func(t *testing.T) {
		calls := 0
		got, err := cxx.DiscoverHierarchy(context.Background(), "testdata/hierarchy.hpp", "hierarchy::Root", func(_ string, b cxx.BaseDeclaration) (string, error) {
			calls++
			if b.Access != "public" {
				t.Fatal("non-public base passed to resolver")
			}
			return "", nil
		}, opts)
		if err != nil || got == nil || calls != 3 || len(got.Classes) != 1 || len(got.Classes[0].Bases) != 5 || len(got.Conversions) != 0 {
			t.Fatal("explicit skipped branches", got, err, calls)
		}
	})
	t.Run("preprocessing", func(t *testing.T) {
		local := opts
		local.Flags = []string{"-DDYLIB_HIERARCHY_PRIVATE"}
		got, err := cxx.DiscoverHierarchy(context.Background(), "testdata/hierarchy.hpp", "hierarchy::Conditional", func(_ string, _ cxx.BaseDeclaration) (string, error) {
			t.Fatal("private base reached resolver")
			return "", nil
		}, local)
		if err != nil || got == nil || len(got.Classes) != 1 || len(got.Classes[0].Bases) != 1 || got.Classes[0].Bases[0].Access != "private" {
			t.Fatal(got, err)
		}
	})
	t.Run("flags_snapshot", func(t *testing.T) {
		header := filepath.Join(t.TempDir(), "flags.hpp")
		write(t, header, []byte("struct Base {}; struct Other {};\n#if GRAPH_EXTRA\nstruct Middle : Base {};\n#else\nstruct Middle : Other {};\n#endif\nstruct Root : Middle {};\n"))
		flags := []string{"-DGRAPH_EXTRA=1"}
		local := opts
		local.Flags = flags
		got, err := cxx.DiscoverHierarchy(context.Background(), header, "Root", func(_ string, b cxx.BaseDeclaration) (string, error) {
			flags[0] = "-DGRAPH_EXTRA=0"
			return b.NativeType, nil
		}, local)
		if err != nil || got == nil || !reflect.DeepEqual(hierarchyNames(got), []string{"Root", "Middle", "Base"}) {
			t.Fatal("caller flag mutation changed later probes", got, err)
		}
	})
}

func TestNativeHierarchyRejectsResolverFailures(t *testing.T) {
	opts := cxx.DiscoveryOptions{Compiler: cppTool(t)}
	for _, name := range []string{"hierarchy::Leaf", "hierarchy::Root", "hierarchy::Alias", "hierarchy::Missing", "hierarchy::Box<int>", "int; injected", strings.Repeat("a", 257)} {
		t.Run(name, func(t *testing.T) {
			got, err := cxx.DiscoverHierarchy(context.Background(), "testdata/hierarchy.hpp", "hierarchy::Root", func(_ string, _ cxx.BaseDeclaration) (string, error) { return name, nil }, opts)
			if err == nil || got != nil {
				t.Fatal("invalid/unrelated/self selection published", got, err)
			}
		})
	}
	t.Run("resolver_error", func(t *testing.T) {
		failure := errors.New("resolver failed")
		got, err := cxx.DiscoverHierarchy(context.Background(), "testdata/hierarchy.hpp", "hierarchy::Root", func(_ string, _ cxx.BaseDeclaration) (string, error) { return "", failure }, opts)
		if got != nil || !errors.Is(err, failure) {
			t.Fatal(got, err)
		}
	})
	t.Run("cancelled_callback", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		got, err := cxx.DiscoverHierarchy(ctx, "testdata/hierarchy.hpp", "hierarchy::Root", func(_ string, _ cxx.BaseDeclaration) (string, error) { cancel(); return "hierarchy::Padding", nil }, opts)
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(got, err)
		}
	})
}

func TestHierarchyRejectsInvalidInputs(t *testing.T) {
	opts := cxx.DiscoveryOptions{Compiler: filepath.Join(t.TempDir(), "missing")}
	for _, name := range []string{"", "class", "A*", "A; injected", strings.Repeat("a", 257)} {
		if got, err := cxx.DiscoverHierarchy(context.Background(), "testdata/hierarchy.hpp", name, hierarchyResolver, opts); err == nil || got != nil {
			t.Fatal(name, got, err)
		}
	}
	if got, err := cxx.DiscoverHierarchy(context.Background(), "testdata/hierarchy.hpp", "hierarchy::Root", nil, opts); err == nil || got != nil {
		t.Fatal(got, err)
	}
	if got, err := cxx.DiscoverHierarchy(nil, "testdata/hierarchy.hpp", "hierarchy::Root", hierarchyResolver, opts); err == nil || got != nil {
		t.Fatal(got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := cxx.DiscoverHierarchy(ctx, "testdata/hierarchy.hpp", "hierarchy::Root", hierarchyResolver, opts); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(got, err)
	}
}

func TestNativeHierarchyBudgets(t *testing.T) {
	var source strings.Builder
	for i := 0; i < 33; i++ {
		if i == 0 {
			fmt.Fprintln(&source, "struct C0 {};")
		} else {
			fmt.Fprintf(&source, "struct C%d : C%d {};\n", i, i-1)
		}
	}
	header := filepath.Join(t.TempDir(), "chain.hpp")
	write(t, header, []byte(source.String()))
	opts := cxx.DiscoveryOptions{Compiler: cppTool(t)}
	resolve := func(_ string, b cxx.BaseDeclaration) (string, error) { return b.NativeType, nil }
	t.Run("names_at_limit", func(t *testing.T) {
		got, err := cxx.DiscoverHierarchy(context.Background(), header, "C31", resolve, opts)
		if err != nil || got == nil || len(got.Classes) != 32 || len(got.Conversions) != 496 {
			t.Fatal("maximum hierarchy", got, err)
		}
	})
	t.Run("names_exceeded", func(t *testing.T) {
		got, err := cxx.DiscoverHierarchy(context.Background(), header, "C32", resolve, opts)
		if err == nil || got != nil || !strings.Contains(err.Error(), "32 selected names") {
			t.Fatal(got, err)
		}
	})
	t.Run("bases_exceeded", func(t *testing.T) {
		var text strings.Builder
		for i := 0; i < 257; i++ {
			fmt.Fprintf(&text, "struct B%d {};\n", i)
		}
		fmt.Fprint(&text, "struct Root : ")
		for i := 0; i < 257; i++ {
			if i != 0 {
				fmt.Fprint(&text, ",")
			}
			fmt.Fprintf(&text, "B%d", i)
		}
		fmt.Fprintln(&text, " {};")
		file := filepath.Join(t.TempDir(), "bases.hpp")
		write(t, file, []byte(text.String()))
		got, err := cxx.DiscoverHierarchy(context.Background(), file, "Root", func(_ string, _ cxx.BaseDeclaration) (string, error) {
			t.Fatal("base budget must be checked before callbacks")
			return "", nil
		}, opts)
		if err == nil || got != nil || !strings.Contains(err.Error(), "256 direct base declarations") {
			t.Fatal(got, err)
		}
	})
}

func TestHierarchyFacadeAcrossTargets(t *testing.T) {
	header, err := filepath.Abs("testdata/hierarchy.hpp")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := cxxexample.DiscoverHierarchy(context.Background(), filepath.ToSlash(header), cxx.DiscoveryOptions{Compiler: cppTool(t)})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "hierarchy.cpp")
	write(t, source, facade.CXXSource)
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			if output, err := exec.Command(cppTool(t), "--target="+target, "-ffreestanding", "-std=c++17", "-fsyntax-only", source).CombinedOutput(); err != nil {
				t.Fatalf("hierarchy facade: %v\n%s", err, output)
			}
		})
	}
}
