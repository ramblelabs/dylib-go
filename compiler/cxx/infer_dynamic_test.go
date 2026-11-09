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

func TestNativeDynamicInferredFacade(t *testing.T) {
	header, err := filepath.Abs("testdata/inferred.hpp")
	if err != nil {
		t.Fatal(err)
	}
	inferred, err := cxxexample.InferSelectedFunctions(context.Background(), filepath.ToSlash(header), cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: cppTargetFlags()})
	if err != nil {
		t.Fatal(err)
	}
	source, metadata := prepare(t, inferred.Facade)
	signatures := map[string]abi.Signature{}
	for _, f := range inferred.Functions {
		signatures[f.Selection.Symbol] = f.Transport
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
			integer := func(name string, want int32, args ...abi.Value) {
				t.Helper()
				if got := output(name, abi.Int32(999), cxx.Success, args...); got != abi.Int32(want) {
					t.Fatal(name, got, want)
				}
			}
			integer("auto_add", 42, abi.Int32(20), abi.Int32(22))
			integer("auto_constant", 42)
			integer("auto_plus", 42, abi.Int32(40))
			integer("auto_checked", 42, abi.Int32(40))
			if got := output("auto_checked", abi.Int32(999), cxx.Exception, abi.Int32(-1)); got != abi.Int32(0) {
				t.Fatal(got)
			}
			if got, err := functions["auto_add"].Call(abi.Int32(20), abi.Int32(22), abi.Ptr(0)); err != nil || got != abi.Int32(cxx.InvalidArgument) {
				t.Fatal(got, err)
			}
			for _, tc := range []struct {
				name  string
				value abi.Value
			}{
				{"auto_narrow", abi.Int8(-128)}, {"auto_wide", abi.Uint64(^uint64(0))}, {"auto_code", abi.Uint16(42)},
				{"auto_toggle", abi.Boolean(true)}, {"auto_boolean", abi.Boolean(true)}, {"auto_floating", abi.Float32(42)},
			} {
				if got := output(tc.name, tc.value, cxx.Success, tc.value); got != tc.value {
					t.Fatal(tc.name, got)
				}
			}
			for _, name := range []string{"auto_character", "auto_native_long", "auto_plain"} {
				value := abi.Value{Type: signatures[name].Result, Bits: 42}
				if got := output(name, value, cxx.Success, value); got != value {
					t.Fatal(name, got)
				}
			}
			if got := output("auto_mixed", abi.Float64(999), cxx.Success, abi.Float32(20), abi.Float64(22)); got != abi.Float64(42) {
				t.Fatal(got)
			}
			if got := output("auto_static", abi.Float64(999), cxx.Success, abi.Float64(21)); got != abi.Float64(42) {
				t.Fatal(got)
			}
			pair := output("auto_pair", abi.Ptr(0), cxx.Success)
			if pair.Bits == 0 {
				t.Fatal("missing native object")
			}
			if got := output("auto_echo", abi.Ptr(0), cxx.Success, pair); got != pair {
				t.Fatal(got, pair)
			}
			if got := output("auto_echo", pair, cxx.Success, abi.Ptr(0)); got != abi.Ptr(0) {
				t.Fatal(got)
			}
			if got, err := functions["auto_store"].Call(pair, abi.Int32(42)); err != nil || got != abi.Int32(cxx.Success) {
				t.Fatal(got, err)
			}
			integer("auto_read", 42, pair)
			entry := output("auto_entry", abi.Ptr(0), cxx.Success)
			if entry.Bits == 0 {
				t.Fatal("missing native function pointer")
			}
			integer("auto_apply", 42, entry, abi.Int32(40))
			integer("auto_apply", -100, abi.Ptr(0), abi.Int32(40))
			calls := 0
			callback, err := abi.NewCallback(abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}, func(args []abi.Value) (abi.Value, error) { calls++; return abi.Int32(int32(args[0].Bits) + 9), nil })
			if err != nil {
				t.Fatal(err)
			}
			defer callback.Close()
			if err := callback.WithAddress(func(address uintptr) error { integer("auto_apply", 49, abi.Ptr(address), abi.Int32(40)); return nil }); err != nil || callback.Err() != nil || calls != 1 {
				t.Fatal(err, callback.Err(), calls)
			}
		})
	}
}
