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

func TestNativeDynamicConversionFacade(t *testing.T) {
	header, err := filepath.Abs("testdata/conversions.hpp")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := generateConversions(filepath.ToSlash(header))
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
				declaration, err := metadata.ForHost(name)
				if err != nil {
					t.Fatal(err)
				}
				functions[name], err = session.Bind(name, declaration.Signature)
				if err != nil {
					t.Fatal(err)
				}
			}
			output := func(t *testing.T, name string, initial abi.Value, status int32, args ...abi.Value) abi.Value {
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
			for _, name := range []string{"bool_true", "bool_false", "integer_lvalue", "integer_rvalue", "floating", "address", "references", "owned", "function", "virtual", "exception", "nulls_occupied"} {
				t.Run(name, func(t *testing.T) {
					value := int32(42)
					if name == "bool_false" {
						value = 0
					}
					if name == "exception" {
						value = 13
					}
					owner := output(t, "convert_new", abi.Ptr(0), cxx.Success, abi.Int32(value))
					defer func() {
						if got := output(t, "convert_delete", owner, cxx.Success); got != abi.Ptr(0) {
							t.Error("conversion owner not consumed", got)
						}
						if got := output(t, "converted_live", abi.Int32(999), cxx.Success); got != abi.Int32(0) {
							t.Error("conversion owner leak", got)
						}
					}()
					switch name {
					case "bool_true", "bool_false":
						if got := output(t, "convert_bool", abi.Boolean(value == 0), cxx.Success, owner); got != abi.Boolean(value != 0) {
							t.Fatal("explicit bool", got)
						}
					case "integer_lvalue", "integer_rvalue":
						method, want := "convert_lvalue", int32(42)
						if name == "integer_rvalue" {
							method, want = "convert_rvalue", 43
						}
						if got := output(t, method, abi.Int32(0), cxx.Success, owner); got != abi.Int32(want) {
							t.Fatal("integer/ref-qualified conversion", got)
						}
					case "floating":
						if got := output(t, "convert_float", abi.Float64(0), cxx.Success, owner); got != abi.Float64(42.5) {
							t.Fatal("floating/cv-qualified conversion", got)
						}
					case "address":
						address := output(t, "convert_address", abi.Ptr(0), cxx.Success, owner)
						if got := output(t, "convert_read", abi.Int32(0), cxx.Success, address); got != abi.Int32(42) {
							t.Fatal("native pointer conversion", got)
						}
					case "references":
						first := output(t, "convert_reference", abi.Ptr(0), cxx.Success, owner)
						second := output(t, "convert_rvalue_reference", abi.Ptr(0), cxx.Success, owner)
						if first.Bits == 0 || first != second {
							t.Fatal("borrowed reference identities", first, second)
						}
						if got := output(t, "converted_read", abi.Int32(0), cxx.Success, first); got != abi.Int32(42) {
							t.Fatal("borrowed conversion value", got)
						}
					case "owned":
						result := output(t, "convert_owned", abi.Ptr(0), cxx.Success, owner)
						defer func() {
							if got := output(t, "converted_delete", result, cxx.Success); got != abi.Ptr(0) {
								t.Error("conversion result not consumed", got)
							}
						}()
						if got := output(t, "converted_read", abi.Int32(0), cxx.Success, result); got != abi.Int32(42) {
							t.Fatal("owned conversion result", got)
						}
						view := output(t, "convert_reference", abi.Ptr(0), cxx.Success, owner)
						if got := output(t, "converted_read", abi.Int32(0), cxx.Success, view); got != abi.Int32(-1) {
							t.Fatal("conversion post-move source", got)
						}
					case "function":
						entry := output(t, "convert_entry", abi.Ptr(0), cxx.Success, owner)
						if got := output(t, "convert_invoke", abi.Int32(0), cxx.Success, entry, abi.Int32(20), abi.Int32(22)); got != abi.Int32(42) {
							t.Fatal("function-pointer conversion", got)
						}
					case "virtual":
						view := output(t, "convert_base", abi.Ptr(0), cxx.Success, owner)
						if view.Bits == 0 || view == owner {
							t.Fatal("secondary conversion base adjustment", view)
						}
						if got := output(t, "convert_virtual", abi.Int32(0), cxx.Success, view); got != abi.Int32(42) {
							t.Fatal("virtual conversion dispatch", got)
						}
					case "exception":
						if got := output(t, "convert_owned", abi.Ptr(0), cxx.Exception, owner); got != abi.Ptr(0) {
							t.Fatal("conversion exception publication", got)
						}
						view := output(t, "convert_reference", abi.Ptr(0), cxx.Success, owner)
						if got := output(t, "converted_read", abi.Int32(0), cxx.Success, view); got != abi.Int32(13) {
							t.Fatal("native exception state", got)
						}
					case "nulls_occupied":
						if got := output(t, "convert_bool", abi.Boolean(true), cxx.InvalidArgument, abi.Ptr(0)); got != abi.Boolean(false) {
							t.Fatal("conversion bool clearing", got)
						}
						for _, method := range []string{"convert_address", "convert_reference", "convert_rvalue_reference", "convert_owned", "convert_entry"} {
							if got := output(t, method, abi.Ptr(0), cxx.InvalidArgument, abi.Ptr(0)); got != abi.Ptr(0) {
								t.Fatal("conversion null clearing", method, got)
							}
						}
						if got := output(t, "convert_owned", owner, cxx.InvalidArgument, owner); got != owner {
							t.Fatal("occupied conversion slot", got)
						}
						view := output(t, "convert_reference", abi.Ptr(0), cxx.Success, owner)
						if got := output(t, "converted_read", abi.Int32(0), cxx.Success, view); got != abi.Int32(42) {
							t.Fatal("occupied slot moved source", got)
						}
					}
				})
			}
		})
	}
}
