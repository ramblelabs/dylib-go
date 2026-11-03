package cxx_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func TestNativeDirectBaseMetadataAcrossTargets(t *testing.T) {
	want := []cxx.BaseDeclaration{{NativeType: "Padding", Access: "public"}, {NativeType: "Counter", Access: "public"}, {NativeType: "Virtual", Access: "public", Virtual: true}, {NativeType: "Hidden", Access: "protected"}, {NativeType: "Secret", Access: "private"}}
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			opts := cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding"}}
			for _, name := range []string{"Selected", "Alias", "Target"} {
				t.Run(name, func(t *testing.T) {
					got, err := cxx.DiscoverMembers(context.Background(), "testdata/direct_bases.hpp", "direct_bases::"+name, opts)
					if err != nil {
						t.Fatal(err)
					}
					expected := want
					if name == "Target" {
						base := "Virtual"
						if strings.HasPrefix(target, "i686-") {
							base = "Counter"
						}
						expected = []cxx.BaseDeclaration{{NativeType: base, Access: "public"}}
					}
					if got.Name != "direct_bases::"+name || !reflect.DeepEqual(got.Bases, expected) {
						t.Fatalf("direct base order/type/access/virtual/identity: %+v, want %+v", got, expected)
					}
				})
			}
		})
	}
}

func TestNativeDirectBaseMetadataForms(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bases []cxx.BaseDeclaration
	}{
		{"PrivateDefault", []cxx.BaseDeclaration{{NativeType: "Counter", Access: "private"}}},
		{"Protected", []cxx.BaseDeclaration{{NativeType: "Counter", Access: "protected"}}},
		{"Aliased", []cxx.BaseDeclaration{{NativeType: "CounterAlias", Access: "public"}}},
		{"Ambiguous", []cxx.BaseDeclaration{{NativeType: "Counter", Access: "public"}, {NativeType: "Left", Access: "public"}}},
		{"Diamond", []cxx.BaseDeclaration{{NativeType: "VLeft", Access: "public"}, {NativeType: "VRight", Access: "public"}}},
		{"VLeft", []cxx.BaseDeclaration{{NativeType: "Counter", Access: "public", Virtual: true}}},
		{"Templated", []cxx.BaseDeclaration{{NativeType: "IntBox", Access: "public"}}},
		{"NestedBase", []cxx.BaseDeclaration{{NativeType: "Nested::Base", Access: "public"}}},
		{"Counter", nil},
		{"Conditional", []cxx.BaseDeclaration{{NativeType: "Counter", Access: "public"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cxx.DiscoverMembers(context.Background(), "testdata/direct_bases.hpp", "direct_bases::"+tc.name, cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if err != nil || got == nil || !reflect.DeepEqual(got.Bases, tc.bases) {
				t.Fatalf("base metadata: %+v, %v; want %+v", got, err, tc.bases)
			}
		})
	}
	t.Run("preprocessing", func(t *testing.T) {
		got, err := cxx.DiscoverMembers(context.Background(), "testdata/direct_bases.hpp", "direct_bases::Conditional", cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: []string{"-DDYLIB_DIRECT_PRIVATE"}})
		if err != nil || got == nil || !reflect.DeepEqual(got.Bases, []cxx.BaseDeclaration{{NativeType: "Counter", Access: "private"}}) {
			t.Fatal(got, err)
		}
	})
	t.Run("conversion_eligibility", func(t *testing.T) {
		names := []string{"direct_bases::Ambiguous", "direct_bases::Counter", "direct_bases::Diamond", "direct_bases::PrivateDefault", "direct_bases::Protected"}
		got, err := cxx.DiscoverBases(context.Background(), "testdata/direct_bases.hpp", names, cxx.DiscoveryOptions{Compiler: cppTool(t)})
		want := []cxx.BaseRelation{{Derived: names[2], Base: names[1]}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("public direct metadata must not imply an unambiguous conversion", got, err)
		}
	})
}

func TestDirectBaseFacadeAcrossTargets(t *testing.T) {
	header, err := filepath.Abs("testdata/direct_bases.hpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			facade, err := cxxexample.DiscoverDirectBases(context.Background(), filepath.ToSlash(header), cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding"}})
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "direct_bases.cpp")
			write(t, source, facade.CXXSource)
			if output, err := exec.Command(cppTool(t), "--target="+target, "-ffreestanding", "-std=c++17", "-fsyntax-only", source).CombinedOutput(); err != nil {
				t.Fatalf("direct-base facade: %v\n%s", err, output)
			}
		})
	}
}
