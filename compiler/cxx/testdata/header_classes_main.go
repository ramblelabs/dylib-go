package main

import (
	"fmt"
	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
	"os"
	"unsafe"
)

func main() {
	s := dylib.New(dylib.Options{})
	defer s.Close()
	if err := s.Load(os.Args[1]); err != nil {
		panic(err)
	}
	b, err := NewBindings(s)
	if err != nil {
		panic(err)
	}
	check := func(status int32, err error) {
		if status != cxx.Success || err != nil {
			panic(fmt.Sprintf("header class status %d: %v", status, err))
		}
	}
	var owner, lambda unsafe.Pointer
	var n int32
	out := unsafe.Pointer(&n)
	want := func(expected int32) {
		if n != expected {
			panic(fmt.Sprintf("header class result %d want %d", n, expected))
		}
	}
	check(b.Hd_new(20, unsafe.Pointer(&owner)))
	defer func() { check(b.Hd_delete(unsafe.Pointer(&owner))) }()
	check(b.Hd_live(out))
	want(1)
	check(b.Hd_member(owner, 22, out))
	want(42)
	check(b.Hd_twice(owner, 21, out))
	want(42)
	check(b.Hd_lambda(unsafe.Pointer(&lambda)))
	if lambda == nil {
		panic("missing discovered lambda")
	}
	check(b.Hd_call(lambda, 22, out))
	want(42)
	n = 999
	if status, err := b.Hd_member(nil, 22, out); status != cxx.InvalidArgument || err != nil || n != 0 {
		panic("null discovered receiver")
	}
	n = 999
	if status, err := b.Hd_call(nil, 22, out); status != cxx.InvalidArgument || err != nil || n != 0 {
		panic("null discovered lambda")
	}
	check(b.Hd_delete(unsafe.Pointer(&owner)))
	if owner != nil {
		panic("header owner retained")
	}
	check(b.Hd_live(out))
	want(0)
	fmt.Println(42)
}
