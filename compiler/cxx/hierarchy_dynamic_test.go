//go:build libffi && cgo && (linux || darwin || windows)

package cxx_test

import (
	"fmt"
	"path/filepath"
	"testing"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

func TestNativeDynamicHierarchyFacade(t *testing.T) {
	header, err := filepath.Abs("testdata/hierarchy.hpp")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := generateHierarchy(t)(filepath.ToSlash(header))
	if err != nil {
		t.Fatal(err)
	}
	testDynamicHierarchy(t, facade)
}

func TestNativeDynamicNamespaceHierarchyFacade(t *testing.T) {
	header, err := filepath.Abs("testdata/namespace_hierarchy.hpp")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := generateNamespaceHierarchy(t)(filepath.ToSlash(header))
	if err != nil {
		t.Fatal(err)
	}
	testDynamicHierarchy(t, facade)
}

func testDynamicHierarchy(t *testing.T, facade *cxx.Facade) {
	t.Helper()
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
				declaration, err := metadata.ForHost(name)
				if err != nil {
					t.Fatal(err)
				}
				functions[name], err = session.Bind(name, declaration.Signature)
				if err != nil {
					t.Fatal(err)
				}
			}
			output := func(name string, initial abi.Value, status int32, args ...abi.Value) abi.Value {
				t.Helper()
				value, err := abi.NewNativeValue(abi.TypeDesc{Type: initial.Type}, initial)
				if err != nil {
					t.Fatal(err)
				}
				defer value.Close()
				err = value.WithAddress(func(address uintptr) error {
					got, err := functions[name].Call(append(args, abi.Ptr(address))...)
					if err == nil && got != abi.Int32(status) {
						return fmt.Errorf("%s status %v; want %d", name, got, status)
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
			owner := output("hier_new", abi.Ptr(0), cxx.Success, abi.Int32(20))
			defer func() {
				if got := output("hier_delete", owner, cxx.Success); got != abi.Ptr(0) {
					t.Error("owner not consumed", got)
				}
				if got := output("hier_live", abi.Int32(999), cxx.Success); got != abi.Int32(0) {
					t.Error("hierarchy owner leak", got)
				}
			}()
			left := output("hier_alias_left", abi.Ptr(0), cxx.Success, owner)
			right := output("hier_alias_right", abi.Ptr(0), cxx.Success, owner)
			direct := output("hier_alias_counter", abi.Ptr(0), cxx.Success, owner)
			viaLeft := output("hier_left_counter", abi.Ptr(0), cxx.Success, left)
			viaRight := output("hier_right_counter", abi.Ptr(0), cxx.Success, right)
			if left.Bits == 0 || right.Bits == 0 || left == right || direct.Bits == 0 || direct != viaLeft || direct != viaRight {
				t.Fatal("recursive shared virtual base identities", left, right, direct, viaLeft, viaRight)
			}
			for _, view := range []abi.Value{direct, viaLeft, viaRight} {
				if got := output("hier_score", abi.Int32(0), cxx.Success, view, abi.Int32(20)); got != abi.Int32(42) {
					t.Fatal("recursive virtual dispatch", got)
				}
			}
			if got := output("hier_left_counter", abi.Ptr(999), cxx.InvalidArgument, abi.Ptr(0)); got != abi.Ptr(0) {
				t.Fatal("null composed view", got)
			}
			if got := output("hier_new", abi.Ptr(0), cxx.Exception, abi.Int32(-1)); got != abi.Ptr(0) {
				t.Fatal("failed hierarchy constructor", got)
			}
			if got := output("hier_live", abi.Int32(0), cxx.Success); got != abi.Int32(1) {
				t.Fatal("hierarchy owner count", got)
			}
		})
	}
}
