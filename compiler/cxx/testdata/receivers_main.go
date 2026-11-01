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
			panic(fmt.Sprintf("receiver status %d: %v", status, err))
		}
	}
	var owner, result unsafe.Pointer
	defer func() {
		check(b.Result_delete(unsafe.Pointer(&result)))
		check(b.Receiver_delete(unsafe.Pointer(&owner)))
		if result != nil || owner != nil {
			panic("receiver/result ownership was not consumed")
		}
	}()
	check(b.Receiver_new(2, unsafe.Pointer(&owner)))
	var value int32
	out := unsafe.Pointer(&value)
	want := func(expected int32) {
		if value != expected {
			panic(fmt.Sprintf("receiver returned %d; want %d", value, expected))
		}
	}
	calls := []func(unsafe.Pointer, int32, unsafe.Pointer) (int32, error){
		b.Receiver_call0, b.Receiver_call1, b.Receiver_call2, b.Receiver_call3,
		b.Receiver_call4, b.Receiver_call5, b.Receiver_call6, b.Receiver_call7,
		b.Receiver_call8, b.Receiver_call9, b.Receiver_call10, b.Receiver_call11,
	}
	for i, call := range calls {
		check(call(owner, 20, out))
		want(22 + int32(i))
	}
	check(b.Receiver_mark(owner, 40))
	check(b.Receiver_operator(owner, 2, out))
	want(42)
	var base unsafe.Pointer
	check(b.Receiver_base(owner, unsafe.Pointer(&base)))
	if base == nil || base == owner {
		panic("missing secondary base adjustment")
	}
	check(b.Base_virtual(base, 2, out))
	want(42)
	value = 999
	if status, err := b.Receiver_fail(owner, out); err != nil || status != cxx.Exception {
		panic("&& exception was not translated")
	}
	want(0)
	check(b.Receiver_read(owner, out))
	want(40)
	check(b.Receiver_take(owner, unsafe.Pointer(&result)))
	check(b.Result_read(result, out))
	want(40)
	check(b.Receiver_read(owner, out))
	want(-1)
	value = 999
	if status, err := b.Receiver_call8(nil, 20, out); err != nil || status != cxx.InvalidArgument {
		panic("null && receiver accepted")
	}
	want(0)
	fmt.Println(42)
}
