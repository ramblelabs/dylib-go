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
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func TestNativeDynamicInferredMethods(t *testing.T) {
	header, err := filepath.Abs("testdata/inferred_methods.hpp")
	if err != nil {
		t.Fatal(err)
	}
	inferred, err := cxxexample.InferSelectedMethods(context.Background(), filepath.ToSlash(header), cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
	if err != nil {
		t.Fatal(err)
	}
	source, metadata := prepare(t, inferred.Facade)
	signatures := map[string]abi.Signature{}
	for _, m := range inferred.Methods {
		signatures[m.Selection.Symbol] = m.Transport
	}
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			s := dylib.New(dylib.Options{})
			defer s.Close()
			if err := s.Load(shared(t, source, opt)); err != nil {
				t.Fatal(err)
			}
			functions := map[string]*dylib.Function{}
			for _, name := range inferred.Facade.Functions {
				d, err := metadata.ForHost(name)
				if err != nil {
					t.Fatal(err)
				}
				functions[name], err = s.Bind(name, d.Signature)
				if err != nil {
					t.Fatal(err)
				}
			}
			output := func(name string, initial abi.Value, status int32, args ...abi.Value) abi.Value {
				t.Helper()
				slot, err := abi.NewNativeValue(abi.TypeDesc{Type: initial.Type}, initial)
				if err != nil {
					t.Fatal(err)
				}
				defer slot.Close()
				if err := slot.WithAddress(func(address uintptr) error {
					got, err := functions[name].Call(append(args, abi.Ptr(address))...)
					if err == nil && got != abi.Int32(status) {
						return fmt.Errorf("%s status %v want %d", name, got, status)
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
				value, err := slot.Read()
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			integer := func(name string, want int32, args ...abi.Value) {
				t.Helper()
				if got := output(name, abi.Int32(999), cxx.Success, args...); got != abi.Int32(want) {
					t.Fatal(name, got, want)
				}
			}
			object := output("auto_new", abi.Ptr(0), cxx.Success, abi.Int32(20))
			if object.Bits == 0 {
				t.Fatal("missing owner")
			}
			// Destruction policy is explicit and survives rvalue-qualified calls.
			defer func() {
				if got := output("auto_delete", object, cxx.Success); got != abi.Ptr(0) {
					t.Fatal("owner not consumed", got)
				}
			}()
			base := output("auto_base", abi.Ptr(0), cxx.Success, object)
			view := output("auto_shared", abi.Ptr(0), cxx.Success, object)
			if base.Bits == 0 || view.Bits == 0 || base == object || view == object {
				t.Fatal(object, base, view)
			}
			for _, prefix := range []string{"q", "n"} {
				for i := 0; i < 12; i++ {
					integer(fmt.Sprintf("auto_%s%d", prefix, i), 42+int32(i), object, abi.Int32(22))
				}
			}
			integer("auto_inherited", 42, object, abi.Int32(-58))
			integer("auto_shared_call", 42, object, abi.Int32(2))
			integer("auto_score", 42, base, abi.Int32(22))
			integer("auto_operator", 42, object, abi.Int32(22))
			integer("auto_plus", 42, object, abi.Int32(40))
			if got := output("auto_q0", abi.Int32(999), cxx.InvalidArgument, abi.Ptr(0), abi.Int32(22)); got != abi.Int32(0) {
				t.Fatal(got)
			}
			if got, err := functions["auto_mark"].Call(abi.Ptr(0), abi.Int32(42)); err != nil || got != abi.Int32(cxx.InvalidArgument) {
				t.Fatal(got, err)
			}
			if got, err := functions["auto_q0"].Call(object, abi.Int32(22), abi.Ptr(0)); err != nil || got != abi.Int32(cxx.InvalidArgument) {
				t.Fatal(got, err)
			}
			if got := output("auto_fail", abi.Int32(999), cxx.Exception, object); got != abi.Int32(0) {
				t.Fatal(got)
			}
			for _, tc := range []struct {
				name  string
				value abi.Value
			}{{"auto_narrow", abi.Int8(-128)}, {"auto_wide", abi.Uint64(^uint64(0))}, {"auto_code", abi.Uint16(42)}, {"auto_boolean", abi.Boolean(true)}} {
				if got := output(tc.name, tc.value, cxx.Success, object, tc.value); got != tc.value {
					t.Fatal(tc.name, got)
				}
			}
			for _, name := range []string{"auto_native_long", "auto_character"} {
				value := abi.Value{Type: signatures[name].Result, Bits: 42}
				if got := output(name, value, cxx.Success, object, value); got != value {
					t.Fatal(name, got)
				}
			}
			if got := output("auto_mixed", abi.Float64(999), cxx.Success, object, abi.Float32(20), abi.Float64(22)); got != abi.Float64(42) {
				t.Fatal(got)
			}
			if got := output("auto_self", abi.Ptr(0), cxx.Success, object); got != object {
				t.Fatal(got, object)
			}
			entry := output("auto_entry", abi.Ptr(0), cxx.Success, object)
			if entry.Bits == 0 {
				t.Fatal("missing member entry")
			}
			integer("auto_apply", 42, object, entry, abi.Int32(40))
			integer("auto_apply", -100, object, abi.Ptr(0), abi.Int32(40))
			calls := 0
			callback, err := abi.NewCallback(abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}, func(args []abi.Value) (abi.Value, error) { calls++; return abi.Int32(int32(args[0].Bits) + 9), nil })
			if err != nil {
				t.Fatal(err)
			}
			defer callback.Close()
			if err := callback.WithAddress(func(address uintptr) error {
				integer("auto_apply", 49, object, abi.Ptr(address), abi.Int32(40))
				return nil
			}); err != nil || callback.Err() != nil || calls != 1 {
				t.Fatal(err, callback.Err(), calls)
			}
			if got, err := functions["auto_mark"].Call(object, abi.Int32(42)); err != nil || got != abi.Int32(cxx.Success) {
				t.Fatal(got, err)
			}
			integer("auto_read", 42, object)
			integer("auto_take", 42, object)
			integer("auto_read", -1, object)
		})
	}
}
