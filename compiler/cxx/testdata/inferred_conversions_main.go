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
			panic(fmt.Sprintf("inferred conversion status %d: %v", status, err))
		}
	}
	var receivers [4]unsafe.Pointer
	create := []func(int32, unsafe.Pointer) (int32, error){b.Convert_q_Plain_new, b.Convert_q_Qualified_new, b.Convert_n_NPlain_new, b.Convert_n_NQualified_new}
	destroy := []func(unsafe.Pointer) (int32, error){b.Convert_q_Plain_delete, b.Convert_q_Qualified_delete, b.Convert_n_NPlain_delete, b.Convert_n_NQualified_delete}
	for i, call := range create {
		check(call(42, unsafe.Pointer(&receivers[i])))
	}
	calls := []func(unsafe.Pointer, unsafe.Pointer) (int32, error){
		b.Convert_auto_q0, b.Convert_auto_q1, b.Convert_auto_q2, b.Convert_auto_q3, b.Convert_auto_q4, b.Convert_auto_q5, b.Convert_auto_q6, b.Convert_auto_q7, b.Convert_auto_q8, b.Convert_auto_q9, b.Convert_auto_q10, b.Convert_auto_q11,
		b.Convert_auto_n0, b.Convert_auto_n1, b.Convert_auto_n2, b.Convert_auto_n3, b.Convert_auto_n4, b.Convert_auto_n5, b.Convert_auto_n6, b.Convert_auto_n7, b.Convert_auto_n8, b.Convert_auto_n9, b.Convert_auto_n10, b.Convert_auto_n11,
	}
	var n int32
	for i, call := range calls {
		owner := 2 * (i / 12)
		if i%12 >= 4 {
			owner++
		}
		check(call(receivers[owner], unsafe.Pointer(&n)))
		if n != 42+int32(i%12) {
			panic(fmt.Sprintf("conversion receiver %d: %d", i, n))
		}
	}
	for i, call := range destroy {
		check(call(unsafe.Pointer(&receivers[i])))
		if receivers[i] != nil {
			panic("conversion receiver owner not cleared")
		}
	}
	check(b.Convert_forms_alive(unsafe.Pointer(&n)))
	if n != 0 {
		panic("conversion receiver owners still alive")
	}
	var object, base, entry, pointer, constant, self, template unsafe.Pointer
	check(b.Convert_auto_new(42, unsafe.Pointer(&object)))
	check(b.Convert_auto_base(object, unsafe.Pointer(&base)))
	if base == nil || base == object {
		panic("missing adjusted conversion base")
	}
	check(b.Convert_auto_virtual(base, unsafe.Pointer(&n)))
	if n != 42 {
		panic(n)
	}
	check(b.Convert_auto_int(object, unsafe.Pointer(&n)))
	if n != 42 {
		panic(n)
	}
	var flag bool
	check(b.Convert_auto_bool(object, unsafe.Pointer(&flag)))
	if !flag {
		panic("explicit bool conversion")
	}
	var narrow int8
	check(b.Convert_auto_narrow(object, unsafe.Pointer(&narrow)))
	if narrow != -128 {
		panic(narrow)
	}
	var wide uint64
	check(b.Convert_auto_wide(object, unsafe.Pointer(&wide)))
	if wide != ^uint64(0) {
		panic(wide)
	}
	var code uint16
	check(b.Convert_auto_code(object, unsafe.Pointer(&code)))
	if code != 42 {
		panic(code)
	}
	if runtime.GOOS == "windows" || strconv.IntSize == 32 {
		var value int32
		check(b.Convert_auto_long(object, unsafe.Pointer(&value)))
		if value != 42 {
			panic(value)
		}
	} else {
		var value int64
		check(b.Convert_auto_long(object, unsafe.Pointer(&value)))
		if value != 42 {
			panic(value)
		}
	}
	var floating float64
	check(b.Convert_auto_double(object, unsafe.Pointer(&floating)))
	if floating != 42.5 {
		panic(floating)
	}
	check(b.Convert_auto_pointer(object, unsafe.Pointer(&pointer)))
	check(b.Convert_auto_const_pointer(object, unsafe.Pointer(&constant)))
	check(b.Convert_auto_self(object, unsafe.Pointer(&self)))
	if pointer == nil || pointer != constant || self != object || *(*int32)(pointer) != 42 {
		panic("borrowed conversion pointers")
	}
	check(b.Convert_auto_entry(object, unsafe.Pointer(&entry)))
	if entry == nil {
		panic("missing converted native function")
	}
	check(b.Convert_auto_apply(object, entry, 20, 22, unsafe.Pointer(&n)))
	if n != 42 {
		panic(n)
	}
	n = 999
	if status, err := b.Convert_auto_int(nil, unsafe.Pointer(&n)); status != cxx.InvalidArgument || err != nil || n != 0 {
		panic("conversion null receiver output")
	}
	if status, err := b.Convert_auto_int(object, nil); status != cxx.InvalidArgument || err != nil {
		panic("conversion null output")
	}
	check(b.Convert_auto_take(object, unsafe.Pointer(&n)))
	if n != 43 {
		panic(n)
	}
	check(b.Convert_auto_read(object, unsafe.Pointer(&n)))
	if n != -1 || *(*int32)(pointer) != -1 {
		panic("rvalue conversion state")
	}
	n = 999
	if status, err := b.Convert_auto_int(object, unsafe.Pointer(&n)); status != cxx.Exception || err != nil || n != 0 {
		panic("conversion exception output")
	}
	check(b.Convert_auto_delete(unsafe.Pointer(&object)))
	if object != nil {
		panic("conversion owner not cleared")
	}
	check(b.Convert_auto_delete(unsafe.Pointer(&object)))
	check(b.Convert_template_new(unsafe.Pointer(&template)))
	check(b.Convert_template_int(template, unsafe.Pointer(&n)))
	check(b.Convert_template_double(template, unsafe.Pointer(&floating)))
	if n != 42 || floating != 42 {
		panic("native conversion template")
	}
	check(b.Convert_template_delete(unsafe.Pointer(&template)))
	if template != nil {
		panic("conversion template owner not cleared")
	}
	fmt.Println(42)
}
