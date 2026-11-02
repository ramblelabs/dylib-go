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

func TestNativeDynamicVariadicFacade(t *testing.T) {
	header, err := filepath.Abs("testdata/variadics.hpp")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := generateVariadics(filepath.ToSlash(header))
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
				if declaration.Signature.Variadic {
					t.Fatal("facade carrier must be fixed", name)
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
			for _, name := range []string{"empty", "promotions", "integer_spill", "floating_spill", "mixed", "rvalue_callable", "object_prefix", "pointer_function_tail", "function_prefix", "virtual", "void", "borrowed_result", "exceptions_nulls"} {
				t.Run(name, func(t *testing.T) {
					owner := output(t, "var_new", abi.Ptr(0), cxx.Success, abi.Int32(20))
					defer func() {
						if got := output(t, "var_delete", owner, cxx.Success); got != abi.Ptr(0) {
							t.Error("owner not consumed", got)
						}
						if got := output(t, "var_live", abi.Int32(999), cxx.Success); got != abi.Int32(0) {
							t.Error("variadic owner leak", got)
						}
					}()
					want := abi.Int32(42)
					var got abi.Value
					switch name {
					case "empty":
						got = output(t, "var_empty", abi.Int32(999), cxx.Success, owner, abi.Int32(0))
						want = abi.Int32(20)
					case "promotions":
						got = output(t, "var_promoted", abi.Int32(0), cxx.Success, owner, abi.Int32(5), abi.Int8(-3), abi.Uint8(4), abi.Int16(-5), abi.Uint16(25), abi.Boolean(true))
					case "integer_spill":
						args := []abi.Value{owner, abi.Int32(12)}
						for i := 0; i < 11; i++ {
							args = append(args, abi.Int8(1))
						}
						args = append(args, abi.Int8(11))
						got = output(t, "var_spill", abi.Int32(0), cxx.Success, args...)
					case "floating_spill":
						args := []abi.Value{abi.Int32(12)}
						for i := 0; i < 11; i++ {
							args = append(args, abi.Float32(1))
						}
						args = append(args, abi.Float32(11))
						got = output(t, "var_floats", abi.Float64(0), cxx.Success, args...)
						want = abi.Float64(42)
					case "mixed":
						got = output(t, "var_mixed", abi.Float64(0), cxx.Success, owner, abi.Int32(7), abi.Uint32(4000000000), abi.Int64(-5000000000), abi.Uint64(10000000000), abi.Float32(1.25), abi.Float64(2.5))
						want = abi.Float64(42)
					case "rvalue_callable":
						got = output(t, "var_call", abi.Int32(0), cxx.Success, owner, abi.Int32(2), abi.Int16(20))
					case "object_prefix", "pointer_function_tail", "borrowed_result":
						address := output(t, "var_address", abi.Ptr(0), cxx.Success, owner, abi.Int32(7), abi.Uint8(7))
						if address.Bits == 0 {
							t.Fatal("missing borrowed result")
						}
						switch name {
						case "object_prefix":
							got = output(t, "var_prefix", abi.Int32(0), cxx.Success, address, abi.Int32(1), abi.Int8(22))
						case "pointer_function_tail":
							entry := output(t, "var_entry", abi.Ptr(0), cxx.Success)
							got = output(t, "var_pointers", abi.Int32(0), cxx.Success, abi.Int32(22), address, entry)
							if v := output(t, "var_pointers", abi.Int32(0), cxx.Success, abi.Int32(22), abi.Ptr(0), entry); v != abi.Int32(-100) {
								t.Fatal("null tail pointer", v)
							}
						case "borrowed_result":
							got = output(t, "var_read", abi.Int32(0), cxx.Success, address)
							want = abi.Int32(20)
						}
					case "function_prefix":
						entry := output(t, "var_entry", abi.Ptr(0), cxx.Success)
						got = output(t, "var_apply", abi.Int32(0), cxx.Success, entry, abi.Int32(0), abi.Int8(20), abi.Int16(22))
					case "virtual":
						base := output(t, "var_base", abi.Ptr(0), cxx.Success, owner)
						if base.Bits == 0 || base == owner {
							t.Fatal("secondary base adjustment", base)
						}
						got = output(t, "var_virtual", abi.Int32(0), cxx.Success, base, abi.Int32(2), abi.Int16(-5), abi.Uint16(27))
					case "void":
						status, err := functions["var_set"].Call(owner, abi.Int32(20), abi.Int8(22))
						if err != nil || status != abi.Int32(cxx.Success) {
							t.Fatal(status, err)
						}
						got = output(t, "var_empty", abi.Int32(0), cxx.Success, owner, abi.Int32(0))
					case "exceptions_nulls":
						got = output(t, "var_empty", abi.Int32(999), cxx.Exception, owner, abi.Int32(-1))
						want = abi.Int32(0)
						if v := output(t, "var_empty", abi.Int32(999), cxx.InvalidArgument, abi.Ptr(0), abi.Int32(0)); v != want {
							t.Fatal("null receiver clearing", v)
						}
						if v := output(t, "var_prefix", abi.Int32(999), cxx.InvalidArgument, abi.Ptr(0), abi.Int32(1), abi.Int8(22)); v != want {
							t.Fatal("null fixed reference", v)
						}
						status, err := functions["var_empty"].Call(owner, abi.Int32(0), abi.Ptr(0))
						if err != nil || status != abi.Int32(cxx.InvalidArgument) {
							t.Fatal("null output", status, err)
						}
					}
					if got != want {
						t.Fatal("variadic result", got, "want", want)
					}
				})
			}
		})
	}
}
