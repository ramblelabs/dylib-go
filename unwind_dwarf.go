package dylib

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/ramblelabs/dylib-go/internal/native"
)

// Keep registration and deregistration paired with one retained provider.
func (im *image) registerDwarfFrames(providerName string, frames []uintptr) error {
	provider, err := native.Open(providerName)
	if err != nil {
		return fmt.Errorf("DWARF unwind provider %s: %w", providerName, err)
	}
	register := native.Lookup(provider, "__register_frame")
	deregister := native.Lookup(provider, "__deregister_frame")
	if register == 0 || deregister == 0 {
		native.Close(provider)
		return fmt.Errorf("%s is missing frame registration functions", providerName)
	}
	u := &runtimeFunctions{provider: provider, deregister: deregister}
	im.unwind = u
	for _, frame := range frames {
		native.CallPointer(register, frame)
		u.frames = append(u.frames, frame)
	}
	u.registered = true
	return nil
}

type frameCIE struct {
	encoding    byte
	augmented   bool
	lsda        byte
	personality bool
}

type framePolicy struct {
	pcRelative bool
	language   bool
	fixup      func(uint64, byte) error
	lsdaFixup  func(uint64, byte) error
	record     func(start, length uint64, fde uintptr, personality bool) error

	personalityFixup func(uint64, byte) error
}

// Keep a C-only validator for metadata callers that reject language augmentation.
func (im *image) validateDWARFFrames(b []byte, imageOffset uint64, pcRelative bool, fixup func(uint64, byte) error) ([]uintptr, error) {
	return im.validateFrameRecords(b, imageOffset, framePolicy{pcRelative: pcRelative, fixup: fixup})
}

func (im *image) validateFrameRecords(b []byte, imageOffset uint64, policy framePolicy) ([]uintptr, error) {
	cies := make(map[int]frameCIE)
	var fdes []uintptr
	for offset := 0; offset < len(b); {
		if len(b)-offset < 4 {
			return nil, fmt.Errorf("truncated frame length")
		}
		size := uint64(binary.LittleEndian.Uint32(b[offset:]))
		if size == 0 {
			if !allZero(b[offset:]) {
				return nil, fmt.Errorf("data after frame terminator")
			}
			return fdes, nil
		}
		if size == 0xffffffff {
			return nil, fmt.Errorf("DWARF64 frame records are unsupported")
		}
		if size < 4 || size > uint64(len(b)-offset-4) {
			return nil, fmt.Errorf("invalid frame record length")
		}
		end := offset + 4 + int(size)
		id := binary.LittleEndian.Uint32(b[offset+4:])
		r := frameReader{b: b[offset+8 : end]}
		if id == 0 {
			version, err := r.byte()
			if err != nil || version != 1 && version != 3 {
				return nil, fmt.Errorf("unsupported CIE version")
			}
			zero := bytes.IndexByte(r.b, 0)
			if zero < 0 {
				return nil, fmt.Errorf("unterminated CIE augmentation")
			}
			augmentation := string(r.b[:zero])
			r.b = r.b[zero+1:]
			if augmentation != "" && augmentation != "zR" && (!policy.language || augmentation[0] != 'z') {
				return nil, fmt.Errorf("unsupported CIE augmentation %q", augmentation)
			}
			code, err := r.leb(false)
			if err != nil || code == 0 {
				return nil, fmt.Errorf("invalid CIE code alignment")
			}
			if _, err := r.leb(true); err != nil {
				return nil, err
			}
			if version == 1 {
				_, err = r.byte()
			} else {
				_, err = r.leb(false)
			}
			if err != nil {
				return nil, err
			}
			cie := frameCIE{augmented: augmentation != "", lsda: 0xff}
			if cie.augmented {
				n, err := r.leb(false)
				if err != nil || n > uint64(len(r.b)) {
					return nil, fmt.Errorf("invalid CIE augmentation data")
				}
				data := frameReader{b: r.b[:int(n)]}
				dataOffset := imageOffset + uint64(end-len(r.b))
				r.b = r.b[int(n):]
				seen := make(map[byte]bool)
				for _, letter := range []byte(augmentation[1:]) {
					if seen[letter] {
						return nil, fmt.Errorf("duplicate CIE augmentation %q", letter)
					}
					seen[letter] = true
					switch letter {
					case 'R':
						cie.encoding, err = data.byte()
					case 'L':
						if !policy.language {
							return nil, fmt.Errorf("unsupported CIE augmentation %q", augmentation)
						}
						cie.lsda, err = data.byte()
						if err == nil && cie.lsda != 0xff {
							_, err = frameEncodingSize(cie.lsda&^0x80, im.pointerSize)
						}
					case 'P':
						if !policy.language {
							return nil, fmt.Errorf("unsupported CIE augmentation %q", augmentation)
						}
						var encoding byte
						encoding, err = data.byte()
						if err == nil && policy.personalityFixup != nil {
							err = policy.personalityFixup(dataOffset+uint64(n)-uint64(len(data.b))-imageOffset, encoding)
						}
						if err == nil {
							var pointer uint64
							pointer, err = im.framePointer(&data, encoding, dataOffset+uint64(n)-uint64(len(data.b)))
							if err == nil && pointer == 0 {
								err = fmt.Errorf("null CIE personality")
							}
							if err == nil && pointer >= uint64(im.base) && pointer-uint64(im.base) < uint64(len(im.mem)) && !im.unwindRange(pointer-uint64(im.base), 1, true) {
								err = fmt.Errorf("CIE personality is not executable")
							}
						}
					default:
						return nil, fmt.Errorf("unsupported CIE augmentation %q", augmentation)
					}
					if err != nil {
						return nil, err
					}
				}
				if len(data.b) != 0 {
					return nil, fmt.Errorf("unused CIE augmentation data")
				}
				if seen['L'] && !seen['P'] {
					return nil, fmt.Errorf("LSDA requires a CIE personality")
				}
				cie.personality = seen['P']
			}
			if _, err := frameEncodingSize(cie.encoding, im.pointerSize); err != nil {
				return nil, err
			}
			if policy.pcRelative && cie.encoding&0x70 != 0x10 {
				return nil, fmt.Errorf("macOS FDEs require PC-relative addressing")
			}
			if err := r.cfi(); err != nil {
				return nil, err
			}
			cies[offset] = cie
		} else {
			target := int64(offset) + 4 - int64(id)
			cie, ok := cies[int(target)]
			if target < 0 || !ok {
				return nil, fmt.Errorf("FDE does not reference a preceding CIE")
			}
			addressOffset := imageOffset + uint64(offset+8)
			if policy.fixup != nil {
				if err := policy.fixup(uint64(offset+8), cie.encoding); err != nil {
					return nil, err
				}
			}
			start, err := r.encoded(cie.encoding, im.pointerSize)
			if err != nil {
				return nil, err
			}
			if cie.encoding&0x70 == 0x10 {
				start += int64(im.base) + int64(addressOffset)
			}
			length, err := r.encoded(cie.encoding&15, im.pointerSize)
			if err != nil || length <= 0 || start < int64(im.base) || !im.unwindRange(uint64(start-int64(im.base)), uint64(length), true) {
				return nil, fmt.Errorf("FDE range is outside an executable section")
			}
			if cie.augmented {
				n, err := r.leb(false)
				if err != nil || n > uint64(len(r.b)) {
					return nil, fmt.Errorf("unsupported FDE augmentation data")
				}
				data := frameReader{b: r.b[:int(n)]}
				dataOffset := imageOffset + uint64(end-len(r.b))
				r.b = r.b[int(n):]
				if cie.lsda != 0xff {
					if policy.lsdaFixup != nil {
						if err := policy.lsdaFixup(dataOffset-imageOffset, cie.lsda); err != nil {
							return nil, err
						}
					}
					pointer, err := im.framePointer(&data, cie.lsda, dataOffset)
					if err != nil {
						return nil, err
					}
					if pointer != 0 && !im.frameLSDA(pointer) {
						return nil, fmt.Errorf("FDE LSDA is outside read-only exception data")
					}
				}
				if len(data.b) != 0 {
					return nil, fmt.Errorf("unused FDE augmentation data")
				}
			}
			if err := r.cfi(); err != nil {
				return nil, err
			}
			if policy.record != nil {
				if err := policy.record(uint64(start), uint64(length), im.base+uintptr(imageOffset)+uintptr(offset), cie.personality); err != nil {
					return nil, err
				}
			}
			fdes = append(fdes, im.base+uintptr(imageOffset)+uintptr(offset))
		}
		offset = end
	}
	return fdes, nil
}

type frameReader struct{ b []byte }

func (r *frameReader) take(n int) ([]byte, error) {
	if n < 0 || n > len(r.b) {
		return nil, fmt.Errorf("truncated frame data")
	}
	b := r.b[:n]
	r.b = r.b[n:]
	return b, nil
}
func (r *frameReader) byte() (byte, error) {
	b, err := r.take(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}
func (r *frameReader) leb(signed bool) (uint64, error) {
	var value uint64
	for i := 0; i < 10; i++ {
		b, err := r.byte()
		if err != nil {
			return 0, err
		}
		if i == 9 && (signed && b&0x7f != 0 && b&0x7f != 0x7f || !signed && b&0x7f > 1) {
			return 0, fmt.Errorf("frame LEB128 overflow")
		}
		value |= uint64(b&0x7f) << uint(i*7)
		if b&0x80 == 0 {
			if signed && b&0x40 != 0 && i < 9 {
				value |= ^uint64(0) << uint((i+1)*7)
			}
			return value, nil
		}
	}
	return 0, fmt.Errorf("unterminated frame LEB128")
}
func frameEncodingSize(encoding byte, pointerSize uint64) (int, error) {
	if encoding&0xf0 != 0 && encoding&0xf0 != 0x10 {
		return 0, fmt.Errorf("unsupported frame pointer encoding %#x", encoding)
	}
	switch encoding & 15 {
	case 0:
		return int(pointerSize), nil
	case 3, 11:
		return 4, nil
	case 4, 12:
		return 8, nil
	default:
		return 0, fmt.Errorf("unsupported frame pointer encoding %#x", encoding)
	}
}
func (r *frameReader) encoded(encoding byte, pointerSize uint64) (int64, error) {
	n, err := frameEncodingSize(encoding, pointerSize)
	if err != nil {
		return 0, err
	}
	b, err := r.take(n)
	if err != nil {
		return 0, err
	}
	if n == 4 {
		v := binary.LittleEndian.Uint32(b)
		if encoding&15 == 11 {
			return int64(int32(v)), nil
		}
		return int64(v), nil
	}
	return int64(binary.LittleEndian.Uint64(b)), nil
}

// Check operand boundaries before handing CFI to libgcc. Native code and its
// CFI remain trusted; validation is not a proof of the machine-code prolog.
func (r *frameReader) cfi() error {
	depth := 0
	for len(r.b) != 0 {
		op, _ := r.byte()
		unsigned, signed, fixed := 0, 0, 0
		switch op & 0xc0 {
		case 0x40, 0xc0:
			continue
		case 0x80:
			unsigned = 1
		default:
			switch op {
			case 0:
				continue
			case 2:
				fixed = 1
			case 3:
				fixed = 2
			case 4:
				fixed = 4
			case 5, 9, 12, 20:
				unsigned = 2
			case 6, 7, 8, 13, 14, 46:
				unsigned = 1
			case 10:
				depth++
				continue
			case 11:
				if depth == 0 {
					return fmt.Errorf("unbalanced CFI restore_state")
				}
				depth--
				continue
			case 17, 18, 21:
				unsigned, signed = 1, 1
			case 19:
				signed = 1
			default:
				return fmt.Errorf("unsupported CFI opcode %#x", op)
			}
		}
		if _, err := r.take(fixed); err != nil {
			return err
		}
		for i := 0; i < unsigned; i++ {
			if _, err := r.leb(false); err != nil {
				return err
			}
		}
		for i := 0; i < signed; i++ {
			if _, err := r.leb(true); err != nil {
				return err
			}
		}
	}
	return nil
}
