package dylib

import (
	"encoding/binary"
	"fmt"
	"math"
)

var le = binary.LittleEndian

func signed32(b []byte, v int64) error {
	if v < math.MinInt32 || v > math.MaxInt32 {
		return fmt.Errorf("signed 32-bit relocation overflow: %d", v)
	}
	le.PutUint32(b, uint32(v))
	return nil
}
func unsigned32(b []byte, v int64) error {
	if v < 0 || v > math.MaxUint32 {
		return fmt.Errorf("unsigned 32-bit relocation overflow: %d", v)
	}
	le.PutUint32(b, uint32(v))
	return nil
}
func signExtend(v uint32, bits uint) int64 { return int64(int32(v<<(32-bits)) >> (32 - bits)) }

func elfNoRelocation(o *object, typ uint32) bool {
	return o.info.Format == "ELF" && (typ == 0 || o.info.Arch == "arm64" && typ == 256)
}

func (im *image) relocate(o *object, r relocation) error {
	if elfNoRelocation(o, r.typ) {
		return nil
	}
	if r.section <= 0 || r.section >= len(o.sections) || o.sections[r.section] == nil {
		return fmt.Errorf("invalid target section")
	}
	sec := o.sections[r.section]
	w := 4
	switch o.info.Format {
	case "Mach-O":
		w = r.width
		stub := r.typ == machoStubFixup && machoStubWidthSupported(o.info.Arch, uint64(w))
		if r.typ == machoStubFixup && !stub || w != 4 && w != 8 && !stub {
			return fmt.Errorf("unsupported Mach-O relocation width %d", w)
		}
	case "ELF":
		if elfTLSDescCall(o.info.Arch, r.typ) {
			w = 2
		}
		if o.info.Arch == "amd64" && (r.typ == 1 || r.typ == 17 || r.typ == 24 || r.typ == 25 || r.typ >= 27 && r.typ <= 31 || r.typ == elfSize64) || o.info.Arch == "arm64" && (r.typ == 257 || r.typ == 260 || r.typ == 307) {
			w = 8
		}
		if o.info.Arch == "arm64" && (r.typ == 259 || r.typ == 262) {
			w = 2
		}
		if width, _ := elfX86Narrow(o, r.typ); width != 0 {
			w = width
		}
	case "COFF":
		if r.typ == 0 {
			return nil
		}
		if o.info.Arch == "amd64" && r.typ == 1 || o.info.Arch == "arm64" && r.typ == 14 {
			w = 8
		}
		if o.info.Arch == "amd64" && r.typ == 10 || o.info.Arch == "arm64" && r.typ == 13 || o.info.Arch == "386" && r.typ == 10 {
			w = 2
		}
	}
	if r.offset > sec.size || uint64(w) > sec.size-r.offset {
		return fmt.Errorf("relocation writes outside section")
	}
	b := im.mem[sec.offset+r.offset : sec.offset+r.offset+uint64(w)]
	p := im.base + uintptr(sec.offset+r.offset)
	if o.info.Format == "ELF" && elfSizeRelocation(o, r) {
		return im.relocELFSize(o, r, b)
	}
	if elfGOTBaseRelocation(o, r.typ) {
		return im.relocELF(o, r, b, 0, p)
	}
	if o.info.Format == "ELF" {
		if done, err := im.relocateELFTLS(o, r, b, p); done || err != nil {
			return err
		}
	}
	if o.info.Format == "Mach-O" {
		var err error
		r, err = im.machOTLSRelocation(o, r, b)
		if err != nil {
			return err
		}
	}
	if o.info.Format == "COFF" {
		if done, err := im.relocateCOFFTLS(o, r, b); done || err != nil {
			return err
		}
		sectionRelocation := r.typ == 10 || r.typ == 11
		if o.info.Arch == "arm64" {
			sectionRelocation = r.typ == 8 || r.typ == 9 || r.typ == 10 || r.typ == 11 || r.typ == 13
		}
		if sectionRelocation {
			return im.relocCOFFSection(o, r, b)
		}
	}
	s, e := im.symbol(o, r.symbol, r.local)
	if e != nil {
		return e
	}
	if o.info.Format == "ELF" && o.info.Arch == "arm64" && s == 0 {
		var done bool
		s, done, e = im.elfARM64Weak(o, r, b, p)
		if e != nil || done {
			return e
		}
	}
	if o.info.Format == "Mach-O" && (r.typ == machoPointerFixup || r.typ == machoStubFixup) {
		return im.relocateMachOIndirect(o, r, b, s, p)
	}
	if r.pair >= 0 {
		if r.local || r.pcrel {
			return fmt.Errorf("invalid subtractor flags")
		}
		end, e := im.symbol(o, r.pair, false)
		if e != nil {
			return e
		}
		if w == 8 {
			le.PutUint64(b, uint64(int64(end)-int64(s)+int64(le.Uint64(b))+r.addend))
			return nil
		}
		return signed32(b, int64(end)-int64(s)+int64(int32(le.Uint32(b)))+r.addend)
	}
	switch o.info.Format {
	case "ELF":
		return im.relocELF(o, r, b, s, p)
	case "Mach-O":
		return im.relocMachO(o, r, b, s, p)
	case "COFF":
		return im.relocCOFF(o, r, b, s, p)
	}
	return fmt.Errorf("no relocation backend")
}

// COFF SECTION and SECREL encode a section ordinal or an offset in that
// section, not a process address. This image keeps each input section separate.
func (im *image) relocCOFFSection(o *object, r relocation, b []byte) error {
	if r.symbol < 0 || r.symbol >= len(o.symbols) {
		return fmt.Errorf("invalid section-relative symbol")
	}
	d, err := im.resolveDefinition(o, r.symbol)
	if err != nil {
		return err
	}
	o, s := d.o, d.sym()
	if s.section <= 0 || s.section >= len(o.sections) || o.sections[s.section] == nil || s.value > o.sections[s.section].size {
		return fmt.Errorf("section-relative relocation requires a symbol in a loaded object section")
	}
	if len(b) == 2 {
		ordinal := uint64(0)
		found := false
		for _, object := range im.objects {
			for i, section := range object.sections {
				if section == nil {
					continue
				}
				ordinal++
				if object == o && i == s.section {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		ordinal += uint64(le.Uint16(b))
		if !found || ordinal > math.MaxUint16 {
			return fmt.Errorf("COFF section ordinal overflow or missing section")
		}
		le.PutUint16(b, uint16(ordinal))
		return nil
	}
	value := s.value
	if sec := o.sections[s.section]; sec.nativeTLS == coffTLSSection {
		if im.coffTLS == nil {
			return fmt.Errorf("native COFF TLS requires an image-owned template")
		}
		offset, ok := im.coffTLS.sections[sec]
		if !ok {
			return fmt.Errorf("foreign native COFF TLS template")
		}
		value += offset
	}
	if o.info.Arch != "arm64" || r.typ == 8 {
		if value+uint64(le.Uint32(b)) > math.MaxUint32 {
			return fmt.Errorf("COFF section-relative offset overflow")
		}
		le.PutUint32(b, uint32(value)+le.Uint32(b))
		return nil
	}
	ins := le.Uint32(b)
	if r.typ == 11 {
		scale, err := armOffsetScale(ins)
		if err != nil || ins&0x3b000000 != 0x39000000 {
			return fmt.Errorf("SECREL_LOW12L requires an unsigned-offset load/store")
		}
		value += uint64((ins>>10)&0xfff) << scale
		return armPageOff(b, uintptr(value), scale)
	}
	if ins&0x5f000000 != 0x11000000 {
		return fmt.Errorf("SECREL ADD relocation requires ADD/ADDS")
	}
	shift := uint(0)
	if r.typ == 10 {
		shift = 12
	}
	if (ins>>22)&1 != uint32(shift/12) {
		return fmt.Errorf("SECREL ADD shift mismatch")
	}
	// COFF HIGH12A's encoded immediate is a byte addend before extracting
	// bits 12..23, even though the final ADD executes with a 12-bit shift.
	value += uint64((ins >> 10) & 0xfff)
	if value > math.MaxUint32 {
		return fmt.Errorf("COFF section-relative offset overflow")
	}
	if shift == 12 && value >= 1<<24 {
		return fmt.Errorf("SECREL_HIGH12A offset overflow")
	}
	field := uint32(value>>shift) & 0xfff
	le.PutUint32(b, (ins&^uint32(0xfff<<10))|field<<10)
	return nil
}

func (im *image) relocELF(o *object, r relocation, b []byte, s, p uintptr) error {
	if width, relative := elfX86Narrow(o, r.typ); width != 0 {
		return relocateELFX86Narrow(o, r, b, s, p, relative)
	}
	if o.info.Arch == "386" {
		return im.relocELF386(o, r, b, s, p)
	}
	v := int64(s) + r.addend
	if o.info.Arch == "amd64" {
		switch r.typ {
		case 3, 27, 28, 30: // GOT32, GOT64, GOTPCREL64, obsolete GOTPLT64.
			g, e := im.gotSlot(s)
			if e != nil {
				return e
			}
			d := int64(g) - int64(im.gotBase()) + r.addend
			if r.typ == 3 {
				return signed32(b, d)
			}
			if r.typ == 28 {
				d = int64(g) - int64(p) + r.addend
			}
			le.PutUint64(b, uint64(d))
			return nil
		case 25, 31: // GOTOFF64 / PLTOFF64; all providers bind eagerly.
			le.PutUint64(b, uint64(v)-uint64(im.gotBase()))
			return nil
		case 26, 29: // GOTPC32 / GOTPC64; the symbol is not an operand.
			d := int64(im.gotBase()) - int64(p) + r.addend
			if r.typ == 26 {
				return signed32(b, d)
			}
			le.PutUint64(b, uint64(d))
			return nil
		case 1:
			le.PutUint64(b, uint64(v))
			return nil
		case 2:
			return signed32(b, v-int64(p))
		case 4:
			d := v - int64(p)
			if d < math.MinInt32 || d > math.MaxInt32 {
				stub, e := im.stub(s, "amd64")
				if e != nil {
					return e
				}
				d = int64(stub) + r.addend - int64(p)
			}
			return signed32(b, d)
		case 9, 41, 42:
			g, e := im.gotSlot(s)
			if e != nil {
				return e
			}
			return signed32(b, int64(g)+r.addend-int64(p))
		case 10:
			return unsigned32(b, v)
		case 11:
			return signed32(b, v)
		case 24:
			le.PutUint64(b, uint64(v-int64(p)))
			return nil
		}
	} else if o.info.Arch == "arm64" {
		switch r.typ {
		case 257:
			le.PutUint64(b, uint64(v))
			return nil
		case 258, 259:
			return armDataRelocation(b, v, false)
		case 260:
			le.PutUint64(b, uint64(v-int64(p)))
			return nil
		case 261:
			return signed32(b, v-int64(p))
		case 262:
			return armDataRelocation(b, v-int64(p), true)
		case 263, 264, 265, 266, 267, 268, 269, 270, 271, 272, 287, 288, 289, 290, 291, 292, 293:
			return armELFMOVW(b, r.typ, uint64(v), p)
		case 273: // LD_PREL_LO19
			return armLiteralLoad(b, uintptr(v), p)
		case 274: // ADR_PREL_LO21
			return armADR(b, uintptr(v), p)
		case 275, 276:
			return armPageRelocation(b, uintptr(v), p, r.typ == 275)
		case 277:
			return armPageOff(b, uintptr(v), 0)
		case 278:
			return armPageOff(b, uintptr(v), 0)
		case 284:
			return armPageOff(b, uintptr(v), 1)
		case 285:
			return armPageOff(b, uintptr(v), 2)
		case 286:
			return armPageOff(b, uintptr(v), 3)
		case 299:
			return armPageOff(b, uintptr(v), 4)
		case 279: // TSTBR14
			return armConditionalBranch(b, uintptr(v), p, 14)
		case 280: // CONDBR19
			return armConditionalBranch(b, uintptr(v), p, 19)
		case 282, 283:
			return im.armBranch(b, uintptr(v), p)
		case 300, 301, 302, 303, 304, 305, 306:
			g, e := im.elfARM64GOT(r, s)
			if e != nil {
				return e
			}
			return armELFMOVW(b, r.typ, uint64(g)-uint64(im.gotBase()), p)
		case 307:
			le.PutUint64(b, uint64(v)-uint64(im.gotBase()))
			return nil
		case 308:
			return signed32(b, v-int64(im.gotBase()))
		case 309: // GOT_LD_PREL19 must load a full native pointer.
			if le.Uint32(b)&0xff000000 != 0x58000000 {
				return fmt.Errorf("GOT_LD_PREL19 requires a 64-bit LDR literal")
			}
			g, e := im.elfARM64GOT(r, s)
			if e != nil {
				return e
			}
			return armLiteralLoad(b, g, p)
		case 311:
			g, e := im.elfARM64GOT(r, s)
			if e != nil {
				return e
			}
			return armPage(b, g, p)
		case 312:
			g, e := im.elfARM64GOT(r, s)
			if e != nil {
				return e
			}
			return armPageOff(b, g, 3)
		case 310, 313:
			g, e := im.elfARM64GOT(r, s)
			if e != nil {
				return e
			}
			base := im.gotBase()
			if r.typ == 313 {
				base &^= 0xfff
			}
			return armGOTOffset15(b, g, base, p)
		case 314: // PLT32 data: the addend is a field bias, not a thunk target.
			d := v - int64(p)
			if d < math.MinInt32 || d > math.MaxInt32 {
				stub, e := im.stub(s, "arm64")
				if e != nil {
					return e
				}
				d = int64(stub) + r.addend - int64(p)
			}
			return signed32(b, d)
		case 315: // GOTPCREL32: A biases the field, while the slot holds S.
			g, e := im.gotSlot(s)
			if e != nil {
				return e
			}
			return signed32(b, int64(g)+r.addend-int64(p))
		}
	}
	return fmt.Errorf("unsupported %s ELF relocation %d", o.info.Arch, r.typ)
}

func (im *image) relocMachO(o *object, r relocation, b []byte, s, p uintptr) error {
	if r.typ == 0 {
		if r.pcrel {
			return fmt.Errorf("PC-relative unsigned relocation")
		}
		v := uint64(s) + uint64(r.addend)
		if len(b) == 8 {
			v += le.Uint64(b)
		} else {
			v += uint64(le.Uint32(b))
		}
		if r.local {
			v -= o.sections[r.symbol].original
		}
		if len(b) == 8 {
			le.PutUint64(b, v)
			return nil
		}
		return unsigned32(b, int64(v))
	}
	if len(b) != 4 {
		return fmt.Errorf("instruction/PC-relative relocation requires 4 bytes")
	}
	if o.info.Arch == "amd64" {
		if !r.pcrel {
			return fmt.Errorf("x86-64 relocation requires PC-relative flag")
		}
		add := int64(int32(le.Uint32(b))) + r.addend
		bias := int64(4)
		if r.local {
			add += int64(o.sections[r.section].original+r.offset+4) - int64(o.sections[r.symbol].original)
		}
		switch r.typ {
		case 1:
		case 2:
			d := int64(s) + add - int64(p) - bias
			if d < math.MinInt32 || d > math.MaxInt32 {
				var e error
				s, e = im.stub(uintptr(int64(s)+add), "amd64")
				if e != nil {
					return e
				}
				add = 0 // The island already targets the selected interior address.
			}
		case 3, 4:
			var e error
			s, e = im.gotSlot(s)
			if e != nil {
				return e
			}
		case 6, 7, 8:
			// SIGNED_1/2/4 encode the trailing immediate-byte correction
			// in the stored addend already. Subtracting it again is wrong.
		default:
			return fmt.Errorf("unsupported x86-64 Mach-O relocation %d", r.typ)
		}
		return signed32(b, int64(s)+add-int64(p)-bias)
	}
	if o.info.Arch == "arm64" {
		if r.local {
			return fmt.Errorf("ARM64 instruction relocations require external symbol indices")
		}
		ins := le.Uint32(b)
		switch r.typ {
		case 2:
			if !r.pcrel {
				return fmt.Errorf("BRANCH26 requires PC-relative flag")
			}
			return im.armBranch(b, uintptr(int64(s)+r.addend+signExtend(ins&0x3ffffff, 26)*4), p)
		case 3, 5:
			if !r.pcrel {
				return fmt.Errorf("PAGE21 requires PC-relative flag")
			}
			if r.typ == 5 {
				var e error
				s, e = im.gotSlot(s)
				if e != nil {
					return e
				}
			}
			add := signExtend(((ins>>29)&3)|((ins>>5)&0x7ffff)<<2, 21) << 12
			return armPage(b, uintptr(int64(s)+r.addend+add), p)
		case 4, 6:
			if r.pcrel {
				return fmt.Errorf("PAGEOFF12 must not be PC-relative")
			}
			if r.typ == 6 {
				var e error
				s, e = im.gotSlot(s)
				if e != nil {
					return e
				}
			}
			shift, e := armOffsetScale(ins)
			if e != nil {
				return e
			}
			add := int64((ins>>10)&0xfff) << shift
			return armPageOff(b, uintptr(int64(s)+r.addend+add), shift)
		case 7:
			if !r.pcrel {
				return fmt.Errorf("POINTER_TO_GOT requires PC-relative flag")
			}
			g, e := im.gotSlot(s)
			if e != nil {
				return e
			}
			// ARM64 POINTER_TO_GOT has no embedded addend. In particular,
			// assembler CIE personality fields can contain a placeholder.
			return signed32(b, int64(g)+r.addend-int64(p))
		}
	}
	return fmt.Errorf("unsupported %s Mach-O relocation %d", o.info.Arch, r.typ)
}

func (im *image) relocCOFF(o *object, r relocation, b []byte, s, p uintptr) error {
	if o.info.Arch == "arm64" {
		return im.relocCOFFARM64(r, b, s, p)
	}
	if o.info.Arch == "386" {
		switch r.typ {
		case 6: // DIR32
			if uint64(s) > math.MaxUint32 {
				return fmt.Errorf("32-bit COFF address overflow")
			}
			le.PutUint32(b, uint32(s)+le.Uint32(b))
			return nil
		case 7: // DIR32NB
			return unsigned32(b, int64(s)-int64(im.base)+int64(le.Uint32(b)))
		case 20: // REL32 wraps within the 32-bit address space.
			le.PutUint32(b, uint32(s)-uint32(p)-4+le.Uint32(b))
			return nil
		}
		return fmt.Errorf("unsupported i386 COFF relocation %d", r.typ)
	}
	if o.info.Arch != "amd64" {
		return fmt.Errorf("only AMD64 COFF relocation implemented")
	}
	switch r.typ {
	case 1:
		le.PutUint64(b, uint64(s)+le.Uint64(b))
		return nil
	case 2:
		return unsigned32(b, int64(s)+int64(le.Uint32(b)))
	case 3:
		return unsigned32(b, int64(s)-int64(im.base)+int64(le.Uint32(b)))
	case 4, 5, 6, 7, 8, 9:
		add := int64(int32(le.Uint32(b)))
		d := int64(s) + add - int64(p) - 4 - int64(r.typ-4)
		if d < math.MinInt32 || d > math.MaxInt32 {
			sec := o.sections[r.section]
			off := sec.offset + r.offset
			// REL32 also relocates data. Only executable E8/E9 or 0F 8x
			// branch operands can redirect through a nearby jump stub.
			direct := r.offset > 0 && (im.mem[off-1] == 0xe8 || im.mem[off-1] == 0xe9)
			conditional := r.offset >= 2 && im.mem[off-2] == 0x0f && im.mem[off-1]&0xf0 == 0x80
			if !sec.exec || !direct && !conditional {
				return fmt.Errorf("COFF data reference out of range; compile position-independent code or use __declspec(dllimport)")
			}
			// The CPU branches from the displacement's end. Fold any COFF
			// trailing-byte bias and embedded addend into the absolute target,
			// never into the offset of the stub's instruction bytes.
			offset := add - int64(r.typ-4)
			target := s + uintptr(offset)
			if offset > 0 && target < s || offset < 0 && target > s {
				return fmt.Errorf("COFF branch target address overflow")
			}
			stub, e := im.stub(target, "amd64")
			if e != nil {
				return e
			}
			d = int64(stub) - int64(p) - 4
		}
		return signed32(b, d)
	default:
		return fmt.Errorf("unsupported AMD64 COFF relocation %d", r.typ)
	}
}

func armPage(b []byte, target, p uintptr) error {
	return armPageRelocation(b, target, p, true)
}

func armPageRelocation(b []byte, target, p uintptr, checkRange bool) error {
	ins := le.Uint32(b)
	if ins&0x9f000000 != 0x90000000 {
		return fmt.Errorf("PAGE21 relocation does not reference ADRP")
	}
	d := (int64(target&^0xfff) - int64(p&^0xfff)) >> 12
	if checkRange && (d < -(1<<20) || d >= (1<<20)) {
		return fmt.Errorf("ADRP page displacement overflow")
	}
	v := uint32(d) & 0x1fffff
	ins = (ins &^ uint32((3<<29)|(0x7ffff<<5))) | ((v & 3) << 29) | ((v >> 2) << 5)
	le.PutUint32(b, ins)
	return nil
}
func armOffsetScale(ins uint32) (uint, error) {
	if ins&0x5f000000 == 0x11000000 {
		if ins&(1<<22) != 0 {
			return 0, fmt.Errorf("shifted ADD not supported for PAGEOFF12")
		}
		return 0, nil
	}
	if ins&0x3b000000 == 0x39000000 {
		scale := uint(ins >> 30)
		if ins&(1<<26) != 0 && ins&(1<<23) != 0 {
			scale = 4
		}
		return scale, nil
	}
	return 0, fmt.Errorf("PAGEOFF12 requires ADD or unsigned-offset load/store")
}
func armPageOff(b []byte, target uintptr, scale uint) error {
	ins := le.Uint32(b)
	got, e := armOffsetScale(ins)
	if e != nil {
		return e
	}
	if got != scale {
		return fmt.Errorf("load/store scale mismatch")
	}
	off := uint32(target & 0xfff)
	if off&((1<<scale)-1) != 0 {
		return fmt.Errorf("unaligned page offset")
	}
	le.PutUint32(b, (ins&^uint32(0xfff<<10))|((off>>scale)<<10))
	return nil
}
func (im *image) armBranch(b []byte, target, p uintptr) error {
	ins := le.Uint32(b)
	if ins&0x7c000000 != 0x14000000 {
		return fmt.Errorf("BRANCH26 does not reference B/BL")
	}
	d := int64(target) - int64(p)
	if d%4 != 0 {
		return fmt.Errorf("unaligned branch target")
	}
	if d < -(1<<27) || d >= (1<<27) {
		stub, e := im.stub(target, "arm64")
		if e != nil {
			return e
		}
		d = int64(stub) - int64(p)
	}
	if d%4 != 0 || d < -(1<<27) || d >= (1<<27) {
		return fmt.Errorf("branch stub out of range")
	}
	le.PutUint32(b, (ins&0xfc000000)|(uint32(d>>2)&0x3ffffff))
	return nil
}
