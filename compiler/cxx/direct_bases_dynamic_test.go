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

func TestNativeDynamicDirectBaseFacade(t *testing.T) {
	header, err := filepath.Abs("testdata/direct_bases.hpp")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := generateDirectBases(t)(filepath.ToSlash(header))
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
			output := func(name string, initial abi.Value, status int32, args ...abi.Value) abi.Value {
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
			owner := output("direct_new", abi.Ptr(0), cxx.Success, abi.Int32(20))
			defer func() {
				if got := output("direct_delete", owner, cxx.Success); got != abi.Ptr(0) {
					t.Error("owner not consumed", got)
				}
				if got := output("direct_live", abi.Int32(999), cxx.Success); got != abi.Int32(0) {
					t.Error("direct base owner leak", got)
				}
			}()
			secondary := output("direct_counter", abi.Ptr(0), cxx.Success, owner)
			virtual := output("direct_virtual", abi.Ptr(0), cxx.Success, owner)
			if owner.Bits == 0 || secondary.Bits == 0 || secondary == owner || virtual.Bits == 0 || virtual == owner || virtual == secondary {
				t.Fatal("secondary/virtual base adjustment", owner, secondary, virtual)
			}
			if got := output("direct_score", abi.Int32(0), cxx.Success, secondary, abi.Int32(21)); got != abi.Int32(42) {
				t.Fatal("virtual dispatch", got)
			}
			if got := output("direct_read", abi.Int32(0), cxx.Success, virtual); got != abi.Int32(42) {
				t.Fatal("virtual base state", got)
			}
			if got := output("direct_counter", abi.Ptr(999), cxx.InvalidArgument, abi.Ptr(0)); got != abi.Ptr(0) {
				t.Fatal("null base view", got)
			}
		})
	}
}
