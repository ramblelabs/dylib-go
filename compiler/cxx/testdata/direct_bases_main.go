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
			panic(fmt.Sprintf("direct base status %d: %v", status, err))
		}
	}
	var owner, secondary, virtual unsafe.Pointer
	defer func() { check(b.Direct_delete(unsafe.Pointer(&owner))) }()
	check(b.Direct_new(20, unsafe.Pointer(&owner)))
	check(b.Direct_counter(owner, unsafe.Pointer(&secondary)))
	check(b.Direct_virtual(owner, unsafe.Pointer(&virtual)))
	if owner == nil || secondary == nil || secondary == owner || virtual == nil || virtual == owner || secondary == virtual {
		panic("native secondary/virtual adjustment")
	}
	var value int32
	out := unsafe.Pointer(&value)
	check(b.Direct_score(secondary, 21, out))
	if value != 42 {
		panic("discovered secondary virtual dispatch")
	}
	check(b.Direct_read(virtual, out))
	if value != 42 {
		panic("discovered virtual base state")
	}
	if status, err := b.Direct_counter(nil, unsafe.Pointer(&secondary)); err != nil || status != cxx.InvalidArgument || secondary != nil {
		panic("null base output")
	}
	check(b.Direct_delete(unsafe.Pointer(&owner)))
	if owner != nil {
		panic("owner not consumed")
	}
	check(b.Direct_live(out))
	if value != 0 {
		panic("direct base owner leak")
	}
	fmt.Println(42)
}
