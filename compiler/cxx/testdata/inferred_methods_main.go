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
			panic(fmt.Sprintf("inferred member status %d: %v", status, err))
		}
	}
	var object, base, shared, view, entry unsafe.Pointer
	check(b.Auto_new(20, unsafe.Pointer(&object)))
	check(b.Auto_base(object, unsafe.Pointer(&base)))
	check(b.Auto_shared(object, unsafe.Pointer(&shared)))
	if object == nil || base == nil || shared == nil || base == object || shared == object {
		panic("missing adjusted native base")
	}
	var n int32
	output := unsafe.Pointer(&n)
	calls := []func(unsafe.Pointer, int32, unsafe.Pointer) (int32, error){
		b.Auto_q0, b.Auto_q1, b.Auto_q2, b.Auto_q3, b.Auto_q4, b.Auto_q5, b.Auto_q6, b.Auto_q7, b.Auto_q8, b.Auto_q9, b.Auto_q10, b.Auto_q11,
		b.Auto_n0, b.Auto_n1, b.Auto_n2, b.Auto_n3, b.Auto_n4, b.Auto_n5, b.Auto_n6, b.Auto_n7, b.Auto_n8, b.Auto_n9, b.Auto_n10, b.Auto_n11,
	}
	for i, call := range calls {
		check(call(object, 22, output))
		if n != 42+int32(i%12) {
			panic(fmt.Sprintf("receiver %d: %d", i, n))
		}
	}
	integer := func(want int32, status int32, err error) {
		check(status, err)
		if n != want {
			panic(fmt.Sprintf("member integer %d want %d", n, want))
		}
	}
	// Method expressions preserve inherited and virtual-base adjustments in C++.
	status, err := b.Auto_inherited(object, -58, output)
	integer(42, status, err)
	// The shared base method is selected through the complete Receiver handle.
	status, err = b.Auto_shared_call(object, 2, output)
	integer(42, status, err)
	status, err = b.Auto_score(base, 22, output)
	integer(42, status, err)
	status, err = b.Auto_operator(object, 22, output)
	integer(42, status, err)
	status, err = b.Auto_plus(object, 40, output)
	integer(42, status, err)
	n = 999
	status, err = b.Auto_q0(nil, 22, output)
	if status != cxx.InvalidArgument || err != nil || n != 0 {
		panic("member null receiver")
	}
	if status, err := b.Auto_mark(nil, 42); status != cxx.InvalidArgument || err != nil {
		panic("void member null receiver")
	}
	if status, err := b.Auto_q0(object, 22, nil); status != cxx.InvalidArgument || err != nil {
		panic("member null output")
	}
	n = 999
	status, err = b.Auto_fail(object, output)
	if status != cxx.Exception || err != nil || n != 0 {
		panic("member exception output")
	}
	var small int8
	check(b.Auto_narrow(object, -128, unsafe.Pointer(&small)))
	if small != -128 {
		panic(small)
	}
	var wide uint64
	check(b.Auto_wide(object, 18446744073709551615, unsafe.Pointer(&wide)))
	if wide != 18446744073709551615 {
		panic(wide)
	}
	if runtime.GOOS == "windows" || strconv.IntSize == 32 {
		var v int32
		check(b.Auto_native_long(object, 42, unsafe.Pointer(&v)))
		if v != 42 {
			panic(v)
		}
	} else {
		var v int64
		check(b.Auto_native_long(object, 42, unsafe.Pointer(&v)))
		if v != 42 {
			panic(v)
		}
	}
	var character byte
	check(b.Auto_character(object, 42, unsafe.Pointer(&character)))
	if character != 42 {
		panic(character)
	}
	var code uint16
	check(b.Auto_code(object, 42, unsafe.Pointer(&code)))
	if code != 42 {
		panic(code)
	}
	var flag bool
	check(b.Auto_boolean(object, true, unsafe.Pointer(&flag)))
	if !flag {
		panic("member boolean")
	}
	var mixed float64
	check(b.Auto_mixed(object, 20, 22, unsafe.Pointer(&mixed)))
	if mixed != 42 {
		panic(mixed)
	}
	check(b.Auto_self(object, unsafe.Pointer(&view)))
	if view != object {
		panic("member native pointer")
	}
	check(b.Auto_entry(object, unsafe.Pointer(&entry)))
	if entry == nil {
		panic("member function pointer")
	}
	status, err = b.Auto_apply(object, entry, 40, output)
	integer(42, status, err)
	status, err = b.Auto_apply(object, nil, 40, output)
	integer(-100, status, err)
	check(b.Auto_mark(object, 42))
	status, err = b.Auto_read(object, output)
	integer(42, status, err)
	status, err = b.Auto_take(object, output)
	integer(42, status, err)
	status, err = b.Auto_read(object, output)
	integer(-1, status, err)
	// An && call leaves ownership intact; explicit deletion consumes the owner.
	check(b.Auto_delete(unsafe.Pointer(&object)))
	if object != nil {
		panic("member owner not cleared")
	}
	check(b.Auto_delete(unsafe.Pointer(&object)))
	fmt.Println(42)
}
