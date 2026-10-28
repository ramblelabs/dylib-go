//go:build cgo && (linux || darwin || windows)

package cbridge_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"unsafe"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cbridge"
	"github.com/ramblelabs/dylib-go/examples/headerbridge"
)

func TestNativeHeaderLayoutGuards(t *testing.T) {
	compiler := os.Getenv("CLANG")
	if compiler == "" {
		compiler = "clang"
	}
	compiler = tool(t, compiler)
	h, _ := model(t, compiler)
	header, err := filepath.Abs("testdata/exports.h")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := cbridge.Generate(filepath.ToSlash(header), h, []cbridge.Function{{Name: "sum", Symbol: "bridge_sum"}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "facade.c")
	if err := os.WriteFile(src, facade.CSource, 0600); err != nil {
		t.Fatal(err)
	}
	object := filepath.Join(dir, "facade.o")
	args := append([]string{"-std=c17", "-O2", "-ffreestanding", "-fno-stack-protector", "-c", src, "-o", object}, flags()...)
	compile(t, compiler, args...)
	s := dylib.New(dylib.Options{})
	defer s.Close()
	if err := s.Load(object); err != nil {
		t.Fatal(err)
	}
	b := facade.Functions[0]
	symbol, err := s.Resolve(b.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(target uintptr, out, in unsafe.Pointer) (int32, error) {
		return headerbridge.Invoke(symbol, target, out, in)
	}
	if err := b.CheckLayout(invoke); err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{0, 1, 2, 3, 4} {
		t.Run([]string{"magic", "length", "size", "alignment", "offset"}[index], func(t *testing.T) {
			targetCalls := 0
			bad := func(target uintptr, out, in unsafe.Pointer) (int32, error) {
				if target != 0 {
					targetCalls++
				}
				status, err := invoke(target, out, in)
				if err == nil && status == 0 {
					word := (*uint64)(unsafe.Add(out, index*8))
					*word ^= 1
				}
				return status, err
			}
			if _, err := b.Call(bad, 1, abi.Int32(20), abi.Int32(22)); !errors.Is(err, cbridge.ErrLayout) || targetCalls != 0 {
				t.Fatal("bad layout reached target", err, targetCalls)
			}
		})
	}
	t.Run("invoke_error", func(t *testing.T) {
		want := errors.New("facade retired")
		if err := b.CheckLayout(func(uintptr, unsafe.Pointer, unsafe.Pointer) (int32, error) { return 0, want }); !errors.Is(err, want) {
			t.Fatal(err)
		}
	})
	t.Run("native_status", func(t *testing.T) {
		fake := func(target uintptr, out, in unsafe.Pointer) (int32, error) {
			if target != 0 {
				return 7, nil
			}
			return invoke(target, out, in)
		}
		if _, err := b.Call(fake, 1, abi.Int32(20), abi.Int32(22)); !errors.Is(err, cbridge.ErrStatus) {
			t.Fatal(err)
		}
	})
	t.Run("invalid_pointers", func(t *testing.T) {
		if status, err := invoke(0, nil, nil); err != nil || status != 1 {
			t.Fatal(status, err)
		}
	})
	t.Run("parallel_layout", func(t *testing.T) {
		var group sync.WaitGroup
		for range 8 {
			group.Add(1)
			go func() {
				defer group.Done()
				for range 8 {
					if err := b.CheckLayout(invoke); err != nil {
						t.Error(err)
						return
					}
				}
			}()
		}
		group.Wait()
	})
	t.Run("retired_facade", func(t *testing.T) {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := b.CheckLayout(invoke); !errors.Is(err, dylib.ErrClosed) {
			t.Fatal(err)
		}
	})
}
