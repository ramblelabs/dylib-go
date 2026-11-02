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
			panic(fmt.Sprintf("conversion status %d: %v", status, err))
		}
	}
	var owner, bad, owned unsafe.Pointer
	defer func() {
		check(b.Converted_delete(unsafe.Pointer(&owned)))
		check(b.Convert_delete(unsafe.Pointer(&bad)))
		check(b.Convert_delete(unsafe.Pointer(&owner)))
	}()
	check(b.Convert_new(42, unsafe.Pointer(&owner)))
	var boolean bool
	check(b.Convert_bool(owner, unsafe.Pointer(&boolean)))
	if !boolean {
		panic("explicit bool conversion")
	}
	var value int32
	out := unsafe.Pointer(&value)
	want := func(expected int32) {
		if value != expected {
			panic(fmt.Sprintf("conversion result %d; want %d", value, expected))
		}
	}
	check(b.Convert_lvalue(owner, out))
	want(42)
	check(b.Convert_rvalue(owner, out))
	want(43)
	var floating float64
	check(b.Convert_float(owner, unsafe.Pointer(&floating)))
	if floating != 42.5 {
		panic("floating conversion")
	}
	var address, reference, rvalue, base, entry unsafe.Pointer
	check(b.Convert_address(owner, unsafe.Pointer(&address)))
	check(b.Convert_read(address, out))
	want(42)
	check(b.Convert_reference(owner, unsafe.Pointer(&reference)))
	check(b.Convert_rvalue_reference(owner, unsafe.Pointer(&rvalue)))
	if reference == nil || rvalue != reference {
		panic("conversion reference/overloaded operator&")
	}
	check(b.Converted_read(reference, out))
	want(42)
	check(b.Convert_base(owner, unsafe.Pointer(&base)))
	if base == nil || base == owner {
		panic("conversion secondary base adjustment")
	}
	check(b.Convert_virtual(base, out))
	want(42)
	check(b.Convert_entry(owner, unsafe.Pointer(&entry)))
	check(b.Convert_invoke(entry, 20, 22, out))
	want(42)
	occupied := owner
	if status, err := b.Convert_owned(owner, unsafe.Pointer(&occupied)); err != nil || status != cxx.InvalidArgument || occupied != owner {
		panic("occupied conversion destination")
	}
	check(b.Converted_read(reference, out))
	want(42)
	check(b.Convert_owned(owner, unsafe.Pointer(&owned)))
	check(b.Converted_read(owned, out))
	want(42)
	check(b.Converted_read(reference, out))
	want(-1)
	check(b.Convert_new(13, unsafe.Pointer(&bad)))
	var failed unsafe.Pointer
	if status, err := b.Convert_owned(bad, unsafe.Pointer(&failed)); err != nil || status != cxx.Exception || failed != nil {
		panic("conversion exception publication")
	}
	boolean = true
	if status, err := b.Convert_bool(nil, unsafe.Pointer(&boolean)); err != nil || status != cxx.InvalidArgument || boolean {
		panic("null conversion output clearing")
	}
	check(b.Converted_delete(unsafe.Pointer(&owned)))
	check(b.Convert_delete(unsafe.Pointer(&bad)))
	check(b.Convert_delete(unsafe.Pointer(&owner)))
	if owned != nil || bad != nil || owner != nil {
		panic("conversion ownership not consumed")
	}
	check(b.Converted_live(out))
	want(0)
	fmt.Println(42)
}
