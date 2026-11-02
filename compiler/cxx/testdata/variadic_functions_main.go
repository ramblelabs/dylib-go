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
			panic(fmt.Sprintf("variadic function status %d: %v", status, err))
		}
	}
	var owner, entry, returned, converted unsafe.Pointer
	defer func() { check(b.Varfn_delete(unsafe.Pointer(&owner))) }()
	check(b.Varfn_factory(unsafe.Pointer(&entry)))
	if entry == nil {
		panic("null variadic entry")
	}
	check(b.Varfn_new(entry, unsafe.Pointer(&owner)))
	var value int32
	out := unsafe.Pointer(&value)
	want := func(n int32) {
		if value != n {
			panic(fmt.Sprintf("variadic function result %d; want %d", value, n))
		}
	}
	check(b.Varfn_call(owner, 20, 2, 20, out))
	want(42)
	check(b.Varfn_get(owner, unsafe.Pointer(&returned)))
	check(b.Varfn_convert(owner, unsafe.Pointer(&converted)))
	if returned != entry || converted != entry {
		panic("variadic entry identity")
	}
	check(b.Varfn_invoke(returned, 20, 2, 20, out))
	want(42)
	check(b.Varfn_tail(20, converted, out))
	want(42)
	check(b.Varfn_invoke(nil, 20, 2, 20, out))
	want(-100)
	check(b.Varfn_tail(20, nil, out))
	want(-100)
	value = 999
	if status, err := b.Varfn_call(owner, -1, 2, 20, out); err != nil || status != cxx.Exception || value != 0 {
		panic("variadic function exception")
	}
	var bad unsafe.Pointer
	if status, err := b.Varfn_new(nil, unsafe.Pointer(&bad)); err != nil || status != cxx.Exception || bad != nil {
		panic("null constructor entry")
	}
	if status, err := b.Varfn_get(nil, unsafe.Pointer(&bad)); err != nil || status != cxx.InvalidArgument || bad != nil {
		panic("null receiver")
	}
	check(b.Varfn_delete(unsafe.Pointer(&owner)))
	if owner != nil {
		panic("variadic function owner not consumed")
	}
	// The entry belongs to the loaded provider, rather than the destroyed Holder.
	check(b.Varfn_invoke(returned, 20, 2, 20, out))
	want(42)
	check(b.Varfn_live(out))
	want(0)
	fmt.Println(42)
}
