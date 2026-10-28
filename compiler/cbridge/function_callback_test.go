//go:build libffi && cgo && (linux || darwin || windows)

package cbridge_test

import (
	"testing"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cbridge"
	"github.com/ramblelabs/dylib-go/compiler/clang"
	"github.com/ramblelabs/dylib-go/examples/headerbridge"
)

func TestNativeHeaderFunctionPointerCallback(t *testing.T) {
	compiler := clangTool(t)
	h, f := entryModel(t, compiler, "", flags()...)
	testNativeFacade(t, compiler, h, f, "testdata/entries.c", func(t *testing.T, s *dylib.Session, _ *clang.Header, f *cbridge.Facade) {
		var b cbridge.Binding
		for _, binding := range f.Functions {
			if binding.Name == "entry_scalar_apply" {
				b = binding
			}
		}
		p, err := b.LookupFunctionPointer(0)
		if err != nil {
			t.Fatal(err)
		}
		increment := int32(22)
		callback, err := abi.NewCallback(p.Signature, func(args []abi.Value) (abi.Value, error) {
			return abi.Int32(int32(args[0].Bits) + increment), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer callback.Close()
		err = callback.WithAddress(func(address uintptr) error {
			got, err := headerbridge.Call(s, b, abi.Ptr(address), abi.Int32(20))
			if err == nil && got != abi.Int32(42) {
				t.Fatalf("captured Go callback: %+v", got)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := callback.Err(); err != nil {
			t.Fatal(err)
		}
	})
}
