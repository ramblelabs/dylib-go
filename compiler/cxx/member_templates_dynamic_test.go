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

func TestNativeDynamicMemberTemplates(t *testing.T) {
	header, err := filepath.Abs("testdata/member_templates.hpp")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := generateMemberTemplates(t)(filepath.ToSlash(header))
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
			owner := output("mt_new", abi.Ptr(0), cxx.Success, abi.Int32(20))
			selected := output("mt_selected_new", abi.Ptr(0), cxx.Success, abi.Int32(20))
			if owner.Bits == 0 || selected.Bits == 0 {
				t.Fatal("missing template owner")
			}
			defer func() { output("mt_selected_delete", selected, cxx.Success); output("mt_delete", owner, cxx.Success) }()
			integer("mt_live", 1)
			integer("mt_add", 42, owner, abi.Int32(20))
			integer("mt_move", 42, owner, abi.Int32(22))
			if got := output("mt_float", abi.Float64(999), cxx.Success, abi.Float64(21)); got != abi.Float64(42) {
				t.Fatal(got)
			}
			integer("mt_box", 42, owner, abi.Int32(22))
			integer("mt_pack", 22, owner, abi.Int32(20), abi.Float64(22))
			integer("mt_sum", 42, owner, abi.Int32(10), abi.Int8(2), abi.Float32(10))
			integer("mt_convert", 20, owner)
			integer("mt_selected_add", 42, selected, abi.Int32(22))
			if got := output("mt_add", abi.Int32(999), cxx.Exception, owner, abi.Int32(-1)); got != abi.Int32(0) {
				t.Fatal("template exception", got)
			}
			if got := output("mt_add", abi.Int32(999), cxx.InvalidArgument, abi.Ptr(0), abi.Int32(20)); got != abi.Int32(0) {
				t.Fatal("null receiver", got)
			}
			if got, err := functions["mt_add"].Call(owner, abi.Int32(20), abi.Ptr(0)); err != nil || got != abi.Int32(cxx.InvalidArgument) {
				t.Fatal(got, err)
			}
			lambda := output("mt_lambda", abi.Ptr(0), cxx.Success)
			if lambda.Bits == 0 {
				t.Fatal("missing generic lambda")
			}
			integer("mt_call", 42, lambda, abi.Int32(22))
			if got := output("mt_call", abi.Int32(999), cxx.InvalidArgument, abi.Ptr(0), abi.Int32(22)); got != abi.Int32(0) {
				t.Fatal("null lambda", got)
			}
			owner = output("mt_delete", owner, cxx.Success)
			selected = output("mt_selected_delete", selected, cxx.Success)
			if owner != abi.Ptr(0) || selected != abi.Ptr(0) {
				t.Fatal("template owners retained", owner, selected)
			}
			integer("mt_live", 0)
		})
	}
}
