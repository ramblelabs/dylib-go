//go:build cgo && darwin && (amd64 || arm64)

package abibridge

/*
#include <stdint.h>
static uint32_t bridge_version(uintptr_t entry) {
    return ((uint32_t (*)(void))entry)();
}
static uintptr_t bridge_prepare(uintptr_t entry, const unsigned char *data, uint32_t size, char *error, uint32_t capacity) {
    return (uintptr_t)((void *(*)(const unsigned char *, uint32_t, char *, uint32_t))entry)(data, size, error, capacity);
}
static int32_t bridge_invoke(uintptr_t entry, uintptr_t plan, uintptr_t function,
    const unsigned char *args, uint32_t args_size, unsigned char *result, uint32_t result_size,
    char *error, uint32_t capacity) {
    return ((int32_t (*)(void *, const void *, const unsigned char *, uint32_t, unsigned char *, uint32_t, char *, uint32_t))entry)
        ((void *)plan, (const void *)function, args, args_size, result, result_size, error, capacity);
}
static int32_t bridge_invoke_values(uintptr_t entry, uintptr_t plan, uintptr_t function,
    const unsigned char *args, uint32_t args_size, unsigned char *result, uint32_t result_size,
    const uint64_t *handles, uint32_t count, char *error, uint32_t capacity) {
    return ((int32_t (*)(void *, const void *, const unsigned char *, uint32_t, unsigned char *, uint32_t,
        const uint64_t *, uint32_t, char *, uint32_t))entry)
        ((void *)plan, (const void *)function, args, args_size, result, result_size, handles, count, error, capacity);
}
static uintptr_t bridge_value_type(uintptr_t entry, uintptr_t provider, uint64_t *layout, char *error, uint32_t capacity) {
    return (uintptr_t)((void *(*)(uintptr_t, uint64_t *, char *, uint32_t))entry)(provider, layout, error, capacity);
}
static uintptr_t bridge_value_new(uintptr_t entry, uintptr_t type, uintptr_t source, char *error, uint32_t capacity) {
    return (uintptr_t)((void *(*)(void *, const void *, char *, uint32_t))entry)((void *)type, (const void *)source, error, capacity);
}
static void *bridge_value_pointer(uintptr_t entry, uintptr_t handle) {
    return (void *)((uintptr_t (*)(void *))entry)((void *)handle);
}
static void bridge_release(uintptr_t entry, uintptr_t plan) {
    ((void (*)(void *))entry)((void *)plan);
}
*/
import "C"

import (
	"bytes"
	"fmt"
	"runtime"
	"unsafe"
)

func nativeAvailable() bool              { return true }
func nativeVersion(entry uintptr) uint32 { return uint32(C.bridge_version(C.uintptr_t(entry))) }

func bytePointer(data []byte) *C.uchar {
	if len(data) == 0 {
		return nil
	}
	return (*C.uchar)(unsafe.Pointer(&data[0]))
}

func nativeFailure(data []byte) error {
	if end := bytes.IndexByte(data, 0); end >= 0 {
		data = data[:end]
	}
	if len(data) == 0 {
		return fmt.Errorf("abibridge: native operation failed")
	}
	return fmt.Errorf("abibridge: %s", data)
}

func nativePrepare(entry uintptr, wire []byte) (uintptr, error) {
	var message [1024]byte
	plan := uintptr(C.bridge_prepare(C.uintptr_t(entry), bytePointer(wire), C.uint32_t(len(wire)), (*C.char)(unsafe.Pointer(&message[0])), C.uint32_t(len(message))))
	runtime.KeepAlive(wire)
	if plan == 0 {
		return 0, nativeFailure(message[:])
	}
	return plan, nil
}

func nativeInvoke(entry, plan, function uintptr, args, result []byte) error {
	var message [1024]byte
	status := C.bridge_invoke(C.uintptr_t(entry), C.uintptr_t(plan), C.uintptr_t(function), bytePointer(args), C.uint32_t(len(args)), bytePointer(result), C.uint32_t(len(result)), (*C.char)(unsafe.Pointer(&message[0])), C.uint32_t(len(message)))
	runtime.KeepAlive(args)
	runtime.KeepAlive(result)
	if status != 0 {
		return nativeFailure(message[:])
	}
	return nil
}

func nativeRelease(entry, plan uintptr) {
	C.bridge_release(C.uintptr_t(entry), C.uintptr_t(plan))
}

func nativeInvokeValues(entry, plan, function uintptr, args, result []byte, handles []uint64) error {
	var message [1024]byte
	var refs *C.uint64_t
	if len(handles) != 0 {
		refs = (*C.uint64_t)(unsafe.Pointer(&handles[0]))
	}
	status := C.bridge_invoke_values(C.uintptr_t(entry), C.uintptr_t(plan), C.uintptr_t(function), bytePointer(args), C.uint32_t(len(args)), bytePointer(result), C.uint32_t(len(result)),
		refs, C.uint32_t(len(handles)), (*C.char)(unsafe.Pointer(&message[0])), C.uint32_t(len(message)))
	runtime.KeepAlive(args)
	runtime.KeepAlive(result)
	runtime.KeepAlive(handles)
	if status != 0 {
		return nativeFailure(message[:])
	}
	return nil
}

func nativeValueType(entry, provider uintptr) (uintptr, ValueLayout, error) {
	var message [1024]byte
	var layout [3]C.uint64_t
	plan := uintptr(C.bridge_value_type(C.uintptr_t(entry), C.uintptr_t(provider), &layout[0], (*C.char)(unsafe.Pointer(&message[0])), C.uint32_t(len(message))))
	if plan == 0 {
		return 0, ValueLayout{}, nativeFailure(message[:])
	}
	return plan, ValueLayout{uint64(layout[0]), uint64(layout[1]), uint64(layout[2])}, nil
}
func nativeValueNew(entry, plan, source uintptr) (uintptr, error) {
	var message [1024]byte
	handle := uintptr(C.bridge_value_new(C.uintptr_t(entry), C.uintptr_t(plan), C.uintptr_t(source), (*C.char)(unsafe.Pointer(&message[0])), C.uint32_t(len(message))))
	if handle == 0 {
		return 0, nativeFailure(message[:])
	}
	return handle, nil
}
func nativeValuePointer(entry, handle uintptr) unsafe.Pointer {
	return C.bridge_value_pointer(C.uintptr_t(entry), C.uintptr_t(handle))
}
