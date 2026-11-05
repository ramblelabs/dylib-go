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
			panic(fmt.Sprintf("free function status %d: %v", status, err))
		}
	}
	var n int32
	out := unsafe.Pointer(&n)
	want := func() {
		if n != 42 {
			panic(fmt.Sprintf("free result %d", n))
		}
	}
	check(b.Free_global_add(20, 22, out))
	want()
	check(b.Free_add(-1, 43, out))
	want()
	var real float64
	check(b.Free_floating(20.25, 21.75, unsafe.Pointer(&real)))
	if real != 42 {
		panic(real)
	}
	check(b.Free_internal(40, out))
	want()
	check(b.Free_c(40, out))
	want()
	check(b.Free_sum(20, 2, 20, out))
	want()
	n = 999
	if status, err := b.Free_sum(-1, 2, 20, out); err != nil || status != cxx.Exception || n != 0 {
		panic("exception output")
	}
	if status, err := b.Free_add(20, 22, nil); err != nil || status != cxx.InvalidArgument {
		panic("null output")
	}
	var entry, object unsafe.Pointer
	check(b.Free_factory(unsafe.Pointer(&entry)))
	if entry == nil {
		panic("function result")
	}
	check(b.Free_invoke(entry, 20, 22, out))
	want()
	check(b.Free_invoke(nil, 20, 22, out))
	if n != -100 {
		panic("null function argument")
	}
	check(b.Free_borrowed(unsafe.Pointer(&object)))
	if object == nil {
		panic("borrowed reference")
	}
	check(b.Free_read(object, 22, out))
	want()
	n = 999
	if status, err := b.Free_read(nil, 22, out); err != nil || status != cxx.InvalidArgument || n != 0 {
		panic("null reference argument")
	}
	check(b.Free_assign(object, 40))
	check(b.Free_read(object, 2, out))
	want()
	check(b.Free_assign(object, 20))
	fmt.Println(42)
}
