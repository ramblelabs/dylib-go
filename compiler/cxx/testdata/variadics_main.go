package main

import (
	"fmt"
	"os"
	"unsafe"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

func main() {
	session := dylib.New(dylib.Options{})
	defer session.Close()
	if err := session.Load(os.Args[1]); err != nil {
		panic(err)
	}
	b, err := NewBindings(session)
	if err != nil {
		panic(err)
	}
	check := func(status int32, err error) {
		if err != nil || status != cxx.Success {
			panic(fmt.Sprintf("variadic status %d: %v", status, err))
		}
	}
	var owner unsafe.Pointer
	defer func() { check(b.Var_delete(unsafe.Pointer(&owner))) }()
	check(b.Var_new(20, unsafe.Pointer(&owner)))
	var value int32
	out := unsafe.Pointer(&value)
	want := func(n int32) {
		if value != n {
			panic(fmt.Sprintf("variadic result %d; want %d", value, n))
		}
	}
	check(b.Var_empty(owner, 0, out))
	want(20)
	check(b.Var_promoted(owner, 5, -3, 4, -5, 25, true, out))
	want(42)
	check(b.Var_spill(owner, 12, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 11, out))
	want(42)
	var floating float64
	check(b.Var_floats(12, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 11, unsafe.Pointer(&floating)))
	if floating != 42 {
		panic("floating variadic register/stack promotion")
	}
	check(b.Var_mixed(owner, 7, 4000000000, -5000000000, 10000000000, 1.25, 2.5, unsafe.Pointer(&floating)))
	if floating != 42 {
		panic("mixed variadic widths/cv receiver")
	}
	check(b.Var_call(owner, 2, 20, out))
	want(42)
	var address, entry, base unsafe.Pointer
	check(b.Var_address(owner, 7, 7, unsafe.Pointer(&address)))
	check(b.Var_read(address, out))
	want(20)
	check(b.Var_prefix(address, 1, 22, out))
	want(42)
	if status, err := b.Var_prefix(nil, 1, 22, out); err != nil || status != cxx.InvalidArgument || value != 0 {
		panic("null fixed reference")
	}
	check(b.Var_entry(unsafe.Pointer(&entry)))
	check(b.Var_pointers(22, address, entry, out))
	want(42)
	check(b.Var_pointers(22, nil, entry, out))
	want(-100)
	check(b.Var_apply(entry, 0, 20, 22, out))
	want(42)
	check(b.Var_base(owner, unsafe.Pointer(&base)))
	if base == nil || base == owner {
		panic("secondary variadic base adjustment")
	}
	check(b.Var_virtual(base, 2, -5, 27, out))
	want(42)
	value = 999
	if status, err := b.Var_empty(owner, -1, out); err != nil || status != cxx.Exception || value != 0 {
		panic("variadic exception clearing")
	}
	if status, err := b.Var_empty(nil, 0, out); err != nil || status != cxx.InvalidArgument || value != 0 {
		panic("null variadic receiver")
	}
	if status, err := b.Var_empty(owner, 0, nil); err != nil || status != cxx.InvalidArgument {
		panic("null variadic output")
	}
	check(b.Var_set(owner, 20, 22))
	check(b.Var_empty(owner, 0, out))
	want(42)
	check(b.Var_delete(unsafe.Pointer(&owner)))
	if owner != nil {
		panic("variadic owner not consumed")
	}
	check(b.Var_live(out))
	want(0)
	fmt.Println(42)
}
