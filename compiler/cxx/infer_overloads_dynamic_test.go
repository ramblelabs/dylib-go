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

func TestNativeDynamicInferredOverloads(t *testing.T) {
	header, err := filepath.Abs("testdata/inferred_overloads.hpp")
	if err != nil {
		t.Fatal(err)
	}
	functions, err := cxxexample.InferSelectedFunctionOverloads(context.Background(), filepath.ToSlash(header), cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
	if err != nil {
		t.Fatal(err)
	}
	methods, err := cxxexample.InferSelectedMethodOverloads(context.Background(), filepath.ToSlash(header), cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
	if err != nil {
		t.Fatal(err)
	}
	facade := &cxx.Facade{CHeader: append(functions.Facade.CHeader, methods.Facade.CHeader...), CXXSource: append(functions.Facade.CXXSource, methods.Facade.CXXSource...), Functions: append(functions.Facade.Functions, methods.Facade.Functions...)}
	source, metadata := prepare(t, facade)
	signatures := map[string]abi.Signature{}
	for _, f := range functions.Functions {
		signatures[f.Selection.Symbol] = f.Transport
	}
	for _, m := range methods.Methods {
		signatures[m.Selection.Symbol] = m.Transport
	}
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			s := dylib.New(dylib.Options{})
			defer s.Close()
			if err := s.Load(shared(t, source, opt)); err != nil {
				t.Fatal(err)
			}
			bound := map[string]*dylib.Function{}
			for _, name := range facade.Functions {
				d, err := metadata.ForHost(name)
				if err != nil {
					t.Fatal(err)
				}
				bound[name], err = s.Bind(name, d.Signature)
				if err != nil {
					t.Fatal(err)
				}
			}
			call := func(name string, status int32, args ...abi.Value) {
				t.Helper()
				value, err := bound[name].Call(args...)
				if err != nil || value != abi.Int32(status) {
					t.Fatal(name, value, status, err)
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
					call(name, status, append(args, abi.Ptr(address))...)
					return nil
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
				if value := output(name, abi.Int32(999), cxx.Success, args...); value != abi.Int32(want) {
					t.Fatal(name, value, want)
				}
			}
			floating := func(name string, args ...abi.Value) {
				t.Helper()
				if value := output(name, abi.Float64(999), cxx.Success, args...); value != abi.Float64(42) {
					t.Fatal(name, value)
				}
			}
			longValue := func(name string, position int) abi.Value {
				return abi.Value{Type: signatures[name].Args[position], Bits: 39}
			}
			integer("pick_integer", 42, abi.Int32(40))
			integer("pick_long", 42, longValue("pick_long", 0))
			integer("pick_zero", 42)
			integer("pick_one", 42, abi.Int32(42))
			integer("pick_template", 42, abi.Int32(40))
			integer("pick_static_int", 42, abi.Int32(42))
			integer("pick_checked", 42, abi.Int32(40))
			if value := output("pick_checked", abi.Int32(999), cxx.Exception, abi.Int32(-1)); value != abi.Int32(0) {
				t.Fatal(value)
			}
			floating("pick_double", abi.Float64(38))
			floating("pick_ordinary", abi.Float64(39))
			floating("pick_static_double", abi.Float64(42))
			for _, tc := range []struct {
				name  string
				value abi.Value
			}{{"pick_narrow", abi.Int8(-128)}, {"pick_wide", abi.Uint64(^uint64(0))}, {"pick_enum", abi.Uint16(42)}} {
				if value := output(tc.name, tc.value, cxx.Success, tc.value); value != tc.value {
					t.Fatal(tc.name, value)
				}
			}
			call("pick_store_int", cxx.Success, abi.Int32(42))
			integer("pick_read", 42)
			call("pick_store_double", cxx.Success, abi.Float64(41))
			integer("pick_read", 42)
			pair := output("pick_pair", abi.Ptr(0), cxx.Success)
			entry := output("pick_entry", abi.Ptr(0), cxx.Success)
			if pair.Bits == 0 || entry.Bits == 0 {
				t.Fatal(pair, entry)
			}
			integer("pick_pointer", 22, pair)
			integer("pick_const_pointer", 23, pair)
			integer("pick_pointer", -100, abi.Ptr(0))
			integer("pick_const_pointer", -101, abi.Ptr(0))
			integer("pick_apply", 42, entry, abi.Int32(40))
			integer("pick_apply", -100, abi.Ptr(0), abi.Int32(40))
			object := output("pick_new", abi.Ptr(0), cxx.Success, abi.Int32(20))
			if object.Bits == 0 {
				t.Fatal("missing overload receiver")
			}
			defer func() {
				if value := output("pick_delete", object, cxx.Success); value != abi.Ptr(0) {
					t.Fatal(value)
				}
			}()
			base := output("pick_base", abi.Ptr(0), cxx.Success, object)
			if base.Bits == 0 || base == object {
				t.Fatal(base, object)
			}
			for _, prefix := range []string{"q", "n"} {
				for q := 0; q < 12; q++ {
					integer(fmt.Sprintf("pick_%s%d", prefix, q), 42+int32(q), object, abi.Int32(22))
				}
			}
			floating("pick_score_double", object, abi.Float64(18))
			integer("pick_zero_mutable", 42, object)
			integer("pick_zero_const", 43, object)
			integer("pick_operator_int", 42, object, abi.Int32(22))
			floating("pick_operator_double", object, abi.Float64(22))
			integer("pick_plus", 42, object, abi.Int32(40))
			integer("pick_checked_int", 42, object, abi.Int32(22))
			if value := output("pick_checked_int", abi.Int32(999), cxx.Exception, object, abi.Int32(-1)); value != abi.Int32(0) {
				t.Fatal(value)
			}
			floating("pick_checked_double", object, abi.Float64(22))
			integer("pick_native_int", 42, object, abi.Int32(40))
			integer("pick_native_long", 42, object, longValue("pick_native_long", 0))
			integer("pick_pointer_mutable", 22, object, pair)
			integer("pick_pointer_const", 23, object, pair)
			integer("pick_apply_member", 42, object, entry, abi.Int32(40))
			integer("pick_inherited_int", 42, object, abi.Int32(-58))
			floating("pick_inherited_double", object, abi.Float64(-59))
			integer("pick_virtual_int", 42, base, abi.Int32(17))
			floating("pick_virtual_double", base, abi.Float64(18))
			integer("pick_shared", 42, object, abi.Int32(2))
			floating("pick_mixed_ordinary", object, abi.Float64(39))
			integer("pick_mixed_template", 42, object, abi.Int32(40))
			if value := output("pick_q0", abi.Int32(999), cxx.InvalidArgument, abi.Ptr(0), abi.Int32(22)); value != abi.Int32(0) {
				t.Fatal(value)
			}
			call("pick_mark_int", cxx.InvalidArgument, abi.Ptr(0), abi.Int32(42))
			call("pick_q0", cxx.InvalidArgument, object, abi.Int32(22), abi.Ptr(0))
			calls := 0
			callback, err := abi.NewCallback(abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}, func(args []abi.Value) (abi.Value, error) { calls++; return abi.Int32(int32(args[0].Bits) + 9), nil })
			if err != nil {
				t.Fatal(err)
			}
			defer callback.Close()
			if err := callback.WithAddress(func(address uintptr) error {
				integer("pick_apply", 49, abi.Ptr(address), abi.Int32(40))
				integer("pick_apply_member", 49, object, abi.Ptr(address), abi.Int32(40))
				return nil
			}); err != nil || callback.Err() != nil || calls != 2 {
				t.Fatal(err, callback.Err(), calls)
			}
			call("pick_mark_int", cxx.Success, object, abi.Int32(42))
			integer("pick_read_member", 42, object)
			call("pick_mark_double", cxx.Success, object, abi.Float64(41))
			integer("pick_read_member", 42, object)
		})
	}
}
