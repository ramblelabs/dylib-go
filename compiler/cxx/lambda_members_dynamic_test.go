//go:build libffi && cgo && (linux || darwin || windows)

package cxx_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

func TestNativeDynamicDiscoveredLambdaFacade(t *testing.T) {
	testDynamicDiscoveredLambdaFacade(t, "lambda_members::Captured")
}

func TestNativeDynamicNamespaceClassFacade(t *testing.T) {
	header, err := filepath.Abs("testdata/lambda_members.hpp")
	if err != nil {
		t.Fatal(err)
	}
	names, err := cxx.DiscoverClasses(context.Background(), header, "lambda_members", cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name == "lambda_members::Captured" {
			testDynamicDiscoveredLambdaFacade(t, name)
			return
		}
	}
	t.Fatal("captured lambda alias was not discovered", names)
}

func testDynamicDiscoveredLambdaFacade(t *testing.T, name string) {
	t.Helper()
	header, err := filepath.Abs("testdata/lambda_members.hpp")
	if err != nil {
		t.Fatal(err)
	}
	declaration, err := cxx.DiscoverMembers(context.Background(), header, name, cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
	if err != nil || declaration == nil || len(declaration.Callables) != 1 {
		t.Fatal("discovered lambda operator", declaration, err)
	}
	// Native spellings describe the callable; ABI types and ownership stay explicit.
	facade, err := cxx.Generate(filepath.ToSlash(header), []cxx.Class{
		{Name: "lambda_members::Factory", Methods: []cxx.Method{{Name: "make", Symbol: "lambda_make", Static: true,
			Signature:    abi.Signature{Result: abi.Pointer, Args: []abi.Type{abi.I32}},
			ObjectResult: &cxx.ObjectType{Name: declaration.Name, Kind: cxx.ObjectValue}}}},
		{Name: declaration.Name, Delete: "lambda_delete", Methods: []cxx.Method{{Name: declaration.Callables[0].Name, Symbol: "lambda_call",
			Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32, abi.I32}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	source, metadata := prepare(t, facade)
	for _, optimization := range []string{"-O0", "-O2"} {
		t.Run(optimization, func(t *testing.T) {
			session := dylib.New(dylib.Options{})
			defer session.Close()
			if err := session.Load(shared(t, source, optimization)); err != nil {
				t.Fatal(err)
			}
			functions := map[string]*dylib.Function{}
			for _, name := range facade.Functions {
				function, err := metadata.ForHost(name)
				if err != nil {
					t.Fatal(err)
				}
				functions[name], err = session.Bind(name, function.Signature)
				if err != nil {
					t.Fatal(err)
				}
			}
			output := func(name string, initial abi.Value, args ...abi.Value) abi.Value {
				t.Helper()
				value, err := abi.NewNativeValue(abi.TypeDesc{Type: initial.Type}, initial)
				if err != nil {
					t.Fatal(err)
				}
				defer value.Close()
				err = value.WithAddress(func(address uintptr) error {
					got, err := functions[name].Call(append(args, abi.Ptr(address))...)
					if err == nil && got != abi.Int32(cxx.Success) {
						return fmt.Errorf("%s status: %v", name, got)
					}
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				got, err := value.Read()
				if err != nil {
					t.Fatal(err)
				}
				return got
			}
			owner := output("lambda_make", abi.Ptr(0), abi.Int32(2))
			if owner.Bits == 0 {
				t.Fatal("missing lambda capture owner")
			}
			defer func() {
				if got := output("lambda_delete", owner); got != abi.Ptr(0) {
					t.Error("lambda owner was not consumed", got)
				}
			}()
			for _, want := range []int32{42, 82} {
				if got := output("lambda_call", abi.Int32(0), owner, abi.Int32(20), abi.Int32(20)); got != abi.Int32(want) {
					t.Fatal("discovered lambda call/capture", got, want)
				}
			}
		})
	}
}
