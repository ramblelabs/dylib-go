//go:build libffi && cgo && (linux || darwin || windows)

package cxx_test

import (
	"fmt"
	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
	"path/filepath"
	"testing"
)

func TestNativeDynamicFreeFunctionFacade(t *testing.T) {
	header, err := filepath.Abs("testdata/free_functions.hpp")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := generateFreeFunctions(t)(filepath.ToSlash(header))
	if err != nil {
		t.Fatal(err)
	}
	source, metadata := prepare(t, facade)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			s := dylib.New(dylib.Options{})
			defer s.Close()
			if err := s.Load(shared(t, source, opt)); err != nil {
				t.Fatal(err)
			}
			functions := map[string]*dylib.Function{}
			for _, name := range facade.Functions {
				decl, err := metadata.ForHost(name)
				if err != nil {
					t.Fatal(err)
				}
				functions[name], err = s.Bind(name, decl.Signature)
				if err != nil {
					t.Fatal(err)
				}
			}
			output := func(name string, initial abi.Value, status int32, args ...abi.Value) abi.Value {
				value, err := abi.NewNativeValue(abi.TypeDesc{Type: initial.Type}, initial)
				if err != nil {
					t.Fatal(err)
				}
				defer value.Close()
				err = value.WithAddress(func(address uintptr) error {
					got, err := functions[name].Call(append(args, abi.Ptr(address))...)
					if err == nil && got != abi.Int32(status) {
						return fmt.Errorf("%s status %v want %d", name, got, status)
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
			for _, name := range []string{"free_global_add", "free_add"} {
				if got := output(name, abi.Int32(0), cxx.Success, abi.Int32(20), abi.Int32(22)); got != abi.Int32(42) {
					t.Fatal(name, got)
				}
			}
			if got := output("free_floating", abi.Float64(0), cxx.Success, abi.Float64(20.25), abi.Float64(21.75)); got != abi.Float64(42) {
				t.Fatal(got)
			}
			for _, name := range []string{"free_internal", "free_c"} {
				if got := output(name, abi.Int32(0), cxx.Success, abi.Int32(40)); got != abi.Int32(42) {
					t.Fatal(name, got)
				}
			}
			if got := output("free_sum", abi.Int32(0), cxx.Success, abi.Int32(20), abi.Int8(2), abi.Float32(20)); got != abi.Int32(42) {
				t.Fatal(got)
			}
			if got := output("free_sum", abi.Int32(999), cxx.Exception, abi.Int32(-1), abi.Int8(2), abi.Float32(20)); got != abi.Int32(0) {
				t.Fatal("exception output", got)
			}
			if got, err := functions["free_add"].Call(abi.Int32(20), abi.Int32(22), abi.Ptr(0)); err != nil || got != abi.Int32(cxx.InvalidArgument) {
				t.Fatal(got, err)
			}
			entry := output("free_factory", abi.Ptr(0), cxx.Success)
			if entry.Bits == 0 {
				t.Fatal("missing native function")
			}
			if got := output("free_invoke", abi.Int32(0), cxx.Success, entry, abi.Int32(20), abi.Int32(22)); got != abi.Int32(42) {
				t.Fatal(got)
			}
			callbackCalls := 0
			callback, err := abi.NewCallback(abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32, abi.I32}}, func(args []abi.Value) (abi.Value, error) {
				callbackCalls++
				return abi.Int32(int32(args[0].Bits) + int32(args[1].Bits) + 7), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer callback.Close()
			if err := callback.WithAddress(func(address uintptr) error {
				if got := output("free_invoke", abi.Int32(0), cxx.Success, abi.Ptr(address), abi.Int32(20), abi.Int32(22)); got != abi.Int32(49) {
					t.Fatal("Go callback reentry", got)
				}
				return nil
			}); err != nil || callback.Err() != nil || callbackCalls != 1 {
				t.Fatal(err, callback.Err(), callbackCalls)
			}
			if got := output("free_invoke", abi.Int32(0), cxx.Success, abi.Ptr(0), abi.Int32(20), abi.Int32(22)); got != abi.Int32(-100) {
				t.Fatal(got)
			}
			object := output("free_borrowed", abi.Ptr(0), cxx.Success)
			if object.Bits == 0 {
				t.Fatal("missing borrowed reference")
			}
			if got := output("free_read", abi.Int32(0), cxx.Success, object, abi.Int32(22)); got != abi.Int32(42) {
				t.Fatal(got)
			}
			if got := output("free_read", abi.Int32(999), cxx.InvalidArgument, abi.Ptr(0), abi.Int32(22)); got != abi.Int32(0) {
				t.Fatal(got)
			}
			if got, err := functions["free_assign"].Call(object, abi.Int32(40)); err != nil || got != abi.Int32(cxx.Success) {
				t.Fatal(got, err)
			}
			if got := output("free_read", abi.Int32(0), cxx.Success, object, abi.Int32(2)); got != abi.Int32(42) {
				t.Fatal(got)
			}
		})
	}
}
