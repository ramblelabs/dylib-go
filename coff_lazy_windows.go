//go:build windows

package dylib

import (
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/ramblelabs/dylib-go/internal/native"
)

var coffLazyRegistry struct {
	sync.Mutex
	once     sync.Once
	callback uintptr
	next     uintptr
	images   map[uintptr]*coffLazyImage
}

func registerCOFFLazyImage(image *coffLazyImage) (uintptr, uintptr, error) {
	coffLazyRegistry.once.Do(func() {
		coffLazyRegistry.images = make(map[uintptr]*coffLazyImage)
		coffLazyRegistry.callback = syscall.NewCallback(dispatchCOFFLazyImport)
	})
	coffLazyRegistry.Lock()
	defer coffLazyRegistry.Unlock()
	if coffLazyRegistry.next == ^uintptr(0) {
		return 0, 0, fmt.Errorf("delayed import registry identities exhausted")
	}
	coffLazyRegistry.next++
	id := coffLazyRegistry.next
	coffLazyRegistry.images[id] = image
	return id, coffLazyRegistry.callback, nil
}

func unregisterCOFFLazyImage(id uintptr) {
	coffLazyRegistry.Lock()
	delete(coffLazyRegistry.images, id)
	coffLazyRegistry.Unlock()
}

func dispatchCOFFLazyImport(address uintptr) (target uintptr) {
	// No panic or exception may unwind through the public Go callback.
	defer func() {
		if recover() != nil {
			native.SetCOFFDelayResult(address, 0, 0, coffDelayInvalidParameter, coffDelayInvalidParameter)
			target = 0
		}
	}()
	request, ok := native.ReadCOFFDelayRequest(address)
	if !ok {
		return 0
	}
	coffLazyRegistry.Lock()
	image := coffLazyRegistry.images[request.Owner]
	coffLazyRegistry.Unlock()
	if image == nil || request.Slot >= uintptr(len(image.slots)) {
		native.SetCOFFDelayResult(address, 0, 0, coffDelayInvalidParameter, coffDelayInvalidParameter)
		return 0
	}
	var recovery coffLazyResult
	if request.Recovering {
		recovery = coffLazyResult{module: request.Module, address: request.Address,
			owned: request.Module != 0 && request.Module != request.PreviousModule}
	}
	result, exception, err := image.providers.resolve(int(request.Slot), request.Thread, recovery)
	code := uint32(0)
	if exception != 0 {
		code = nativeDelayError(err, exception)
	}
	allocation := image.slots[request.Slot]
	atomic.StoreUintptr(nativeWord(image.base+uintptr(allocation.module)), result.module)
	native.SetCOFFDelayResult(address, result.address, result.module, exception, code)
	return result.address
}

func coffLazyNativeOperations() coffLazyOperations {
	return coffLazyOperations{
		open: native.Open,
		lookup: func(module uintptr, name string, ordinal uint16) (uintptr, error) {
			return native.LookupImport(module, name, ordinal)
		},
		close: native.Close,
	}
}
