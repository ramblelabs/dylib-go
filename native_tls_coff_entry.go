package dylib

import (
	"encoding/binary"
	"fmt"
)

// Return the address of the private guard through the ordinary Windows native
// TLS ABI. Only volatile C registers are used; no thread-array entry is changed.
func coffTLSGuardEntry(arch string, index uintptr, offset uint32) ([]byte, error) {
	if index == 0 || uint64(offset) >= maxImage {
		return nil, fmt.Errorf("invalid native TLS guard index or offset")
	}
	var b []byte
	switch arch {
	case "amd64":
		b = append(b, 0x48, 0xb9) // movabs rcx, index
		b = binary.LittleEndian.AppendUint64(b, uint64(index))
		b = append(b, 0x8b, 0x09, 0x65, 0x48, 0x8b, 0x04, 0x25, 0x58, 0, 0, 0, 0x48, 0x8b, 0x04, 0xc8, 0x48, 0x05)
		b = binary.LittleEndian.AppendUint32(b, offset)
		b = append(b, 0xc3)
	case "386":
		if uint64(index) > 0xffffffff {
			return nil, fmt.Errorf("native TLS index address exceeds 32 bits")
		}
		b = append(b, 0xb9)
		b = binary.LittleEndian.AppendUint32(b, uint32(index))
		b = append(b, 0x8b, 0x09, 0x64, 0xa1, 0x2c, 0, 0, 0, 0x8b, 0x04, 0x88, 0x05)
		b = binary.LittleEndian.AppendUint32(b, offset)
		b = append(b, 0xc3)
	case "arm64":
		for half := uint32(0); half < 4; half++ {
			op := uint32(0xd2800001)
			if half != 0 {
				op = 0xf2800001
			}
			b = binary.LittleEndian.AppendUint32(b, op|half<<21|uint32(uint64(index)>>(half*16)&0xffff)<<5)
		}
		for _, op := range []uint32{0xb9400021, 0xf9402e40, 0xf8617800, 0x52800001 | (offset&0xffff)<<5, 0x72a00001 | (offset>>16)<<5, 0x8b010000, 0xd65f03c0} {
			b = binary.LittleEndian.AppendUint32(b, op)
		}
	default:
		return nil, fmt.Errorf("unsupported native TLS guard architecture %s", arch)
	}
	return b, nil
}
