package petls

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"testing"
)

func TestImages(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "386"} {
		for _, size := range []int{1, 68, 4096, 65540} {
			t.Run(fmt.Sprint(arch, "/", size), func(t *testing.T) {
				template := make([]byte, size)
				template[0] = 7
				template[size-1] = 42
				b, err := Image(arch, template, 64)
				if err != nil {
					t.Fatal(err)
				}
				f, err := pe.NewFile(bytes.NewReader(b))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				var base uint64
				var dirs []pe.DataDirectory
				switch o := f.OptionalHeader.(type) {
				case *pe.OptionalHeader64:
					base = o.ImageBase
					dirs = o.DataDirectory[:]
					if o.AddressOfEntryPoint != 0 {
						t.Fatal("executable entry")
					}
				case *pe.OptionalHeader32:
					base = uint64(o.ImageBase)
					dirs = o.DataDirectory[:]
					if o.AddressOfEntryPoint != 0 {
						t.Fatal("executable entry")
					}
				default:
					t.Fatal("missing optional header")
				}
				if len(f.Sections) != 3 || dirs[0].Size != 0 || dirs[1].Size != 0 || dirs[9].VirtualAddress != IndexRVA+32 {
					t.Fatal("unexpected image directories")
				}
				data, err := f.Section(".data").Data()
				if err != nil {
					t.Fatal(err)
				}
				word := 8
				if arch == "386" {
					word = 4
				}
				read := func(off int) uint64 {
					if word == 4 {
						return uint64(binary.LittleEndian.Uint32(data[off:]))
					}
					return binary.LittleEndian.Uint64(data[off:])
				}
				for i, want := range []uint64{base + 0x2000, base + 0x2000 + uint64(size), base + IndexRVA, 0} {
					if read(32+i*word) != want {
						t.Fatalf("TLS field %d", i)
					}
				}
				if binary.LittleEndian.Uint32(data[32+4*word:]) != 0 || binary.LittleEndian.Uint32(data[36+4*word:]) != 7<<20 {
					t.Fatal("zero fill/alignment")
				}
				tls, err := f.Section(".tls").Data()
				if err != nil || !bytes.Equal(tls[:size], template) {
					t.Fatal("template bytes", err)
				}
				reloc, err := f.Section(".reloc").Data()
				if err != nil {
					t.Fatal(err)
				}
				if binary.LittleEndian.Uint32(reloc) != IndexRVA || binary.LittleEndian.Uint32(reloc[4:]) != 16 {
					t.Fatal("base relocation block")
				}
				kind := uint16(10)
				if word == 4 {
					kind = 3
				}
				for i := 0; i < 3; i++ {
					if binary.LittleEndian.Uint16(reloc[8+2*i:]) != kind<<12|uint16(32+i*word) {
						t.Fatal("TLS VA relocation")
					}
				}
				for _, section := range f.Sections {
					if section.Characteristics&pe.IMAGE_SCN_MEM_EXECUTE != 0 {
						t.Fatal("executable carrier section")
					}
				}
			})
		}
	}
}

func TestInvalidImages(t *testing.T) {
	for _, tc := range []struct {
		name, arch string
		size       int
		align      uint64
	}{
		{"arch", "riscv64", 1, 1}, {"empty", "amd64", 0, 1}, {"alignment_zero", "amd64", 1, 0}, {"alignment_nonpower", "amd64", 1, 3}, {"alignment_large", "386", 1, 8192}, {"size", "arm64", (64 << 20) + 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Image(tc.arch, make([]byte, tc.size), tc.align); err == nil {
				t.Fatal("accepted invalid carrier")
			}
		})
	}
}
