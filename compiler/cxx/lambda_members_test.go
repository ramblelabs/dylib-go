package cxx_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

func TestNativeDiscoverLambdaMembersAcrossTargets(t *testing.T) {
	header, err := filepath.Abs("testdata/lambda_members.hpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu", "i686-unknown-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			for _, tc := range []struct {
				name, native string
				args         int
			}{
				{"lambda_members::Captured", "auto (int, int) -> int", 2},
				{"lambda_members::WithRecordArgument", "auto (int) const -> int", 1},
				{"lambda_members::Forwarded", "auto (int, int) -> int", 2},
				{"lambda_members::Variable", "auto (int, int) -> int", 2},
				{"lambda_members::Literal", "auto (int, int) const -> int", 2},
				{"lambda_members::Member", "auto (int) const -> int", 1},
				{"other_lambda_members::Captured", "auto (double) const -> double", 1},
				{"lambda_members::Generic", "", 0},
			} {
				t.Run(tc.name, func(t *testing.T) {
					got, err := cxx.DiscoverMembers(context.Background(), header, tc.name, cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding"}})
					if err != nil || got == nil || got.Name != tc.name || got.Abstract {
						t.Fatal("lambda class declaration", got, err)
					}
					if tc.native == "" {
						if len(got.Callables) != 0 {
							t.Fatal("generic/implicit callables were inferred", got)
						}
						return
					}
					if len(got.Callables) != 1 {
						t.Fatal("lambda call operator missing or implicit members leaked", got)
					}
					call := got.Callables[0]
					want := tc.native
					if target == "i686-pc-windows-msvc" {
						end := strings.IndexByte(want, ')') + 1
						want = want[:end] + " __attribute__((thiscall))" + want[end:]
					}
					if call.Kind != cxx.MethodDeclaration || call.Name != "operator()" || call.NativeType != want || len(call.Parameters) != tc.args || call.Static || call.Variadic || call.Deleted || call.Defaulted {
						t.Fatal("lambda signature or record identity", call)
					}
				})
			}
		})
	}
	t.Run("query_limit", func(t *testing.T) {
		var source strings.Builder
		source.WriteString("inline auto base() { return []() {}; }\n")
		previous := "base"
		for i := 0; i < 10; i++ {
			name := fmt.Sprintf("forward%d", i)
			fmt.Fprintf(&source, "inline auto %s() { return %s(); }\n", name, previous)
			previous = name
		}
		fmt.Fprintf(&source, "using Selected = decltype(%s());\n", previous)
		path := filepath.Join(t.TempDir(), "bounded.hpp")
		write(t, path, []byte(source.String()))
		got, err := cxx.DiscoverMembers(context.Background(), path, "Selected", cxx.DiscoveryOptions{Compiler: cppTool(t)})
		if err == nil || got != nil || !strings.Contains(err.Error(), "8 declaration queries") {
			t.Fatal("anonymous declaration queries were not bounded", got, err)
		}
	})
}
