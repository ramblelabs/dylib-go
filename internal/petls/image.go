// Package petls constructs data-only PE DLLs whose static TLS is owned by the OS.
package petls

import (
	"encoding/binary"
	"fmt"
	"math/bits"
)

// IndexRVA is the loader-written DWORD TLS module index. There is no entry
// point, import, export, callback or executable section in the generated DLL.
const IndexRVA = 0x1000

// Image creates a native PE32/PE32+ TLS carrier with the supplied template.
func Image(arch string, template []byte, alignment uint64) ([]byte, error) {
	machine, word, optional := uint16(0), 8, 240
	switch arch {
	case "amd64":
		machine = 0x8664
	case "arm64":
		machine = 0xaa64
	case "386":
		machine, word, optional = 0x14c, 4, 224
	default:
		return nil, fmt.Errorf("unsupported PE TLS architecture %s", arch)
	}
	if len(template) == 0 || len(template) > 64<<20 || alignment == 0 || alignment > 4096 || alignment&(alignment-1) != 0 {
		return nil, fmt.Errorf("invalid PE TLS template size or alignment")
	}
	align := func(n, a int) int { return (n + a - 1) &^ (a - 1) }
	tlsRaw := align(len(template), 512)
	relocRVA := 0x2000 + align(len(template), 4096)
	relocFile := 0x400 + tlsRaw
	b := make([]byte, relocFile+512)
	put16 := func(off int, v uint16) { binary.LittleEndian.PutUint16(b[off:], v) }
	put32 := func(off int, v uint32) { binary.LittleEndian.PutUint32(b[off:], v) }
	put64 := func(off int, v uint64) { binary.LittleEndian.PutUint64(b[off:], v) }
	copy(b, "MZ")
	put32(0x3c, 0x80)
	copy(b[0x80:], "PE\x00\x00")
	put16(0x84, machine)
	put16(0x86, 3)
	put16(0x94, uint16(optional))
	flags := uint16(0x2022)
	if word == 4 {
		flags = 0x2102
	}
	put16(0x96, flags)
	base := uint64(0x180000000)
	o := 0x98
	directories := o + 112
	if word == 4 {
		base = 0x10000000
		directories = o + 96
		put16(o, 0x10b)
		put32(o+24, IndexRVA)
		put32(o+28, uint32(base))
		put32(o+72, 1<<20)
		put32(o+76, 4096)
		put32(o+80, 1<<20)
		put32(o+84, 4096)
		put32(o+92, 16)
	} else {
		put16(o, 0x20b)
		put64(o+24, base)
		put64(o+72, 1<<20)
		put64(o+80, 4096)
		put64(o+88, 1<<20)
		put64(o+96, 4096)
		put32(o+108, 16)
	}
	put32(o+8, uint32(1024+tlsRaw))
	put32(o+32, 4096)
	put32(o+36, 512)
	put16(o+40, 6)
	put16(o+48, 6)
	put32(o+56, uint32(relocRVA+4096))
	put32(o+60, 512)
	put16(o+68, 3)
	put16(o+70, 0x140)
	put32(directories+5*8, uint32(relocRVA))
	put32(directories+5*8+4, 16)
	put32(directories+9*8, IndexRVA+32)
	put32(directories+9*8+4, uint32(4*word+8))
	section := func(i int, name string, rva, size, file, raw int, flags uint32) {
		h := o + optional + i*40
		copy(b[h:h+8], name)
		put32(h+8, uint32(size))
		put32(h+12, uint32(rva))
		put32(h+16, uint32(raw))
		put32(h+20, uint32(file))
		put32(h+36, flags)
	}
	section(0, ".data", IndexRVA, 512, 512, 512, 0xc0000040)
	section(1, ".tls", 0x2000, len(template), 1024, tlsRaw, 0xc0000040)
	section(2, ".reloc", relocRVA, 16, relocFile, 512, 0x42000040)
	put32(512, 0xffffffff)
	for i, address := range []uint64{base + 0x2000, base + 0x2000 + uint64(len(template)), base + IndexRVA} {
		off := 512 + 32 + i*word
		if word == 4 {
			put32(off, uint32(address))
		} else {
			put64(off, address)
		}
	}
	put32(512+32+4*word+4, uint32(bits.TrailingZeros64(alignment)+1)<<20)
	copy(b[1024:], template)
	put32(relocFile, IndexRVA)
	put32(relocFile+4, 16)
	kind := uint16(10)
	if word == 4 {
		kind = 3
	}
	for i := 0; i < 3; i++ {
		put16(relocFile+8+2*i, kind<<12|uint16(32+i*word))
	}
	return b, nil
}
