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
			panic(fmt.Sprintf("template status %d: %v", status, err))
		}
	}
	var n int32
	out := unsafe.Pointer(&n)
	want := func(v int32) {
		if n != v {
			panic(fmt.Sprintf("template result %d want %d", n, v))
		}
	}
	check(b.Tmpl_add(40, out))
	want(42)
	n = 999
	if status, err := b.Tmpl_add(-1, out); err != nil || status != cxx.Exception || n != 0 {
		panic("template exception")
	}
	if status, err := b.Tmpl_add(40, nil); err != nil || status != cxx.InvalidArgument {
		panic("null output")
	}
	var real float64
	check(b.Tmpl_floating(40, unsafe.Pointer(&real)))
	if real != 42 {
		panic(real)
	}
	check(b.Tmpl_choose(20, out))
	want(42)
	check(b.Tmpl_ordinary(20, out))
	want(-100)
	check(b.Tmpl_pack(20, 22, out))
	want(2)
	check(b.Tmpl_boxed(40, out))
	want(42)
	check(b.Tmpl_signed(out))
	want(42)
	check(b.Tmpl_unsigned(out))
	want(42)
	check(b.Tmpl_sum(20, 2, 20, out))
	want(42)
	var entry, token, echo, lambda unsafe.Pointer
	check(b.Tmpl_factory(unsafe.Pointer(&entry)))
	if entry == nil {
		panic("template factory")
	}
	check(b.Tmpl_invoke(entry, 40, out))
	want(42)
	check(b.Tmpl_invoke(nil, 40, out))
	want(-100)
	check(b.Tmpl_token(unsafe.Pointer(&token)))
	if token == nil {
		panic("template token")
	}
	check(b.Tmpl_pointer(token, unsafe.Pointer(&echo)))
	if echo != token {
		panic("pointer type alias")
	}
	check(b.Tmpl_pointer(nil, unsafe.Pointer(&echo)))
	if echo != nil {
		panic("null template pointer")
	}
	check(b.Tmpl_read(token, out))
	want(42)
	n = 999
	if status, err := b.Tmpl_read(nil, out); err != nil || status != cxx.InvalidArgument || n != 0 {
		panic("template reference")
	}
	check(b.Tmpl_lambda(unsafe.Pointer(&lambda)))
	if lambda == nil {
		panic("lambda view")
	}
	fmt.Println(42)
}
