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

func TestNativeDynamicReceiverFacade(t *testing.T) {
	header, err := filepath.Abs("testdata/receivers.hpp")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := generateReceivers(filepath.ToSlash(header))
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
			for index, name := range []string{"cv_ref_0", "cv_ref_1", "cv_ref_2", "cv_ref_3", "cv_ref_4", "cv_ref_5", "cv_ref_6", "cv_ref_7", "cv_ref_8", "cv_ref_9", "cv_ref_10", "cv_ref_11", "void_operator", "virtual_base", "exception", "owned_result", "nulls"} {
				t.Run(name, func(t *testing.T) {
					owner := output(t, "receiver_new", abi.Ptr(0), cxx.Success, abi.Int32(2))
					defer func() {
						if got := output(t, "receiver_delete", owner, cxx.Success); got != abi.Ptr(0) {
							t.Error("receiver ownership not consumed", got)
						}
					}()
					want := func(got abi.Value, expected int32) {
						t.Helper()
						if got != abi.Int32(expected) {
							t.Fatal("receiver output", got, expected)
						}
					}
					switch name {
					case "void_operator":
						if got, err := functions["receiver_mark"].Call(owner, abi.Int32(40)); err != nil || got != abi.Int32(cxx.Success) {
							t.Fatal("volatile && void call", got, err)
						}
						want(output(t, "receiver_operator", abi.Int32(0), cxx.Success, owner, abi.Int32(2)), 42)
					case "virtual_base":
						view := output(t, "receiver_base", abi.Ptr(0), cxx.Success, owner)
						if view == owner || view.Bits == 0 {
							t.Fatal("missing secondary base adjustment", owner, view)
						}
						want(output(t, "base_virtual", abi.Int32(0), cxx.Success, view, abi.Int32(40)), 42)
					case "exception":
						want(output(t, "receiver_fail", abi.Int32(999), cxx.Exception, owner), 0)
						want(output(t, "receiver_read", abi.Int32(0), cxx.Success, owner), 2)
					case "owned_result":
						result := output(t, "receiver_take", abi.Ptr(0), cxx.Success, owner)
						defer func() {
							if got := output(t, "result_delete", result, cxx.Success); got != abi.Ptr(0) {
								t.Error("result ownership not consumed", got)
							}
						}()
						want(output(t, "result_read", abi.Int32(0), cxx.Success, result), 2)
						want(output(t, "receiver_read", abi.Int32(0), cxx.Success, owner), -1)
					case "nulls":
						want(output(t, "receiver_call8", abi.Int32(999), cxx.InvalidArgument, abi.Ptr(0), abi.Int32(20)), 0)
						if got, err := functions["receiver_call8"].Call(owner, abi.Int32(20), abi.Ptr(0)); err != nil || got != abi.Int32(cxx.InvalidArgument) {
							t.Fatal("null && output", got, err)
						}
					default:
						want(output(t, fmt.Sprintf("receiver_call%d", index), abi.Int32(0), cxx.Success, owner, abi.Int32(20)), 22+int32(index))
					}
				})
			}
		})
	}
}
