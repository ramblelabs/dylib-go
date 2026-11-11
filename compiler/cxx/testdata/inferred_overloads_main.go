package main

import (
	"fmt"
	"os"
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
			panic(fmt.Sprintf("overload status %d: %v", status, err))
		}
	}
	var n int32
	output := unsafe.Pointer(&n)
	integer := func(want int32, status int32, err error) {
		check(status, err)
		if n != want {
			panic(fmt.Sprintf("overload integer %d want %d", n, want))
		}
	}
	status, err := b.Pick_integer(40, output)
	integer(42, status, err)
	status, err = b.Pick_long(39, output)
	integer(42, status, err)
	status, err = b.Pick_zero(output)
	integer(42, status, err)
	status, err = b.Pick_one(42, output)
	integer(42, status, err)
	status, err = b.Pick_template(40, output)
	integer(42, status, err)
	status, err = b.Pick_static_int(42, output)
	integer(42, status, err)
	status, err = b.Pick_checked(40, output)
	integer(42, status, err)
	n = 999
	if status, err := b.Pick_checked(-1, output); status != cxx.Exception || err != nil || n != 0 {
		panic("free overload exception")
	}
	var f64 float64
	floating := unsafe.Pointer(&f64)
	check(b.Pick_double(38, floating))
	if f64 != 42 {
		panic(f64)
	}
	check(b.Pick_ordinary(39, floating))
	if f64 != 42 {
		panic(f64)
	}
	check(b.Pick_static_double(42, floating))
	if f64 != 42 {
		panic(f64)
	}
	var narrow int8
	check(b.Pick_narrow(-128, unsafe.Pointer(&narrow)))
	if narrow != -128 {
		panic(narrow)
	}
	var wide uint64
	check(b.Pick_wide(18446744073709551615, unsafe.Pointer(&wide)))
	if wide != 18446744073709551615 {
		panic(wide)
	}
	var code uint16
	check(b.Pick_enum(42, unsafe.Pointer(&code)))
	if code != 42 {
		panic(code)
	}
	check(b.Pick_store_int(42))
	status, err = b.Pick_read(output)
	integer(42, status, err)
	check(b.Pick_store_double(41))
	status, err = b.Pick_read(output)
	integer(42, status, err)
	var pair, entry, object, base unsafe.Pointer
	check(b.Pick_pair(unsafe.Pointer(&pair)))
	check(b.Pick_entry(unsafe.Pointer(&entry)))
	if pair == nil || entry == nil {
		panic("overload native factory")
	}
	status, err = b.Pick_pointer(pair, output)
	integer(22, status, err)
	status, err = b.Pick_const_pointer(pair, output)
	integer(23, status, err)
	status, err = b.Pick_pointer(nil, output)
	integer(-100, status, err)
	status, err = b.Pick_apply(entry, 40, output)
	integer(42, status, err)
	status, err = b.Pick_apply(nil, 40, output)
	integer(-100, status, err)
	check(b.Pick_new(20, unsafe.Pointer(&object)))
	check(b.Pick_base(object, unsafe.Pointer(&base)))
	if object == nil || base == nil || base == object {
		panic("overload adjusted base")
	}
	calls := []func(unsafe.Pointer, int32, unsafe.Pointer) (int32, error){
		b.Pick_q0, b.Pick_q1, b.Pick_q2, b.Pick_q3, b.Pick_q4, b.Pick_q5, b.Pick_q6, b.Pick_q7, b.Pick_q8, b.Pick_q9, b.Pick_q10, b.Pick_q11,
		b.Pick_n0, b.Pick_n1, b.Pick_n2, b.Pick_n3, b.Pick_n4, b.Pick_n5, b.Pick_n6, b.Pick_n7, b.Pick_n8, b.Pick_n9, b.Pick_n10, b.Pick_n11,
	}
	for i, call := range calls {
		status, err = call(object, 22, output)
		integer(42+int32(i%12), status, err)
	}
	check(b.Pick_score_double(object, 18, floating))
	if f64 != 42 {
		panic(f64)
	}
	status, err = b.Pick_zero_mutable(object, output)
	integer(42, status, err)
	status, err = b.Pick_zero_const(object, output)
	integer(43, status, err)
	status, err = b.Pick_operator_int(object, 22, output)
	integer(42, status, err)
	check(b.Pick_operator_double(object, 22, floating))
	if f64 != 42 {
		panic(f64)
	}
	status, err = b.Pick_plus(object, 40, output)
	integer(42, status, err)
	status, err = b.Pick_checked_int(object, 22, output)
	integer(42, status, err)
	n = 999
	if status, err := b.Pick_checked_int(object, -1, output); status != cxx.Exception || err != nil || n != 0 {
		panic("member overload exception")
	}
	check(b.Pick_checked_double(object, 22, floating))
	if f64 != 42 {
		panic(f64)
	}
	status, err = b.Pick_native_int(object, 40, output)
	integer(42, status, err)
	status, err = b.Pick_native_long(object, 39, output)
	integer(42, status, err)
	status, err = b.Pick_pointer_mutable(object, pair, output)
	integer(22, status, err)
	status, err = b.Pick_pointer_const(object, pair, output)
	integer(23, status, err)
	status, err = b.Pick_apply_member(object, entry, 40, output)
	integer(42, status, err)
	status, err = b.Pick_inherited_int(object, -58, output)
	integer(42, status, err)
	check(b.Pick_inherited_double(object, -59, floating))
	if f64 != 42 {
		panic(f64)
	}
	status, err = b.Pick_virtual_int(base, 17, output)
	integer(42, status, err)
	check(b.Pick_virtual_double(base, 18, floating))
	if f64 != 42 {
		panic(f64)
	}
	status, err = b.Pick_shared(object, 2, output)
	integer(42, status, err)
	check(b.Pick_mixed_ordinary(object, 39, floating))
	if f64 != 42 {
		panic(f64)
	}
	status, err = b.Pick_mixed_template(object, 40, output)
	integer(42, status, err)
	n = 999
	if status, err := b.Pick_q0(nil, 22, output); status != cxx.InvalidArgument || err != nil || n != 0 {
		panic("overload null receiver")
	}
	if status, err := b.Pick_mark_int(nil, 42); status != cxx.InvalidArgument || err != nil {
		panic("overload void null receiver")
	}
	if status, err := b.Pick_q0(object, 22, nil); status != cxx.InvalidArgument || err != nil {
		panic("overload null output")
	}
	check(b.Pick_mark_int(object, 42))
	status, err = b.Pick_read_member(object, output)
	integer(42, status, err)
	check(b.Pick_mark_double(object, 41))
	status, err = b.Pick_read_member(object, output)
	integer(42, status, err)
	check(b.Pick_delete(unsafe.Pointer(&object)))
	if object != nil {
		panic("overload owner not cleared")
	}
	check(b.Pick_delete(unsafe.Pointer(&object)))
	fmt.Println(42)
}
