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

func TestNativeDynamicMoveObjectFacade(t *testing.T) {
	header, err := filepath.Abs("testdata/moves.hpp")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := generateMoves(filepath.ToSlash(header))
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
			withBox := func(t *testing.T, value int32, body func(abi.Value)) {
				t.Helper()
				owner := output(t, "move_new", abi.Ptr(0), cxx.Success, abi.Int32(value))
				defer func() {
					if got := output(t, "move_delete", owner, cxx.Success); got != abi.Ptr(0) {
						t.Error("owner not consumed", got)
					}
					if got := output(t, "move_live", abi.Int32(999), cxx.Success); got != abi.Int32(0) {
						t.Error("move owner leak", got)
					}
				}()
				body(owner)
			}
			for i := 0; i < 12; i++ {
				t.Run(fmt.Sprintf("probe_%d", i), func(t *testing.T) {
					withBox(t, 42, func(owner abi.Value) {
						if got := output(t, fmt.Sprintf("move_probe%d", i), abi.Int32(0), cxx.Success, owner); got != abi.Int32(42+int32(i)) {
							t.Fatal("native cv/reference overload", got)
						}
					})
				})
			}
			for _, kind := range []string{"self", "pointer"} {
				for cv := 0; cv < 4; cv++ {
					t.Run(fmt.Sprintf("%s_%d", kind, cv), func(t *testing.T) {
						withBox(t, 42, func(owner abi.Value) {
							if got := output(t, fmt.Sprintf("move_%s%d", kind, cv), abi.Ptr(0), cxx.Success, owner); got != owner {
								t.Fatal("borrowed cv result/overloaded operator&", got, owner)
							}
							if got := output(t, "move_live", abi.Int32(0), cxx.Success); got != abi.Int32(1) {
								t.Fatal("borrowed result allocated an owner", got)
							}
						})
					})
				}
			}
			t.Run("constructor", func(t *testing.T) {
				withBox(t, 42, func(owner abi.Value) {
					destination := output(t, "move_construct", abi.Ptr(0), cxx.Success, owner)
					defer func() {
						if got := output(t, "move_delete", destination, cxx.Success); got != abi.Ptr(0) {
							t.Error("destination not consumed", got)
						}
					}()
					if got := output(t, "move_read", abi.Int32(0), cxx.Success, owner); got != abi.Int32(-1) {
						t.Fatal("move constructor did not update source", got)
					}
					if got := output(t, "move_read", abi.Int32(0), cxx.Success, destination); got != abi.Int32(42) {
						t.Fatal("move constructor value", got)
					}
				})
			})
			t.Run("argument", func(t *testing.T) {
				withBox(t, 42, func(owner abi.Value) {
					if got := output(t, "move_consume", abi.Int32(0), cxx.Success, owner); got != abi.Int32(42) {
						t.Fatal("rvalue argument", got)
					}
					if got := output(t, "move_read", abi.Int32(0), cxx.Success, owner); got != abi.Int32(-1) {
						t.Fatal("source was copied or destroyed", got)
					}
				})
			})
			t.Run("exceptions", func(t *testing.T) {
				withBox(t, 13, func(owner abi.Value) {
					if got := output(t, "move_construct", abi.Ptr(0), cxx.Exception, owner); got != abi.Ptr(0) {
						t.Fatal("failed move published owner", got)
					}
					if got := output(t, "move_consume", abi.Int32(999), cxx.Exception, owner); got != abi.Int32(0) {
						t.Fatal("move exception output", got)
					}
					if got := output(t, "move_read", abi.Int32(0), cxx.Success, owner); got != abi.Int32(13) {
						t.Fatal("native strong guarantee", got)
					}
				})
			})
			t.Run("scalar_array", func(t *testing.T) {
				withBox(t, 42, func(owner abi.Value) {
					row := output(t, "move_row", abi.Ptr(0), cxx.Success, owner)
					if got := output(t, "move_sum", abi.Int32(0), cxx.Success, row); got != abi.Int32(42) {
						t.Fatal("array rvalue reference", got)
					}
					number, err := abi.NewNativeValue(abi.TypeDesc{Type: abi.I32}, abi.Int32(40))
					if err != nil {
						t.Fatal(err)
					}
					defer number.Close()
					if err := number.WithAddress(func(address uintptr) error {
						if got := output(t, "move_number", abi.Ptr(0), cxx.Success, abi.Ptr(address)); got != abi.Ptr(address) {
							t.Fatal("scalar reference identity", got)
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if got, err := number.Read(); err != nil || got != abi.Int32(42) {
						t.Fatal("scalar reference mutation", got, err)
					}
				})
			})
			t.Run("nulls_occupied", func(t *testing.T) {
				withBox(t, 42, func(owner abi.Value) {
					for i := 0; i < 12; i++ {
						status, want := int32(cxx.InvalidArgument), int32(0)
						if i < 4 {
							status, want = cxx.Success, -100-int32(i)
						}
						if got := output(t, fmt.Sprintf("move_probe%d", i), abi.Int32(999), status, abi.Ptr(0)); got != abi.Int32(want) {
							t.Fatal("pointer/reference null", i, got)
						}
					}
					if got := output(t, "move_construct", abi.Ptr(0), cxx.InvalidArgument, abi.Ptr(0)); got != abi.Ptr(0) {
						t.Fatal("null move source", got)
					}
					if got := output(t, "move_construct", owner, cxx.InvalidArgument, owner); got != owner {
						t.Fatal("occupied move destination", got)
					}
					if got := output(t, "move_read", abi.Int32(0), cxx.Success, owner); got != abi.Int32(42) {
						t.Fatal("occupied destination moved source", got)
					}
					for cv := 0; cv < 4; cv++ {
						if got := output(t, fmt.Sprintf("move_self%d", cv), owner, cxx.InvalidArgument, abi.Ptr(0)); got != abi.Ptr(0) {
							t.Fatal("stale reference result", got)
						}
						if got := output(t, fmt.Sprintf("move_pointer%d", cv), owner, cxx.Success, abi.Ptr(0)); got != abi.Ptr(0) {
							t.Fatal("nullable cv pointer", got)
						}
					}
					if got := output(t, "move_sum", abi.Int32(999), cxx.InvalidArgument, abi.Ptr(0)); got != abi.Int32(0) {
						t.Fatal("null array reference", got)
					}
				})
			})
		})
	}
}
