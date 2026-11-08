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
	var n int32
	out := unsafe.Pointer(&n)
	check := func(status int32, err error) {
		if status != cxx.Success || err != nil || n != 42 {
			panic(fmt.Sprintf("header function status %d result %d: %v", status, n, err))
		}
	}
	check(b.Hd_add(20, 22, out))
	check(b.Hd_choose(40, out))
	check(b.Hd_local(40, out))
	check(b.Hd_c(40, out))
	check(b.Hd_scale(21, out))
	check(b.Hd_included(40, out))
	check(b.Hd_offset(40, out))
	check(b.Hd_plus(40, out))
	var floating float64
	if status, err := b.Hd_float(40, unsafe.Pointer(&floating)); status != cxx.Success || err != nil || floating != 42 {
		panic("header floating overload")
	}
	if status, err := b.Hd_pack(20, 22, out); status != cxx.Success || err != nil || n != 2 {
		panic("header template pack")
	}
	n = 999
	if status, err := b.Hd_plus(-1, out); status != cxx.Exception || err != nil || n != 0 {
		panic("header template exception")
	}
	if status, err := b.Hd_add(20, 22, nil); status != cxx.InvalidArgument || err != nil {
		panic("header null output")
	}
	fmt.Println(42)
}
