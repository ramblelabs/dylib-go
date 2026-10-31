//go:build windows

package native

import (
	"fmt"
	"syscall"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var virtualAlloc = kernel32.NewProc("VirtualAlloc")
var virtualProtect = kernel32.NewProc("VirtualProtect")
var virtualFree = kernel32.NewProc("VirtualFree")
var flushInstructionCache = kernel32.NewProc("FlushInstructionCache")
var getProcAddress = kernel32.NewProc("GetProcAddress")

func Alloc(n int) ([]byte, error) {
	address, _, e := virtualAlloc.Call(0, uintptr(n), 0x3000, 0x04)
	if address == 0 {
		return nil, e
	}
	// VirtualAlloc returns an OS-owned address, not a Go heap pointer. Its
	// storage remains stable across GC until VirtualFree, so reinterpret the
	// returned address bits as a pointer without a uintptr-to-pointer cast.
	p := *(*unsafe.Pointer)(unsafe.Pointer(&address))
	return unsafe.Slice((*byte)(p), n), nil
}
func Free(b []byte) error {
	r, _, e := virtualFree.Call(uintptr(unsafe.Pointer(&b[0])), 0, 0x8000)
	if r == 0 {
		return e
	}
	return nil
}
func Protect(b []byte, exec, write bool) error {
	p := uintptr(0x02)
	if write {
		p = 0x04
	}
	if exec {
		p = 0x20
	}
	var old uint32
	r, _, e := virtualProtect.Call(uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), p, uintptr(unsafe.Pointer(&old)))
	if r == 0 {
		return e
	}
	if exec {
		r, _, e = flushInstructionCache.Call(^uintptr(0), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
		if r == 0 {
			return e
		}
	}
	return nil
}
func Open(path string) (uintptr, error) {
	h, e := syscall.LoadLibrary(path)
	return uintptr(h), e
}
func Lookup(h uintptr, name string) uintptr {
	if h == 0 {
		return 0
	}
	p, _ := syscall.GetProcAddress(syscall.Handle(h), name)
	return p
}
func LookupOrdinal(h uintptr, ordinal uint16) uintptr {
	if h == 0 || ordinal == 0 {
		return 0
	}
	p, _, _ := getProcAddress.Call(h, uintptr(ordinal))
	return p
}

// LookupImport preserves the actual Win32 failure for delayed-import hooks.
func LookupImport(h uintptr, name string, ordinal uint16) (uintptr, error) {
	if h == 0 {
		return 0, syscall.Errno(6) // ERROR_INVALID_HANDLE.
	}
	if ordinal == 0 {
		return syscall.GetProcAddress(syscall.Handle(h), name)
	}
	p, _, err := getProcAddress.Call(h, uintptr(ordinal))
	if p == 0 {
		return 0, err
	}
	return p, nil
}
func Close(h uintptr) error {
	if h == 0 {
		return fmt.Errorf("invalid library handle")
	}
	return syscall.FreeLibrary(syscall.Handle(h))
}
