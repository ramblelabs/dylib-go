//go:build linux

package dylib

import (
	"os"
	"runtime"
	"testing"
	"unsafe"

	"github.com/ramblelabs/dylib-go/internal/native"
)

func TestNativeELFUnwindBoundaryMapping(t *testing.T) {
	if runtime.GOARCH == "386" {
		t.Skip("4 GiB-aligned nonzero addresses require a 64-bit host")
	}
	needNative(t)
	host := nativeInputSession(t, "testdata/unwind_boundary.c", "library", "-lgcc_s")
	call(t, host, "boundary_create", 0, 0, 0)
	close, err := host.Resolve("boundary_close")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := close.WithAddress(func(address uintptr) error { native.CallVoid(address); return nil }); err != nil {
			t.Error(err)
		}
	}()
	symbol, err := host.Lookup("boundary_address")
	if err != nil {
		t.Fatal(err)
	}
	address := *nativeWord(symbol)
	size := 3 * os.Getpagesize()
	if address == 0 || address&0xffffffff != 0 {
		t.Fatal("native control did not reserve a 4 GiB boundary", address)
	}
	t.Run("native_control", func(t *testing.T) {
		bad, err := callInt32(host, "boundary_lookup", 0, 0)
		if err != nil || bad < 0 || bad > 1 {
			t.Fatal("boundary FDE control", bad, err)
		}
		// A future libgcc may fix the null-PC heuristic; the safe placement
		// must work with both implementations.
		t.Logf("native libgcc lookup at a 4 GiB boundary: %d", bad)
		call(t, host, "boundary_lookup", 1, 0, 1)
	})
	t.Run("allocation", func(t *testing.T) {
		pointer := *(*unsafe.Pointer)(unsafe.Pointer(&address))
		boundary := unsafe.Slice((*byte)(pointer), size)
		calls := 0
		memory, reserved, err := reserveImageMemory(size, func(n int) ([]byte, error) {
			calls++
			if calls == 1 {
				return boundary, nil
			}
			return native.Alloc(n)
		}, gccFrameMappingSafe)
		defer func() {
			if memory != nil {
				if err := native.Free(memory); err != nil {
					t.Error(err)
				}
			}
			for index := 1; index < len(reserved); index++ { // The C control owns the first mapping.
				if err := native.Free(reserved[index]); err != nil {
					t.Error(err)
				}
			}
		}()
		if err != nil || len(reserved) == 0 || calls != len(reserved)+1 || uintptr(unsafe.Pointer(&reserved[0][0])) != address {
			t.Fatal("boundary mapping lost or selected", calls, len(reserved), err)
		}
		if !gccFrameMappingSafe(uint64(uintptr(unsafe.Pointer(&memory[0]))), uint64(len(memory))) {
			t.Fatal("unsafe mapping selected")
		}
	})
}
