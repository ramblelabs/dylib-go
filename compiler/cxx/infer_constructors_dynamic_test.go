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

func TestNativeDynamicInferredConstructors(t *testing.T) {
	header, err := filepath.Abs("testdata/inferred_constructors.hpp")
	if err != nil {
		t.Fatal(err)
	}
	inferred, err := cxxexample.InferSelectedConstructors(context.Background(), filepath.ToSlash(header), cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
	if err != nil {
		t.Fatal(err)
	}
	source, metadata := prepare(t, inferred.Facade)
	longType := inferred.Constructors[2].Transport.Args[0]
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
			output := func(t *testing.T, name string, initial abi.Value, status int32, args ...abi.Value) abi.Value {
				t.Helper()
				slot, err := abi.NewNativeValue(abi.TypeDesc{Type: initial.Type}, initial)
				if err != nil {
					t.Fatal(err)
				}
				defer slot.Close()
				if err := slot.WithAddress(func(address uintptr) error {
					got, err := functions[name].Call(append(args, abi.Ptr(address))...)
					if err == nil && got != abi.Int32(status) {
						return fmt.Errorf("%s: status %v want %d", name, got, status)
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
			integer := func(t *testing.T, name string, want int32, args ...abi.Value) {
				t.Helper()
				if got := output(t, name, abi.Int32(999), cxx.Success, args...); got != abi.Int32(want) {
					t.Fatal(name, got, want)
				}
			}
			pair := output(t, "ctor_pair", abi.Ptr(0), cxx.Success)
			entry := output(t, "ctor_native_entry", abi.Ptr(0), cxx.Success)
			if pair.Bits == 0 || entry.Bits == 0 {
				t.Fatal(pair, entry)
			}
			for _, tc := range []struct {
				name, class string
				args        []abi.Value
				want        int32
			}{
				{"zero", "Receiver", nil, 42},
				{"int", "Receiver", []abi.Value{abi.Int32(20)}, 22},
				{"long", "Receiver", []abi.Value{{Type: longType, Bits: 20}}, 23},
				{"narrow", "Receiver", []abi.Value{abi.Int8(-128)}, 42},
				{"wide", "Receiver", []abi.Value{abi.Uint64(^uint64(0))}, 42},
				{"code", "Receiver", []abi.Value{abi.Uint16(42)}, 46},
				{"bool", "Receiver", []abi.Value{abi.Boolean(true)}, 42},
				{"float", "Receiver", []abi.Value{abi.Float32(42.5)}, 47},
				{"double", "Receiver", []abi.Value{abi.Float64(42.5)}, 48},
				{"pointer", "Receiver", []abi.Value{pair}, 27},
				{"const_pointer", "Receiver", []abi.Value{pair}, 28},
				{"global_pointer", "Receiver", []abi.Value{pair}, 27},
				{"entry", "Receiver", []abi.Value{entry, abi.Int32(40)}, 42},
				{"default_zero", "Defaulted", nil, 42},
				{"default_one", "Defaulted", []abi.Value{abi.Int32(40)}, 42},
				{"default_two", "Defaulted", []abi.Value{abi.Int32(20), abi.Int32(22)}, 42},
				{"deduced_int", "Deduced", []abi.Value{abi.Int32(40)}, 42},
				{"deduced_double", "Deduced", []abi.Value{abi.Float64(40)}, 43},
				{"converted", "Converted", []abi.Value{abi.Int32(40)}, 42},
				{"qualified", "Qualified", []abi.Value{abi.Int32(40)}, 42},
				{"box", "BoxAlias", []abi.Value{abi.Int32(40)}, 42},
			} {
				t.Run(tc.name, func(t *testing.T) {
					object := output(t, "ctor_"+tc.name, abi.Ptr(0), cxx.Success, tc.args...)
					if object.Bits == 0 {
						t.Fatal("missing allocation")
					}
					defer func() {
						if got := output(t, "ctor_"+tc.class+"_delete", object, cxx.Success); got != abi.Ptr(0) {
							t.Error("owner not cleared", got)
						}
					}()
					integer(t, "ctor_"+tc.class+"_read", tc.want, object)
					count := output(t, "ctor_count", abi.Int32(0), cxx.Success)
					if got := output(t, "ctor_"+tc.name, object, cxx.InvalidArgument, tc.args...); got != object {
						t.Fatal("occupied slot changed", got)
					}
					if got, err := functions["ctor_"+tc.name].Call(append(tc.args, abi.Ptr(0))...); err != nil || got != abi.Int32(cxx.InvalidArgument) {
						t.Fatal(got, err)
					}
					if got := output(t, "ctor_count", abi.Int32(0), cxx.Success); got != count {
						t.Fatal("invalid output allocated", got, count)
					}
				})
			}
			integer(t, "ctor_live", 0)
			for _, name := range []string{"ctor_int", "ctor_null_new", "ctor_throw_new"} {
				var args []abi.Value
				if name == "ctor_int" {
					args = []abi.Value{abi.Int32(-1)}
				}
				if got := output(t, name, abi.Ptr(0), cxx.Exception, args...); got != abi.Ptr(0) {
					t.Fatal("failure published allocation", got)
				}
			}
			integer(t, "ctor_live", 0)
			callback, err := abi.NewCallback(abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}, func(args []abi.Value) (abi.Value, error) { return abi.Int32(int32(args[0].Bits) + 3), nil })
			if err != nil {
				t.Fatal(err)
			}
			defer callback.Close()
			if err := callback.WithAddress(func(address uintptr) error {
				object := output(t, "ctor_entry", abi.Ptr(0), cxx.Success, abi.Ptr(address), abi.Int32(40))
				integer(t, "ctor_Receiver_read", 43, object)
				if got := output(t, "ctor_Receiver_delete", object, cxx.Success); got != abi.Ptr(0) {
					return fmt.Errorf("callback owner not cleared: %v", got)
				}
				return nil
			}); err != nil || callback.Err() != nil {
				t.Fatal(err, callback.Err())
			}
			integer(t, "ctor_live", 0)
		})
	}
}
