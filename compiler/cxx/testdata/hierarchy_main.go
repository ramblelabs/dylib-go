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
			panic(fmt.Sprintf("hierarchy status %d: %v", status, err))
		}
	}
	var owner, left, right, direct, viaLeft, viaRight unsafe.Pointer
	defer func() { check(b.Hier_delete(unsafe.Pointer(&owner))) }()
	check(b.Hier_new(20, unsafe.Pointer(&owner)))
	check(b.Hier_alias_left(owner, unsafe.Pointer(&left)))
	check(b.Hier_alias_right(owner, unsafe.Pointer(&right)))
	check(b.Hier_alias_counter(owner, unsafe.Pointer(&direct)))
	check(b.Hier_left_counter(left, unsafe.Pointer(&viaLeft)))
	check(b.Hier_right_counter(right, unsafe.Pointer(&viaRight)))
	if owner == nil || left == nil || right == nil || left == right || direct == nil || direct != viaLeft || direct != viaRight {
		panic("recursive shared virtual base identity")
	}
	var value int32
	out := unsafe.Pointer(&value)
	for _, view := range []unsafe.Pointer{direct, viaLeft, viaRight} {
		check(b.Hier_score(view, 20, out))
		if value != 42 {
			panic("recursive virtual dispatch")
		}
	}
	if status, err := b.Hier_left_counter(nil, unsafe.Pointer(&viaLeft)); err != nil || status != cxx.InvalidArgument || viaLeft != nil {
		panic("null recursive view output")
	}
	var failed unsafe.Pointer
	if status, err := b.Hier_new(-1, unsafe.Pointer(&failed)); err != nil || status != cxx.Exception || failed != nil {
		panic("failed hierarchy construction")
	}
	check(b.Hier_live(out))
	if value != 1 {
		panic("hierarchy owner count")
	}
	check(b.Hier_delete(unsafe.Pointer(&owner)))
	if owner != nil {
		panic("hierarchy owner not consumed")
	}
	check(b.Hier_live(out))
	if value != 0 {
		panic("hierarchy owner leak")
	}
	fmt.Println(42)
}
