//go:build libffi && cgo && (linux || darwin || windows)

// This example compiles a module exporting the C function add(int32, int32).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/llvm"
)

func call(input string) (result abi.Value, err error) {
	object, err := llvm.Compile(context.Background(), input, llvm.Options{Compiler: os.Getenv("DYLIB_LLC")})
	if object != nil {
		defer func() { err = errors.Join(err, object.Close()) }()
	}
	if err != nil {
		return abi.Value{}, err
	}
	session := dylib.New(dylib.Options{LibraryPaths: object.SourceDirectories})
	defer session.Close()
	if err := session.Load(object.Path); err != nil {
		return abi.Value{}, err
	}
	if err := object.Close(); err != nil {
		return abi.Value{}, err
	}
	if err := session.Link(); err != nil {
		return abi.Value{}, err
	}
	fn, err := session.Bind("add", abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32, abi.I32}})
	if err != nil {
		return abi.Value{}, err
	}
	return fn.Call(abi.Int32(20), abi.Int32(22))
}

func main() {
	if len(os.Args) != 2 {
		panic("usage: llvm <module.ll|module.bc>")
	}
	value, err := call(os.Args[1])
	if err != nil {
		panic(err)
	}
	fmt.Println(int32(value.Bits))
}
