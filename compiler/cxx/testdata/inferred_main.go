package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"unsafe"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
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
			panic(fmt.Sprintf("inferred status %d: %v", status, err))
		}
	}
	var n int32
	out := unsafe.Pointer(&n)
	want := func(v int32) {
		if n != v {
			panic(fmt.Sprintf("inferred integer %d want %d", n, v))
		}
	}
	check(b.Auto_add(20, 22, out))
	want(42)
	check(b.Auto_constant(out))
	want(42)
	check(b.Auto_plus(40, out))
	want(42)
	check(b.Auto_checked(40, out))
	want(42)
	n = 999
	if status, err := b.Auto_checked(-1, out); status != cxx.Exception || err != nil || n != 0 {
		panic("inferred exception")
	}
	if status, err := b.Auto_add(20, 22, nil); status != cxx.InvalidArgument || err != nil {
		panic("inferred null output")
	}
	var small int8
	check(b.Auto_narrow(-128, unsafe.Pointer(&small)))
	if small != -128 {
		panic(small)
	}
	var wide uint64
	check(b.Auto_wide(18446744073709551615, unsafe.Pointer(&wide)))
	if wide != 18446744073709551615 {
		panic(wide)
	}
	if runtime.GOOS == "windows" || strconv.IntSize == 32 {
		var value int32
		check(b.Auto_native_long(42, unsafe.Pointer(&value)))
		if value != 42 {
			panic(value)
		}
	} else {
		var value int64
		check(b.Auto_native_long(42, unsafe.Pointer(&value)))
		if value != 42 {
			panic(value)
		}
	}
	var character byte
	check(b.Auto_character(42, unsafe.Pointer(&character)))
	if character != 42 {
		panic(character)
	}
	var code uint16
	check(b.Auto_code(42, unsafe.Pointer(&code)))
	if code != 42 {
		panic(code)
	}
	var flag bool
	check(b.Auto_toggle(true, unsafe.Pointer(&flag)))
	if !flag {
		panic("enum bool")
	}
	check(b.Auto_boolean(true, unsafe.Pointer(&flag)))
	if !flag {
		panic("native bool")
	}
	var plain uint32
	check(b.Auto_plain(42, unsafe.Pointer(&plain)))
	if plain != 42 {
		panic(plain)
	}
	var single float32
	check(b.Auto_floating(42, unsafe.Pointer(&single)))
	if single != 42 {
		panic(single)
	}
	var floating float64
	check(b.Auto_mixed(20, 22, unsafe.Pointer(&floating)))
	if floating != 42 {
		panic(floating)
	}
	check(b.Auto_static(21, unsafe.Pointer(&floating)))
	if floating != 42 {
		panic(floating)
	}
	var pair, echo, entry unsafe.Pointer
	check(b.Auto_pair(unsafe.Pointer(&pair)))
	if pair == nil {
		panic("inferred native object pointer")
	}
	check(b.Auto_echo(pair, unsafe.Pointer(&echo)))
	if pair != echo {
		panic("inferred cv pointer")
	}
	check(b.Auto_store(pair, 42))
	check(b.Auto_read(pair, out))
	want(42)
	check(b.Auto_echo(nil, unsafe.Pointer(&echo)))
	if echo != nil {
		panic("inferred null pointer")
	}
	check(b.Auto_entry(unsafe.Pointer(&entry)))
	if entry == nil {
		panic("inferred native function pointer")
	}
	check(b.Auto_apply(entry, 40, out))
	want(42)
	check(b.Auto_apply(nil, 40, out))
	want(-100)
	fmt.Println(42)
}
