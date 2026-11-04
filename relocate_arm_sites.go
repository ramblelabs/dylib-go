package dylib

import "fmt"

// The ADD/ADDS immediate class excludes ADDG and reserved bit-23 variants.
// Each use separately validates the shift required by its relocation.
func armAddImmediate(ins uint32) bool { return ins&0x5f800000 == 0x11000000 }

func armInstructionPlace(place uintptr) error {
	if place&3 != 0 {
		return fmt.Errorf("AArch64 instruction place must be 4-byte aligned")
	}
	return nil
}

func armPageInstruction(b []byte, place uintptr) error {
	if err := armInstructionPlace(place); err != nil {
		return err
	}
	if le.Uint32(b)&0x9f000000 != 0x90000000 {
		return fmt.Errorf("PAGE21 relocation does not reference ADRP")
	}
	return nil
}

// Defined and unresolved weak ELF targets use the same B/BL contract.
func armELFBranchInstruction(b []byte, typ uint32, place uintptr) error {
	if err := armInstructionPlace(place); err != nil {
		return err
	}
	want := uint32(0x14000000) // JUMP26 requires B; CALL26 requires BL.
	if typ == 283 {
		want = 0x94000000
	}
	if le.Uint32(b)&0xfc000000 != want {
		return fmt.Errorf("AArch64 ELF branch relocation has the wrong B/BL opcode")
	}
	return nil
}

// ELF distinguishes ADD from byte loads/stores even though both scale by zero.
// Validate the instruction and place before allocating a GOT slot or writing.
func armELFPageOffInstruction(b []byte, typ uint32, place uintptr) (uint, error) {
	if err := armInstructionPlace(place); err != nil {
		return 0, err
	}
	ins := le.Uint32(b)
	var want uint
	if typ == 277 { // ADD_ABS_LO12_NC
		if !armAddImmediate(ins) {
			return 0, fmt.Errorf("ADD_ABS_LO12_NC requires ADD/ADDS")
		}
	} else {
		if ins&0x3b000000 != 0x39000000 {
			return 0, fmt.Errorf("ELF low12 load/store requires unsigned-offset addressing")
		}
		switch typ {
		case 278: // LDST8_ABS_LO12_NC
		case 284: // LDST16_ABS_LO12_NC
			want = 1
		case 285: // LDST32_ABS_LO12_NC
			want = 2
		case 286, 312: // LDST64_ABS_LO12_NC / LD64_GOT_LO12_NC
			want = 3
		case 299: // LDST128_ABS_LO12_NC
			want = 4
		default:
			return 0, fmt.Errorf("unsupported ELF low12 relocation %d", typ)
		}
	}
	scale, err := armOffsetScale(ins)
	if err != nil {
		return 0, err
	}
	if scale != want {
		return 0, fmt.Errorf("load/store scale mismatch")
	}
	return scale, nil
}
