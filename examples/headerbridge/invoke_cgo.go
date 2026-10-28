//go:build cgo && !llgo && (linux || darwin || windows)

package headerbridge

/*
#include <stdint.h>
static int32_t dylib_header_call(uintptr_t entry, uintptr_t target, void *out, const void *in) {
    return ((int32_t (*)(void *,void *,const void *))entry)((void *)target,out,in);
}
*/
import "C"

import (
	"unsafe"

	dylib "github.com/ramblelabs/dylib-go"
)

// Invoke implements the facade's fixed C pointer signature with cgo.
func Invoke(symbol *dylib.Symbol, target uintptr, out, in unsafe.Pointer) (int32, error) {
	var result int32
	err := symbol.WithAddress(func(entry uintptr) error {
		result = int32(C.dylib_header_call(C.uintptr_t(entry), C.uintptr_t(target), out, in))
		return nil
	})
	return result, err
}
