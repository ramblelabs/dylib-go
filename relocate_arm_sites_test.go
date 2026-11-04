package dylib

import (
	"fmt"
	"testing"
)

func TestARM64RelocationSiteValidation(t *testing.T) {
	for _, tc := range []struct {
		name, format string
		typ, ins     uint32
		misaligned   bool
	}{
		{"elf_add_on_store", "ELF", 277, 0x39000000, false},
		{"elf_byte_on_add", "ELF", 278, 0x91000000, false},
		{"elf_addg", "ELF", 277, 0x91800000, false},
		{"elf_reserved_add", "ELF", 277, 0x11800000, false},
		{"elf_jump_on_call", "ELF", 282, 0x94000000, false},
		{"elf_call_on_jump", "ELF", 283, 0x14000000, false},
		{"elf_reserved_integer", "ELF", 286, 0xf9c00000, false},
		{"elf_reserved_simd", "ELF", 299, 0x7dc00000, false},
		{"elf_page", "ELF", 275, 0x90000000, true},
		{"elf_unchecked_page", "ELF", 276, 0x90000000, true},
		{"elf_add", "ELF", 277, 0x91000000, true},
		{"elf_byte", "ELF", 278, 0x39400000, true},
		{"elf_half", "ELF", 284, 0x79400000, true},
		{"elf_word", "ELF", 285, 0xb9400000, true},
		{"elf_quad", "ELF", 286, 0xf9400000, true},
		{"elf_vector", "ELF", 299, 0x3dc00000, true},
		{"elf_jump", "ELF", 282, 0x14000000, true},
		{"elf_call", "ELF", 283, 0x94000000, true},
		{"elf_got_page", "ELF", 311, 0x90000000, true},
		{"elf_got_quad", "ELF", 312, 0xf9400000, true},
		{"elf_got_wrong_page", "ELF", 311, 0x10000000, false},
		{"elf_got_wrong_load", "ELF", 312, 0x39400000, false},
		{"coff_page", "COFF", 4, 0x90000000, true},
		{"coff_branch", "COFF", 3, 0x14000000, true},
		{"coff_add", "COFF", 6, 0x91000000, true},
		{"coff_load", "COFF", 7, 0xf9400000, true},
		{"coff_addg", "COFF", 6, 0x91800000, false},
		{"macho_page", "Mach-O", 3, 0x90000000, true},
		{"macho_branch", "Mach-O", 2, 0x14000000, true},
		{"macho_add", "Mach-O", 4, 0x91000000, true},
		{"macho_load", "Mach-O", 4, 0xf9400000, true},
		{"macho_got_load", "Mach-O", 6, 0xf9400000, true},
		{"macho_addg", "Mach-O", 4, 0x91800000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			im := &image{base: 0x10000000, mem: make([]byte, 128), gotStart: 64, gotNext: 64, stubStart: 128, pointerSize: 8, got: map[uintptr]uintptr{}}
			o := &object{info: Info{Format: tc.format, Arch: "arm64"}}
			b := make([]byte, 4)
			le.PutUint32(b, tc.ins)
			place := im.base
			if tc.misaligned {
				place++
			}
			target := place + 64
			if tc.format == "ELF" && tc.typ != 282 && tc.typ != 283 {
				target = im.base + 64
			}
			r := relocation{typ: tc.typ, pcrel: true, pair: -1}
			if tc.format == "Mach-O" && (tc.typ == 4 || tc.typ == 6) {
				r.pcrel = false
			}
			var err error
			switch tc.format {
			case "ELF":
				err = im.relocELF(o, r, b, target, place)
			case "COFF":
				err = im.relocCOFFARM64(r, b, target, place)
			case "Mach-O":
				err = im.relocMachO(o, r, b, target, place)
			}
			if err == nil || le.Uint32(b) != tc.ins || im.gotNext != 64 {
				t.Fatalf("invalid instruction site accepted or mutated: word=%#x GOT=%d error=%v", le.Uint32(b), im.gotNext, err)
			}
		})
	}
}

func TestARM64Low12Encodings(t *testing.T) {
	for _, tc := range []struct {
		name     string
		typ, ins uint32
		scale    uint
	}{
		{"add_w", 277, 0x110000c5, 0}, {"add_x", 277, 0x910000c5, 0},
		{"adds_w", 277, 0x310000c5, 0}, {"adds_x", 277, 0xb10000c5, 0},
		{"strb", 278, 0x390000c5, 0}, {"ldrb", 278, 0x394000c5, 0},
		{"ldrsb_x", 278, 0x398000c5, 0}, {"ldrsb_w", 278, 0x39c000c5, 0},
		{"strh", 284, 0x790000c5, 1}, {"ldrh", 284, 0x794000c5, 1},
		{"ldrsh_x", 284, 0x798000c5, 1}, {"ldrsh_w", 284, 0x79c000c5, 1},
		{"str_w", 285, 0xb90000c5, 2}, {"ldr_w", 285, 0xb94000c5, 2},
		{"ldrsw", 285, 0xb98000c5, 2},
		{"str_x", 286, 0xf90000c5, 3}, {"ldr_x", 286, 0xf94000c5, 3},
		{"prfm", 286, 0xf98000c5, 3},
		{"str_b", 278, 0x3d0000c5, 0}, {"ldr_b", 278, 0x3d4000c5, 0},
		{"str_h", 284, 0x7d0000c5, 1}, {"ldr_h", 284, 0x7d4000c5, 1},
		{"str_s", 285, 0xbd0000c5, 2}, {"ldr_s", 285, 0xbd4000c5, 2},
		{"str_d", 286, 0xfd0000c5, 3}, {"ldr_d", 286, 0xfd4000c5, 3},
		{"str_q", 299, 0x3d8000c5, 4}, {"ldr_q", 299, 0x3dc000c5, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// RELA replaces an existing nonzero immediate, including a page carry.
			ins := tc.ins | 0xfff<<10
			b := make([]byte, 4)
			le.PutUint32(b, ins)
			im, o := &image{}, &object{info: Info{Format: "ELF", Arch: "arm64"}}
			err := im.relocELF(o, relocation{typ: tc.typ, addend: 0x90}, b, 0x100017f0, 0x10000000)
			want := tc.ins | (0x880>>tc.scale)<<10
			if err != nil || le.Uint32(b) != want {
				t.Fatalf("word=%#x want=%#x error=%v", le.Uint32(b), want, err)
			}
		})
	}
}

func TestARM64ReservedUnsignedOffsetForms(t *testing.T) {
	for _, format := range []string{"ELF", "Mach-O", "COFF"} {
		for _, ins := range []uint32{0xb9c00000, 0xf9c00000, 0x7d800000, 0x7dc00000, 0xbd800000, 0xbdc00000, 0xfd800000, 0xfdc00000} {
			t.Run(fmt.Sprintf("%s/%08x", format, ins), func(t *testing.T) {
				im, o := &image{}, &object{info: Info{Format: format, Arch: "arm64"}}
				b := make([]byte, 4)
				le.PutUint32(b, ins)
				var err error
				switch format {
				case "ELF":
					err = im.relocELF(o, relocation{typ: 299}, b, 0x10000040, 0x10000000)
				case "Mach-O":
					err = im.relocMachO(o, relocation{typ: 4}, b, 0x10000040, 0x10000000)
				case "COFF":
					err = im.relocCOFFARM64(relocation{typ: 7}, b, 0x10000040, 0x10000000)
				}
				if err == nil || le.Uint32(b) != ins {
					t.Fatal("reserved form accepted or written", err)
				}
			})
		}
	}
}

func TestARM64COFFSectionInstructionPlaces(t *testing.T) {
	for _, tc := range []struct {
		name     string
		typ, ins uint32
		invalid  bool
	}{{"low_add", 9, 0x91000000, false}, {"high_add", 10, 0x91400000, false}, {"load", 11, 0xf9400000, false}, {"addg", 9, 0x91800000, true}} {
		for _, offset := range []uint64{0, 1} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, offset), func(t *testing.T) {
				o := &object{info: Info{Format: "COFF", Arch: "arm64"}, sections: []*section{nil, {size: 16}}, symbols: []symbol{{section: 1, value: 8}}}
				im := &image{base: 0x10000000, mem: make([]byte, 16), objects: []*object{o}}
				le.PutUint32(im.mem[offset:], tc.ins)
				err := im.relocate(o, relocation{section: 1, offset: offset, typ: tc.typ, pair: -1})
				if offset == 0 && !tc.invalid {
					if err != nil {
						t.Fatal("aligned section instruction rejected", err)
					}
				} else if err == nil || le.Uint32(im.mem[offset:]) != tc.ins {
					t.Fatal("unaligned section instruction accepted or written", err)
				}
			})
		}
	}
}

func TestARM64InstructionDataPlaces(t *testing.T) {
	for _, tc := range []struct {
		format string
		typ    uint32
		width  int
	}{
		{"ELF", 257, 8}, {"ELF", 258, 4}, {"ELF", 259, 2},
		{"ELF", 260, 8}, {"ELF", 261, 4}, {"ELF", 262, 2},
		{"COFF", 1, 4}, {"COFF", 2, 4}, {"COFF", 14, 8}, {"COFF", 17, 4},
		{"COFF", 8, 4}, {"COFF", 13, 2},
		{"Mach-O", 0, 4}, {"Mach-O", 0, 8}, {"Mach-O", 7, 4},
	} {
		t.Run(fmt.Sprintf("%s/%d/%d", tc.format, tc.typ, tc.width), func(t *testing.T) {
			o := &object{info: Info{Format: tc.format, Arch: "arm64"}, sections: []*section{nil, {size: 16}}, symbols: []symbol{{section: -1, value: 42}}}
			im := &image{base: 0x10000000, mem: make([]byte, 128), objects: []*object{o}, gotStart: 64, gotNext: 64, stubStart: 128, pointerSize: 8, got: map[uintptr]uintptr{}}
			if tc.format == "COFF" && tc.typ == 2 || tc.format == "ELF" && tc.typ >= 260 && tc.typ <= 262 {
				o.symbols[0].value += uint64(im.base)
			}
			if tc.format == "COFF" && (tc.typ == 8 || tc.typ == 13) {
				o.symbols[0].section, o.symbols[0].value = 1, 8
			}
			r := relocation{section: 1, offset: 1, typ: tc.typ, width: tc.width, pair: -1, pcrel: tc.format == "Mach-O" && tc.typ == 7}
			if err := im.relocate(o, r); err != nil {
				t.Fatal("byte-aligned data rejected", err)
			}
		})
	}
}
