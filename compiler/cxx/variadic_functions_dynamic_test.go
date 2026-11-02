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

func TestNativeDynamicVariadicFunctionFacade(t *testing.T) {
	header, err := filepath.Abs("testdata/variadic_functions.hpp")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := generateVariadicFunctions(filepath.ToSlash(header))
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
			entry := output(t, "varfn_factory", abi.Ptr(0), cxx.Success)
			if entry.Bits == 0 {
				t.Fatal("null variadic entry")
			}
			for _, name := range []string{"constructor_call", "returned", "conversion", "function_tail", "null_entries", "exceptions_nulls", "after_owner_delete"} {
				t.Run(name, func(t *testing.T) {
					owner := output(t, "varfn_new", abi.Ptr(0), cxx.Success, entry)
					defer func() {
						if got := output(t, "varfn_delete", owner, cxx.Success); got != abi.Ptr(0) {
							t.Error("owner not consumed", got)
						}
						if got := output(t, "varfn_live", abi.Int32(999), cxx.Success); got != abi.Int32(0) {
							t.Error("variadic function owner leak", got)
						}
					}()
					var got abi.Value
					want := abi.Int32(42)
					switch name {
					case "constructor_call":
						got = output(t, "varfn_call", abi.Int32(0), cxx.Success, owner, abi.Int32(20), abi.Int8(2), abi.Float32(20))
					case "returned", "conversion", "after_owner_delete":
						method := "varfn_get"
						if name == "conversion" {
							method = "varfn_convert"
						}
						returned := output(t, method, abi.Ptr(0), cxx.Success, owner)
						if returned != entry {
							t.Fatal("entry identity", returned, entry)
						}
						if name == "after_owner_delete" {
							owner = output(t, "varfn_delete", owner, cxx.Success)
						}
						got = output(t, "varfn_invoke", abi.Int32(0), cxx.Success, returned, abi.Int32(20), abi.Int16(2), abi.Float32(20))
					case "function_tail":
						got = output(t, "varfn_tail", abi.Int32(0), cxx.Success, abi.Int32(20), entry)
					case "null_entries":
						got = output(t, "varfn_invoke", abi.Int32(0), cxx.Success, abi.Ptr(0), abi.Int32(20), abi.Int16(2), abi.Float32(20))
						want = abi.Int32(-100)
						if v := output(t, "varfn_tail", abi.Int32(0), cxx.Success, abi.Int32(20), abi.Ptr(0)); v != want {
							t.Fatal("null variadic entry tail", v)
						}
					case "exceptions_nulls":
						got = output(t, "varfn_call", abi.Int32(999), cxx.Exception, owner, abi.Int32(-1), abi.Int8(2), abi.Float32(20))
						want = abi.Int32(0)
						if v := output(t, "varfn_new", abi.Ptr(0), cxx.Exception, abi.Ptr(0)); v != abi.Ptr(0) {
							t.Fatal("failed constructor publication", v)
						}
						if v := output(t, "varfn_get", abi.Ptr(0), cxx.InvalidArgument, abi.Ptr(0)); v != abi.Ptr(0) {
							t.Fatal("null receiver output", v)
						}
					}
					if got != want {
						t.Fatal("variadic function result", got, "want", want)
					}
				})
			}
		})
	}
}
