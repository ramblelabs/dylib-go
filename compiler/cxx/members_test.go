package cxx_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

func TestNativeDiscoverMembersAcrossTargets(t *testing.T) {
	targets := []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			opts := cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding"}}
			for _, name := range []string{"members::Counter", "members::Alias"} {
				t.Run(name, func(t *testing.T) {
					got, err := cxx.DiscoverMembers(context.Background(), "testdata/members.hpp", name, opts)
					if err != nil {
						t.Fatal(err)
					}
					var names []string
					for _, call := range got.Callables {
						names = append(names, call.Name)
					}
					want := []string{"Counter", "Counter", "~Counter", "add", "add", "twice", "qualified", "operator()", "operator bool", "variadic"}
					if got.Name != name || got.Abstract || !reflect.DeepEqual(names, want) {
						t.Fatalf("selected direct public declarations: %+v, names: %v", got, names)
					}
					c := got.Callables
					if c[0].Kind != cxx.ConstructorDeclaration || c[0].Parameters[0].Name != "initial" || c[1].Kind != cxx.ConstructorDeclaration || !c[1].Deleted || c[2].Kind != cxx.DestructorDeclaration || !c[2].Defaulted {
						t.Fatal("constructor/destructor metadata", c[:3])
					}
					nativeType := "int (int) const"
					if target == "i686-pc-windows-msvc" {
						nativeType = "int (int) __attribute__((thiscall)) const"
					}
					if c[3].NativeType != nativeType || !c[3].Parameters[0].HasDefault || c[3].Parameters[0].NativeType != "int" || len(c[4].Parameters) != 2 || !c[5].Static || !strings.Contains(c[6].NativeType, "const & noexcept") || c[8].Kind != cxx.ConversionDeclaration || !c[9].Variadic {
						t.Fatal("native callable metadata", c[3:])
					}
				})
			}
			for _, name := range []string{"members::Specialized", "members::OtherSpecialized", "members::Abstract", "members::Target"} {
				t.Run(name, func(t *testing.T) {
					got, err := cxx.DiscoverMembers(context.Background(), "testdata/members.hpp", name, opts)
					want := "read"
					if name == "members::Target" {
						want = "width64"
						if strings.HasPrefix(target, "i686-") {
							want = "width32"
						}
					}
					if err != nil || len(got.Callables) != 1 || got.Callables[0].Name != want || got.Abstract != (name == "members::Abstract") {
						t.Fatalf("selected specialization/target/abstract metadata: %+v, %v", got, err)
					}
				})
			}
		})
	}
	got, err := cxx.DiscoverMembers(context.Background(), "testdata/members.hpp", "members::Target", cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: []string{"-DDYLIB_MEMBER_EXTRA"}})
	if err != nil || len(got.Callables) != 2 || got.Callables[1].Name != "extra" {
		t.Fatal("preprocessing flags", got, err)
	}
}

func TestNativeDiscoverMembersRejectsUnsupportedTypes(t *testing.T) {
	for _, name := range []string{"ConstCounter", "Pointer", "Incomplete", "NotClass", "Scalar", "Missing"} {
		t.Run(name, func(t *testing.T) {
			got, err := cxx.DiscoverMembers(context.Background(), "testdata/members.hpp", "members::"+name, cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if err == nil || got != nil {
				t.Fatal("unsupported selected class accepted", got, err)
			}
		})
	}
}

func TestDiscoverMembersRejectsInvalidInputs(t *testing.T) {
	opts := cxx.DiscoveryOptions{Compiler: filepath.Join(t.TempDir(), "missing")}
	for _, name := range []string{"", "class", "A*", "A<int>", "A; injected", strings.Repeat("a", 257)} {
		if got, err := cxx.DiscoverMembers(context.Background(), "testdata/members.hpp", name, opts); err == nil || got != nil {
			t.Fatal(name, got, err)
		}
	}
	if got, err := cxx.DiscoverMembers(nil, "testdata/members.hpp", "members::Counter", opts); err == nil || got != nil {
		t.Fatal(got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := cxx.DiscoverMembers(ctx, "testdata/members.hpp", "members::Counter", opts); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal(got, err)
	}
	for _, header := range []string{"testdata", "testdata/missing.hpp", "testdata/members.hpp"} {
		if got, err := cxx.DiscoverMembers(context.Background(), header, "members::Counter", opts); err == nil || got != nil {
			t.Fatal(got, err)
		}
	}
}
