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
	var owner, lambda unsafe.Pointer
	var n int32
	out := unsafe.Pointer(&n)
	want := func(v int32) {
		if n != v {
			panic(fmt.Sprintf("member template result %d want %d", n, v))
		}
	}
	check(b.Tmpl_new(20, unsafe.Pointer(&owner)))
	defer func() { check(b.Tmpl_delete(unsafe.Pointer(&owner))) }()
	check(b.Tmpl_member(owner, 20, out))
	want(42)
	check(b.Tmpl_static(21, out))
	want(42)
	check(b.Tmpl_member_choose(owner, 20, out))
	want(42)
	check(b.Tmpl_member_ordinary(owner, 20, out))
	want(-100)
	check(b.Tmpl_get_lambda(unsafe.Pointer(&lambda)))
	if lambda == nil {
		panic("missing generic lambda")
	}
	check(b.Tmpl_call(lambda, 40, out))
	want(42)
	n = 999
	if status, err := b.Tmpl_call(nil, 40, out); err != nil || status != cxx.InvalidArgument || n != 0 {
		panic("null generic lambda")
	}
	check(b.Tmpl_delete(unsafe.Pointer(&owner)))
	if owner != nil {
		panic("owner retained")
	}
	fmt.Println(42)
}
