//go:build cgo && (linux || darwin || windows)

// Package headerbridge demonstrates leased calls through an original-header facade.
package headerbridge

import (
	"unsafe"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cbridge"
)

// Call retains the original function and the separately compiled facade. The
// binding checks C carrier layouts before copying arguments into native storage.
func Call(session *dylib.Session, binding cbridge.Binding, args ...abi.Value) (abi.Value, error) {
	if session == nil {
		return abi.Value{}, dylib.ErrClosed
	}
	if err := session.Link(binding.NativeSymbol, binding.Symbol); err != nil {
		return abi.Value{}, err
	}
	target, err := session.Resolve(binding.NativeSymbol)
	if err != nil {
		return abi.Value{}, err
	}
	facade, err := session.Resolve(binding.Symbol)
	if err != nil {
		return abi.Value{}, err
	}
	var result abi.Value
	err = target.WithAddress(func(address uintptr) error {
		var callErr error
		result, callErr = binding.Call(func(target uintptr, out, in unsafe.Pointer) (int32, error) {
			return Invoke(facade, target, out, in)
		}, address, args...)
		return callErr
	})
	return result, err
}
