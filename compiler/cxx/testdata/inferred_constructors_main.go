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
			panic(fmt.Sprintf("constructor status %d: %v", status, err))
		}
	}
	var pair, entry unsafe.Pointer
	check(b.Ctor_pair(unsafe.Pointer(&pair)))
	check(b.Ctor_native_entry(unsafe.Pointer(&entry)))
	if pair == nil || entry == nil {
		panic("missing native addresses")
	}
	long := func(output unsafe.Pointer) (int32, error) { return b.Ctor_long(20, output) }
	for _, tc := range []struct {
		name    string
		create  func(unsafe.Pointer) (int32, error)
		read    func(unsafe.Pointer, unsafe.Pointer) (int32, error)
		destroy func(unsafe.Pointer) (int32, error)
		want    int32
	}{
		{"zero", b.Ctor_zero, b.Ctor_Receiver_read, b.Ctor_Receiver_delete, 42},
		{"int", func(p unsafe.Pointer) (int32, error) { return b.Ctor_int(20, p) }, b.Ctor_Receiver_read, b.Ctor_Receiver_delete, 22},
		{"long", long, b.Ctor_Receiver_read, b.Ctor_Receiver_delete, 23},
		{"narrow", func(p unsafe.Pointer) (int32, error) { return b.Ctor_narrow(-128, p) }, b.Ctor_Receiver_read, b.Ctor_Receiver_delete, 42},
		{"wide", func(p unsafe.Pointer) (int32, error) { return b.Ctor_wide(^uint64(0), p) }, b.Ctor_Receiver_read, b.Ctor_Receiver_delete, 42},
		{"code", func(p unsafe.Pointer) (int32, error) { return b.Ctor_code(42, p) }, b.Ctor_Receiver_read, b.Ctor_Receiver_delete, 46},
		{"bool", func(p unsafe.Pointer) (int32, error) { return b.Ctor_bool(true, p) }, b.Ctor_Receiver_read, b.Ctor_Receiver_delete, 42},
		{"float", func(p unsafe.Pointer) (int32, error) { return b.Ctor_float(42.5, p) }, b.Ctor_Receiver_read, b.Ctor_Receiver_delete, 47},
		{"double", func(p unsafe.Pointer) (int32, error) { return b.Ctor_double(42.5, p) }, b.Ctor_Receiver_read, b.Ctor_Receiver_delete, 48},
		{"pointer", func(p unsafe.Pointer) (int32, error) { return b.Ctor_pointer(pair, p) }, b.Ctor_Receiver_read, b.Ctor_Receiver_delete, 27},
		{"const_pointer", func(p unsafe.Pointer) (int32, error) { return b.Ctor_const_pointer(pair, p) }, b.Ctor_Receiver_read, b.Ctor_Receiver_delete, 28},
		{"global_pointer", func(p unsafe.Pointer) (int32, error) { return b.Ctor_global_pointer(pair, p) }, b.Ctor_Receiver_read, b.Ctor_Receiver_delete, 27},
		{"entry", func(p unsafe.Pointer) (int32, error) { return b.Ctor_entry(entry, 40, p) }, b.Ctor_Receiver_read, b.Ctor_Receiver_delete, 42},
		{"default_zero", b.Ctor_default_zero, b.Ctor_Defaulted_read, b.Ctor_Defaulted_delete, 42},
		{"default_one", func(p unsafe.Pointer) (int32, error) { return b.Ctor_default_one(40, p) }, b.Ctor_Defaulted_read, b.Ctor_Defaulted_delete, 42},
		{"default_two", func(p unsafe.Pointer) (int32, error) { return b.Ctor_default_two(20, 22, p) }, b.Ctor_Defaulted_read, b.Ctor_Defaulted_delete, 42},
		{"deduced_int", func(p unsafe.Pointer) (int32, error) { return b.Ctor_deduced_int(40, p) }, b.Ctor_Deduced_read, b.Ctor_Deduced_delete, 42},
		{"deduced_double", func(p unsafe.Pointer) (int32, error) { return b.Ctor_deduced_double(40, p) }, b.Ctor_Deduced_read, b.Ctor_Deduced_delete, 43},
		{"converted", func(p unsafe.Pointer) (int32, error) { return b.Ctor_converted(40, p) }, b.Ctor_Converted_read, b.Ctor_Converted_delete, 42},
		{"qualified", func(p unsafe.Pointer) (int32, error) { return b.Ctor_qualified(40, p) }, b.Ctor_Qualified_read, b.Ctor_Qualified_delete, 42},
		{"box", func(p unsafe.Pointer) (int32, error) { return b.Ctor_box(40, p) }, b.Ctor_BoxAlias_read, b.Ctor_BoxAlias_delete, 42},
		{"null_pointer", func(p unsafe.Pointer) (int32, error) { return b.Ctor_pointer(nil, p) }, b.Ctor_Receiver_read, b.Ctor_Receiver_delete, -100},
		{"null_entry", func(p unsafe.Pointer) (int32, error) { return b.Ctor_entry(nil, 40, p) }, b.Ctor_Receiver_read, b.Ctor_Receiver_delete, -102},
	} {
		var object unsafe.Pointer
		check(tc.create(unsafe.Pointer(&object)))
		if object == nil {
			panic(tc.name + ": missing allocation")
		}
		var value, before, after int32
		check(tc.read(object, unsafe.Pointer(&value)))
		if value != tc.want {
			panic(fmt.Sprintf("%s: %d want %d", tc.name, value, tc.want))
		}
		check(b.Ctor_count(unsafe.Pointer(&before)))
		original := object
		status, err := tc.create(unsafe.Pointer(&object))
		if status != cxx.InvalidArgument || err != nil || object != original {
			panic(tc.name + ": occupied slot changed")
		}
		status, err = tc.create(nil)
		if status != cxx.InvalidArgument || err != nil {
			panic(tc.name + ": null output accepted")
		}
		check(b.Ctor_count(unsafe.Pointer(&after)))
		if before != after {
			panic(tc.name + ": invalid output constructed an object")
		}
		check(tc.destroy(unsafe.Pointer(&object)))
		check(tc.destroy(unsafe.Pointer(&object)))
		check(b.Ctor_live(unsafe.Pointer(&value)))
		if object != nil || value != 0 {
			panic(tc.name + ": native owner leaked")
		}
	}
	for _, create := range []func(unsafe.Pointer) (int32, error){
		func(p unsafe.Pointer) (int32, error) { return b.Ctor_int(-1, p) }, b.Ctor_null_new, b.Ctor_throw_new,
	} {
		var object unsafe.Pointer
		status, err := create(unsafe.Pointer(&object))
		if status != cxx.Exception || err != nil || object != nil {
			panic("failed allocation published an object")
		}
	}
	var live int32
	check(b.Ctor_live(unsafe.Pointer(&live)))
	if live != 0 {
		panic("exception leaked a native owner")
	}
	fmt.Println(42)
}
