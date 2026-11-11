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

func TestNativeDynamicInferredConversions(t *testing.T) {
	header, err := filepath.Abs("testdata/inferred_conversions.hpp")
	if err != nil {
		t.Fatal(err)
	}
	inferred, err := cxxexample.InferSelectedConversions(context.Background(), filepath.ToSlash(header), cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
	if err != nil {
		t.Fatal(err)
	}
	source, metadata := prepare(t, inferred.Facade)
	var longType abi.Type
	for _, m := range inferred.Methods {
		if m.Selection.Symbol == "convert_auto_long" {
			longType = m.Transport.Result
		}
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
			integer := func(t *testing.T, name string, want int32, args ...abi.Value) {
				t.Helper()
				if got := output(t, name, abi.Int32(999), cxx.Success, args...); got != abi.Int32(want) {
					t.Fatal(name, got, want)
				}
			}
			for _, prefix := range []string{"q", "n"} {
				plain, qualified := "Plain", "Qualified"
				if prefix == "n" {
					plain, qualified = "NPlain", "NQualified"
				}
				for _, class := range []string{plain, qualified} {
					t.Run(prefix+"/"+class, func(t *testing.T) {
						name := "convert_" + prefix + "_" + class
						object := output(t, name+"_new", abi.Ptr(0), cxx.Success, abi.Int32(42))
						defer func() {
							if got := output(t, name+"_delete", object, cxx.Success); got != abi.Ptr(0) {
								t.Error("conversion form owner not cleared", got)
							}
						}()
						first, last := 0, 4
						if class == qualified {
							first, last = 4, 12
						}
						for i := first; i < last; i++ {
							integer(t, fmt.Sprintf("convert_auto_%s%d", prefix, i), 42+int32(i), object)
						}
					})
				}
			}
			integer(t, "convert_forms_alive", 0)
			object := output(t, "convert_auto_new", abi.Ptr(0), cxx.Success, abi.Int32(42))
			defer func() {
				if got := output(t, "convert_auto_delete", object, cxx.Success); got != abi.Ptr(0) {
					t.Fatal("conversion owner not cleared", got)
				}
			}()
			base := output(t, "convert_auto_base", abi.Ptr(0), cxx.Success, object)
			if base.Bits == 0 || base == object {
				t.Fatal(object, base)
			}
			integer(t, "convert_auto_virtual", 42, base)
			integer(t, "convert_auto_int", 42, object)
			for _, tc := range []struct {
				name  string
				value abi.Value
			}{{"bool", abi.Boolean(true)}, {"narrow", abi.Int8(-128)}, {"wide", abi.Uint64(^uint64(0))}, {"code", abi.Uint16(42)}, {"double", abi.Float64(42.5)}, {"long", abi.Value{Type: longType, Bits: 42}}} {
				if got := output(t, "convert_auto_"+tc.name, tc.value, cxx.Success, object); got != tc.value {
					t.Fatal(tc.name, got, tc.value)
				}
			}
			pointer := output(t, "convert_auto_pointer", abi.Ptr(0), cxx.Success, object)
			constant := output(t, "convert_auto_const_pointer", abi.Ptr(0), cxx.Success, object)
			self := output(t, "convert_auto_self", abi.Ptr(0), cxx.Success, object)
			if pointer.Bits == 0 || pointer != constant || self != object {
				t.Fatal(pointer, constant, self, object)
			}
			entry := output(t, "convert_auto_entry", abi.Ptr(0), cxx.Success, object)
			if entry.Bits == 0 {
				t.Fatal("missing converted function")
			}
			integer(t, "convert_auto_apply", 42, object, entry, abi.Int32(20), abi.Int32(22))
			integer(t, "convert_auto_apply", -100, object, abi.Ptr(0), abi.Int32(20), abi.Int32(22))
			calls := 0
			callback, err := abi.NewCallback(abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32, abi.I32}}, func(args []abi.Value) (abi.Value, error) {
				calls++
				return abi.Int32(int32(args[0].Bits) + int32(args[1].Bits) + 1), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer callback.Close()
			if err := callback.WithAddress(func(address uintptr) error {
				integer(t, "convert_auto_apply", 43, object, abi.Ptr(address), abi.Int32(20), abi.Int32(22))
				return nil
			}); err != nil || callback.Err() != nil || calls != 1 {
				t.Fatal(err, callback.Err(), calls)
			}
			if got := output(t, "convert_auto_int", abi.Int32(999), cxx.InvalidArgument, abi.Ptr(0)); got != abi.Int32(0) {
				t.Fatal("null receiver output", got)
			}
			if got, err := functions["convert_auto_int"].Call(object, abi.Ptr(0)); err != nil || got != abi.Int32(cxx.InvalidArgument) {
				t.Fatal(got, err)
			}
			integer(t, "convert_auto_take", 43, object)
			integer(t, "convert_auto_read", -1, object)
			if got := output(t, "convert_auto_int", abi.Int32(999), cxx.Exception, object); got != abi.Int32(0) {
				t.Fatal("conversion exception output", got)
			}
			object = output(t, "convert_auto_delete", object, cxx.Success)
			if object != abi.Ptr(0) {
				t.Fatal("conversion owner not cleared", object)
			}
			template := output(t, "convert_template_new", abi.Ptr(0), cxx.Success)
			defer func() {
				if got := output(t, "convert_template_delete", template, cxx.Success); got != abi.Ptr(0) {
					t.Error("conversion template owner not cleared", got)
				}
			}()
			integer(t, "convert_template_int", 42, template)
			if got := output(t, "convert_template_double", abi.Float64(0), cxx.Success, template); got != abi.Float64(42) {
				t.Fatal(got)
			}
		})
	}
}
