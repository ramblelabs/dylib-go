package dylib

import (
	"fmt"
	"runtime"
	"unsafe"
)

// libgcc treats zero in the representable bits of an encoded FDE PC as a
// discarded function. With sdata4/pcrel this also drops valid 4 GiB-aligned
// addresses. Keep the entire image inside one open 32-bit address window.
func gccFrameMappingSafe(base, size uint64) bool {
	const window = uint64(1) << 32
	low := base & (window - 1)
	return size != 0 && low != 0 && size <= window-low
}

func (im *image) frameMappingPolicy() func(uint64, uint64) bool {
	if runtime.GOOS != "linux" || im.pointerSize != 8 {
		return nil
	}
	for _, o := range im.objects {
		if o.info.Format == "ELF" {
			for _, s := range o.sections {
				if s != nil && elfUnwindSection(s.name) {
					return gccFrameMappingSafe
				}
			}
		}
	}
	return nil
}

// Hold rejected mappings while allocating again, so mmap cannot immediately
// return the same address. All mappings remain owned, including on failure.
func reserveImageMemory(size int, allocate func(int) ([]byte, error), accept func(uint64, uint64) bool) ([]byte, [][]byte, error) {
	var reserved [][]byte
	for attempt := 0; attempt < 8; attempt++ {
		memory, err := allocate(size)
		if err != nil {
			return nil, reserved, err
		}
		base := uint64(uintptr(unsafe.Pointer(&memory[0])))
		if accept == nil || accept(base, uint64(len(memory))) {
			return memory, reserved, nil
		}
		reserved = append(reserved, memory)
	}
	return nil, reserved, fmt.Errorf("cannot allocate an ELF unwind image away from a 4 GiB boundary")
}

func (im *image) freeReservedMemory(free func([]byte) error) error {
	for len(im.reservedMemory) != 0 {
		last := len(im.reservedMemory) - 1
		if err := free(im.reservedMemory[last]); err != nil {
			return err
		}
		im.reservedMemory[last] = nil
		im.reservedMemory = im.reservedMemory[:last]
	}
	return nil
}
