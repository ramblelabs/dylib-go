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
			panic(fmt.Sprintf("member template status %d: %v", status, err))
		}
	}
	var owner, selected, lambda unsafe.Pointer
	var n int32
	out := unsafe.Pointer(&n)
	want := func(v int32) {
		if n != v {
			panic(fmt.Sprintf("member template result %d want %d", n, v))
		}
	}
	check(b.Mt_new(20, unsafe.Pointer(&owner)))
	defer func() { check(b.Mt_delete(unsafe.Pointer(&owner))) }()
	check(b.Mt_live(out))
	want(1)
	check(b.Mt_add(owner, 20, out))
	want(42)
	check(b.Mt_move(owner, 22, out))
	want(42)
	var floating float64
	check(b.Mt_float(21, unsafe.Pointer(&floating)))
	if floating != 42 {
		panic(floating)
	}
	check(b.Mt_box(owner, 22, out))
	want(42)
	check(b.Mt_pack(owner, 20, 22, out))
	want(22)
	check(b.Mt_sum(owner, 10, 2, 10, out))
	want(42)
	check(b.Mt_convert(owner, out))
	want(20)
	n = 999
	if status, err := b.Mt_add(owner, -1, out); err != nil || status != cxx.Exception || n != 0 {
		panic("member template exception")
	}
	n = 999
	if status, err := b.Mt_add(nil, 20, out); err != nil || status != cxx.InvalidArgument || n != 0 {
		panic("null member template receiver")
	}
	if status, err := b.Mt_add(owner, 20, nil); err != nil || status != cxx.InvalidArgument {
		panic("null member template output")
	}
	check(b.Mt_selected_new(20, unsafe.Pointer(&selected)))
	defer func() { check(b.Mt_selected_delete(unsafe.Pointer(&selected))) }()
	check(b.Mt_selected_add(selected, 22, out))
	want(42)
	check(b.Mt_lambda(unsafe.Pointer(&lambda)))
	if lambda == nil {
		panic("missing generic lambda")
	}
	check(b.Mt_call(lambda, 22, out))
	want(42)
	n = 999
	if status, err := b.Mt_call(nil, 22, out); err != nil || status != cxx.InvalidArgument || n != 0 {
		panic("null generic lambda")
	}
	check(b.Mt_delete(unsafe.Pointer(&owner)))
	if owner != nil {
		panic("owner retained")
	}
	check(b.Mt_live(out))
	want(0)
	check(b.Mt_selected_delete(unsafe.Pointer(&selected)))
	if selected != nil {
		panic("selected owner retained")
	}
	fmt.Println(42)
}
