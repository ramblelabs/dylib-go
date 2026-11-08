//go:build libffi && cgo && (linux || darwin || windows)

package cxx_test

import (
	"fmt"
	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
	"path/filepath"
	"testing"
)

func TestNativeDynamicHeaderFacade(t *testing.T) {
	header, err := filepath.Abs("testdata/header.hpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"functions", "classes"} {
		t.Run(kind, func(t *testing.T) {
			facade, err := generateHeaderSelections(t, kind)(filepath.ToSlash(header))
			if err != nil {
				t.Fatal(err)
			}
			source, metadata := prepare(t, facade)
			for _, opt := range []string{"-O0", "-O2"} {
				t.Run(opt, func(t *testing.T) {
					s := dylib.New(dylib.Options{})
					defer s.Close()
					if err := s.Load(shared(t, source, opt)); err != nil {
						t.Fatal(err)
					}
					functions := map[string]*dylib.Function{}
					for _, name := range facade.Functions {
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
						v, err := abi.NewNativeValue(abi.TypeDesc{Type: initial.Type}, initial)
						if err != nil {
							t.Fatal(err)
						}
						defer v.Close()
						err = v.WithAddress(func(address uintptr) error {
							got, err := functions[name].Call(append(args, abi.Ptr(address))...)
							if err == nil && got != abi.Int32(status) {
								return fmt.Errorf("%s status %v want %d", name, got, status)
							}
							return err
						})
						if err != nil {
							t.Fatal(err)
						}
						got, err := v.Read()
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
					if kind == "classes" {
						owner := output("hd_new", abi.Ptr(0), cxx.Success, abi.Int32(20))
						if owner.Bits == 0 {
							t.Fatal("missing header owner")
						}
						defer func() { output("hd_delete", owner, cxx.Success) }()
						integer("hd_live", 1)
						integer("hd_member", 42, owner, abi.Int32(22))
						integer("hd_twice", 42, owner, abi.Int32(21))
						lambda := output("hd_lambda", abi.Ptr(0), cxx.Success)
						if lambda.Bits == 0 {
							t.Fatal("missing discovered lambda")
						}
						integer("hd_call", 42, lambda, abi.Int32(22))
						for _, name := range []string{"hd_member", "hd_call"} {
							if got := output(name, abi.Int32(999), cxx.InvalidArgument, abi.Ptr(0), abi.Int32(22)); got != abi.Int32(0) {
								t.Fatal(name, got)
							}
						}
						owner = output("hd_delete", owner, cxx.Success)
						if owner != abi.Ptr(0) {
							t.Fatal("header owner retained", owner)
						}
						integer("hd_live", 0)
						return
					}
					integer("hd_add", 42, abi.Int32(20), abi.Int32(22))
					for _, name := range []string{"hd_choose", "hd_local", "hd_c", "hd_included", "hd_offset", "hd_plus"} {
						integer(name, 42, abi.Int32(40))
					}
					integer("hd_scale", 42, abi.Int32(21))
					if got := output("hd_float", abi.Float64(999), cxx.Success, abi.Float64(40)); got != abi.Float64(42) {
						t.Fatal(got)
					}
					integer("hd_pack", 2, abi.Int32(20), abi.Float64(22))
					if got := output("hd_plus", abi.Int32(999), cxx.Exception, abi.Int32(-1)); got != abi.Int32(0) {
						t.Fatal("header exception", got)
					}
					if got, err := functions["hd_add"].Call(abi.Int32(20), abi.Int32(22), abi.Ptr(0)); err != nil || got != abi.Int32(cxx.InvalidArgument) {
						t.Fatal(got, err)
					}
				})
			}
		})
	}
}
