//go:build cgo && (darwin || linux || windows)

// This example lets C retain a leased record across separate calls.
package main

/*
#include <stdint.h>
typedef struct { int32_t a, b; } NativePair;
static void save_pair(uintptr_t entry, uintptr_t pair) {
    ((void (*)(NativePair *))entry)((NativePair *)pair);
}
static int32_t sum_pair(uintptr_t entry) {
    return ((int32_t (*)(void))entry)();
}
*/
import "C"

import (
	"fmt"
	"os"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
)

func run(path string) error {
	session := dylib.New(dylib.Options{})
	defer session.Close()
	if err := session.Load(path); err != nil {
		return err
	}
	store, err := session.Resolve("native_value_save_pair")
	if err != nil {
		return err
	}
	sum, err := session.Resolve("native_value_sum_pair")
	if err != nil {
		return err
	}
	description := abi.TypeDesc{Type: abi.Struct, Fields: []abi.Field{
		{Name: "a", Type: abi.TypeDesc{Type: abi.I32}},
		{Name: "b", Type: abi.TypeDesc{Type: abi.I32}},
	}}
	initial, err := abi.StructValue(description, abi.Int32(20), abi.Int32(22))
	if err != nil {
		return err
	}
	owner, err := abi.NewNativeValue(description, initial)
	if err != nil {
		return err
	}
	defer owner.Close()
	return store.WithAddress(func(entry uintptr) error {
		return owner.WithAddress(func(address uintptr) error {
			C.save_pair(C.uintptr_t(entry), C.uintptr_t(address))
			// Unregister before releasing the storage and code leases.
			defer C.save_pair(C.uintptr_t(entry), 0)
			return sum.WithAddress(func(entry uintptr) error {
				fmt.Println(int32(C.sum_pair(C.uintptr_t(entry))))
				return nil
			})
		})
	})
}

func main() {
	if len(os.Args) != 2 {
		panic("usage: nativevalue <library>")
	}
	if err := run(os.Args[1]); err != nil {
		panic(err)
	}
}
