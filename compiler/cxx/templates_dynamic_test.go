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

func TestNativeDynamicTemplateFacade(t *testing.T) {
	for _, kind := range []string{"functions", "classes"} {
		t.Run(kind, func(t *testing.T) {
			header, err := filepath.Abs("testdata/templates.hpp")
			if err != nil {
				t.Fatal(err)
			}
			render := generateTemplateFunctions(t)
			if kind == "classes" {
				render = generateTemplateClasses
			}
			facade, err := render(filepath.ToSlash(header))
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
						if got := output(name, abi.Int32(999), cxx.Success, args...); got != abi.Int32(want) {
							t.Fatal(name, got, want)
						}
					}
					if kind == "classes" {
						owner := output("tmpl_new", abi.Ptr(0), cxx.Success, abi.Int32(20))
						if owner.Bits == 0 {
							t.Fatal("missing template owner")
						}
						defer func() {
							if got := output("tmpl_delete", owner, cxx.Success); got != abi.Ptr(0) {
								t.Error("template owner not consumed", got)
							}
						}()
						integer("tmpl_member", 42, owner, abi.Int32(20))
						integer("tmpl_static", 42, abi.Int32(21))
						integer("tmpl_member_choose", 42, owner, abi.Int32(20))
						integer("tmpl_member_ordinary", -100, owner, abi.Int32(20))
						lambda := output("tmpl_get_lambda", abi.Ptr(0), cxx.Success)
						if lambda.Bits == 0 {
							t.Fatal("missing generic lambda")
						}
						integer("tmpl_call", 42, lambda, abi.Int32(40))
						if got := output("tmpl_call", abi.Int32(999), cxx.InvalidArgument, abi.Ptr(0), abi.Int32(40)); got != abi.Int32(0) {
							t.Fatal("null lambda", got)
						}
						return
					}
					integer("tmpl_add", 42, abi.Int32(40))
					if got := output("tmpl_add", abi.Int32(999), cxx.Exception, abi.Int32(-1)); got != abi.Int32(0) {
						t.Fatal("template exception", got)
					}
					if got, err := functions["tmpl_add"].Call(abi.Int32(40), abi.Ptr(0)); err != nil || got != abi.Int32(cxx.InvalidArgument) {
						t.Fatal(got, err)
					}
					if got := output("tmpl_floating", abi.Float64(0), cxx.Success, abi.Float64(40)); got != abi.Float64(42) {
						t.Fatal(got)
					}
					integer("tmpl_choose", 42, abi.Int32(20))
					integer("tmpl_ordinary", -100, abi.Int32(20))
					integer("tmpl_pack", 2, abi.Int32(20), abi.Float64(22))
					integer("tmpl_boxed", 42, abi.Int32(40))
					integer("tmpl_signed", 42)
					integer("tmpl_unsigned", 42)
					integer("tmpl_sum", 42, abi.Int32(20), abi.Int8(2), abi.Float32(20))
					entry := output("tmpl_factory", abi.Ptr(0), cxx.Success)
					if entry.Bits == 0 {
						t.Fatal("missing template function")
					}
					integer("tmpl_invoke", 42, entry, abi.Int32(40))
					integer("tmpl_invoke", -100, abi.Ptr(0), abi.Int32(40))
					calls := 0
					callback, err := abi.NewCallback(abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}, func(args []abi.Value) (abi.Value, error) { calls++; return abi.Int32(int32(args[0].Bits) + 9), nil })
					if err != nil {
						t.Fatal(err)
					}
					defer callback.Close()
					if err := callback.WithAddress(func(address uintptr) error { integer("tmpl_invoke", 49, abi.Ptr(address), abi.Int32(40)); return nil }); err != nil || callback.Err() != nil || calls != 1 {
						t.Fatal(err, callback.Err(), calls)
					}
					token := output("tmpl_token", abi.Ptr(0), cxx.Success)
					if token.Bits == 0 {
						t.Fatal("missing template token")
					}
					if got := output("tmpl_pointer", abi.Ptr(0), cxx.Success, token); got != token {
						t.Fatal("pointer alias", got, token)
					}
					if got := output("tmpl_pointer", token, cxx.Success, abi.Ptr(0)); got != abi.Ptr(0) {
						t.Fatal("null template pointer", got)
					}
					integer("tmpl_read", 42, token)
					if got := output("tmpl_read", abi.Int32(999), cxx.InvalidArgument, abi.Ptr(0)); got != abi.Int32(0) {
						t.Fatal("null template reference", got)
					}
					if got := output("tmpl_lambda", abi.Ptr(0), cxx.Success); got.Bits == 0 {
						t.Fatal("missing borrowed lambda")
					}
				})
			}
		})
	}
}
