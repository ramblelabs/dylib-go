package dylib

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"fmt"
	"strings"
)

func machoArch(cpu macho.Cpu) string {
	switch cpu {
	case macho.CpuAmd64:
		return "amd64"
	case macho.CpuArm64:
		return "arm64"
	default:
		return cpu.String()
	}
}

func parseMachO(name string, b []byte) (*file, error) {
	f, err := macho.NewFile(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	o := &object{info: Info{Name: name, Format: "Mach-O", Kind: "object", Bits: 64}}
	o.info.OS = "darwin"
	o.info.CPUSubtype = f.SubCpu
	for _, load := range f.Loads {
		raw := load.Raw()
		if len(raw) < 8 {
			continue
		}
		cmd := f.ByteOrder.Uint32(raw)
		if cmd == 0x32 && len(raw) >= 12 { // LC_BUILD_VERSION
			if platform := f.ByteOrder.Uint32(raw[8:]); platform != 1 {
				o.info.OS = fmt.Sprintf("apple-platform-%d", platform)
			}
		} else if cmd == 0x25 || cmd == 0x2f || cmd == 0x30 {
			o.info.OS = "apple-mobile"
		}
	}
	if f.Magic != macho.Magic64 {
		o.info.Bits = 32
		o.unsupported("32-bit Mach-O")
	}
	if f.ByteOrder != binary.LittleEndian {
		o.unsupported("big-endian Mach-O")
	}
	o.info.Arch = machoArch(f.Cpu)
	switch f.Cpu {
	case macho.CpuAmd64:
	case macho.CpuArm64:
		if f.SubCpu&0xffffff > 1 {
			o.unsupported(fmt.Sprintf("ARM64 subtype %#x (including arm64e authentication)", f.SubCpu))
		}
	default:
		o.unsupported("machine " + f.Cpu.String())
	}
	switch f.Type {
	case macho.TypeObj:
	case macho.TypeDylib, macho.TypeBundle:
		o.info.Kind = "shared"
	default:
		o.info.Kind = "executable"
	}
	if f.Symtab != nil {
		var indirect []machoIndirect
		for _, s := range f.Symtab.Syms {
			name := s.Name
			// debug/macho already removes one underscore from dotted names.
			if !strings.Contains(name, ".") {
				name = strings.TrimPrefix(name, "_")
			}
			v := symbol{name: name, section: int(s.Sect), value: s.Value, global: s.Type&1 != 0, weak: s.Desc&(0x40|0x80) != 0}
			if s.Type&0xe0 != 0 {
				v.section = -3
				v.global = false
			} else {
				switch s.Type & 0x0e {
				case 0:
					if s.Value != 0 {
						v.section = -2
						v.size = s.Value
						v.value = 0
						v.align = uint64(1) << ((s.Desc >> 8) & 15)
					}
				case 2:
					v.section = -1
				case 0xa: // N_INDR: n_value indexes the target's string, not an address.
					if !v.global {
						v.section = -3
						break
					}
					target, err := machoIndirectTarget(f, b, s)
					if err != nil {
						return nil, err
					}
					v.section, v.value, v.weak = -5, 0, false
					indirect = append(indirect, machoIndirect{len(o.symbols), target})
				case 0xe:
					if int(s.Sect) == 0 || int(s.Sect) > len(f.Sections) {
						return nil, fmt.Errorf("invalid symbol section")
					}
					base := f.Sections[s.Sect-1].Addr
					if s.Value < base {
						return nil, fmt.Errorf("symbol before section")
					}
					v.value -= base
				default:
					v.section = -3
					o.unsupported("indirect Mach-O symbol " + s.Name)
				}
			}
			o.symbols = append(o.symbols, v)
		}
		bindMachOIndirect(o, indirect)
	}
	if o.info.Kind != "object" {
		return finish(o), nil
	}
	if text := f.Segment("__TEXT"); text != nil {
		o.originalBase = text.Addr
	}
	o.sections = make([]*section, len(f.Sections)+1)
	for i, s := range f.Sections {
		if s.Flags&0x02000000 != 0 && s.Name != "__compact_unwind" || s.Seg == "__DWARF" {
			continue
		}
		t := s.Flags & 0xff
		if t == 0x14 || t == 0x15 {
			o.unsupported("TLS section " + s.Name)
		}
		if strings.HasPrefix(s.Name, "__objc_") || strings.HasPrefix(s.Name, "__swift5_") {
			o.unsupported("language runtime registration: " + s.Name)
		}
		if s.Align > 26 {
			return nil, fmt.Errorf("section alignment too large")
		}
		v := &section{name: s.Name, size: s.Size, align: uint64(1) << s.Align, original: s.Addr, exec: t == 8 || s.Flags&(0x80000000|0x400) != 0, write: s.Seg == "__DATA" || s.Seg == "__DATA_CONST"}
		if t >= 0x11 && t <= 0x13 {
			v.nativeTLS = t
		}
		if t == 9 {
			v.lifecycle = lifecycleInit
		} else if t == 10 {
			v.lifecycle = lifecycleFini
		} else if t == 0x16 { // S_INIT_FUNC_OFFSETS, synthesized by the linker.
			v.lifecycle, v.initOffsets = lifecycleInit, true
			if len(s.Relocs) != 0 {
				return nil, fmt.Errorf("%s: initializer offsets must be linker-resolved", s.Name)
			}
		}
		if v.lifecycle != 0 {
			if err := configureLifecycle(v, o.info.Bits); err != nil {
				return nil, err
			}
		}
		if err := validSection(v); err != nil {
			return nil, err
		}
		if t != 1 && t != 0xc && t != 0x12 {
			v.data, err = s.Data()
			if err != nil {
				return nil, err
			}
		}
		o.sections[i+1] = v
		for j := 0; j < len(s.Relocs); j++ {
			r := s.Relocs[j]
			if r.Scattered {
				return nil, fmt.Errorf("scattered relocations are unsupported")
			}
			addend := int64(0)
			if o.info.Arch == "arm64" && r.Type == uint8(macho.ARM64_RELOC_ADDEND) {
				addend = int64(int32(r.Value<<8) >> 8)
				j++
				if j >= len(s.Relocs) || s.Relocs[j].Addr != r.Addr {
					return nil, fmt.Errorf("unpaired ARM64 addend")
				}
				r = s.Relocs[j]
			}
			v := relocation{section: i + 1, offset: uint64(r.Addr), typ: uint32(r.Type), symbol: int(r.Value), width: 1 << r.Len, pcrel: r.Pcrel, local: !r.Extern, addend: addend, pair: -1}
			if o.info.Arch == "arm64" && r.Type == 1 || o.info.Arch == "amd64" && r.Type == 5 {
				j++
				if !r.Extern || j >= len(s.Relocs) {
					return nil, fmt.Errorf("unpaired subtractor")
				}
				next := s.Relocs[j]
				if !next.Extern || next.Type != 0 || next.Addr != r.Addr || next.Len != r.Len {
					return nil, fmt.Errorf("invalid subtractor pair")
				}
				v.pair = int(next.Value)
			}
			o.relocs = append(o.relocs, v)
		}
	}
	if err := bindMachOIndirectTables(o, f); err != nil {
		return nil, err
	}
	if err := configureMachOCoalescing(o, f, b); err != nil {
		o.unsupported("Mach-O coalesced atoms: " + err.Error())
	}
	return finish(o), nil
}
