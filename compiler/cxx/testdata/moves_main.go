package main

import (
	"fmt"
	"os"
	"unsafe"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
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
			panic(fmt.Sprintf("move status %d: %v", status, err))
		}
	}
	var owner, destination, bad unsafe.Pointer
	defer func() {
		check(b.Move_delete(unsafe.Pointer(&bad)))
		check(b.Move_delete(unsafe.Pointer(&destination)))
		check(b.Move_delete(unsafe.Pointer(&owner)))
	}()
	check(b.Move_new(42, unsafe.Pointer(&owner)))
	var value int32
	out := unsafe.Pointer(&value)
	want := func(expected int32) {
		if value != expected {
			panic(fmt.Sprintf("move result %d; want %d", value, expected))
		}
	}
	probes := []func(unsafe.Pointer, unsafe.Pointer) (int32, error){
		b.Move_probe0, b.Move_probe1, b.Move_probe2, b.Move_probe3, b.Move_probe4, b.Move_probe5,
		b.Move_probe6, b.Move_probe7, b.Move_probe8, b.Move_probe9, b.Move_probe10, b.Move_probe11,
	}
	for i, call := range probes {
		check(call(owner, out))
		want(42 + int32(i))
		value = 999
		status, err := call(nil, out)
		if i < 4 {
			check(status, err)
			want(-100 - int32(i))
		} else {
			if err != nil || status != cxx.InvalidArgument {
				panic("null reference accepted")
			}
			want(0)
		}
	}
	views := []func(unsafe.Pointer, unsafe.Pointer) (int32, error){b.Move_self0, b.Move_self1, b.Move_self2, b.Move_self3, b.Move_pointer0, b.Move_pointer1, b.Move_pointer2, b.Move_pointer3}
	for _, call := range views {
		var view unsafe.Pointer
		check(call(owner, unsafe.Pointer(&view)))
		if view != owner {
			panic("borrowed reference identity/overloaded operator&")
		}
	}
	check(b.Move_construct(owner, unsafe.Pointer(&destination)))
	check(b.Move_read(owner, out))
	want(-1)
	check(b.Move_read(destination, out))
	want(42)
	check(b.Move_consume(destination, out))
	want(42)
	check(b.Move_read(destination, out))
	want(-1)
	check(b.Move_new(13, unsafe.Pointer(&bad)))
	var failed unsafe.Pointer
	if status, err := b.Move_construct(bad, unsafe.Pointer(&failed)); err != nil || status != cxx.Exception || failed != nil {
		panic("move constructor exception publication")
	}
	value = 999
	if status, err := b.Move_consume(bad, out); err != nil || status != cxx.Exception {
		panic("move argument exception")
	}
	want(0)
	check(b.Move_read(bad, out))
	want(13)
	var row unsafe.Pointer
	check(b.Move_row(owner, unsafe.Pointer(&row)))
	check(b.Move_sum(row, out))
	want(42)
	number, err := abi.NewNativeValue(abi.TypeDesc{Type: abi.I32}, abi.Int32(40))
	if err != nil {
		panic(err)
	}
	defer number.Close()
	if err := number.WithAddress(func(address uintptr) error {
		var view unsafe.Pointer
		check(b.Move_number(unsafe.Pointer(address), unsafe.Pointer(&view)))
		if uintptr(view) != address {
			panic("scalar rvalue reference identity")
		}
		return nil
	}); err != nil {
		panic(err)
	}
	if got, err := number.Read(); err != nil || got != abi.Int32(42) {
		panic("scalar rvalue reference mutation")
	}
	check(b.Move_delete(unsafe.Pointer(&bad)))
	check(b.Move_delete(unsafe.Pointer(&destination)))
	check(b.Move_delete(unsafe.Pointer(&owner)))
	if bad != nil || destination != nil || owner != nil {
		panic("move ownership not consumed")
	}
	check(b.Move_live(out))
	want(0)
	fmt.Println(42)
}
