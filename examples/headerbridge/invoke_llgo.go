//go:build cgo && llgo && (linux || darwin || windows)

package headerbridge

import (
	"unsafe"

	dylib "github.com/ramblelabs/dylib-go"
)

//llgo:cdecl
type entry func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) int32

// Invoke uses the direct llgo C function-pointer path for the same facade ABI.
func Invoke(symbol *dylib.Symbol, target uintptr, out, in unsafe.Pointer) (int32, error) {
	var result int32
	err := symbol.WithAddress(func(address uintptr) error {
		function := *(*entry)(unsafe.Pointer(&address))
		pointer := *(*unsafe.Pointer)(unsafe.Pointer(&target))
		result = function(pointer, out, in)
		return nil
	})
	return result, err
}
