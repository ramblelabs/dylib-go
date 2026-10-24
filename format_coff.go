package dylib

import (
	"debug/pe"
	"fmt"
	"strings"

	"github.com/ramblelabs/dylib-go/internal/coffname"
)

func parseCOFF(name string, b []byte) (*file, error) {
	f, err := readCOFF(b)
	if err != nil {
		return nil, err
	}
	o := &object{info: Info{Name: name, Format: "COFF", Kind: f.kind, Bits: 64}, timestamp: f.timestamp}
	o.info.OS = "windows"
	switch f.machine {
	case pe.IMAGE_FILE_MACHINE_AMD64:
		o.info.Arch = "amd64"
	case pe.IMAGE_FILE_MACHINE_ARM64:
		o.info.Arch = "arm64"
	case pe.IMAGE_FILE_MACHINE_I386:
		o.info.Arch = "386"
		o.info.Bits = 32
	default:
		o.info.Arch = fmt.Sprintf("machine-%x", f.machine)
		o.info.Bits = 32
		o.unsupported("32-bit COFF relocation")
	}
	if f.kind != "object" {
		o.info.Format = "PE"
	}
	count := len(f.records) / f.entrySize
	o.symbols = make([]symbol, count)
	comdats := make(map[int]sectionGroup)
	for i := 0; i < count; {
		s := f.symbol(i)
		if i+int(s.aux) >= count {
			return nil, fmt.Errorf("invalid COFF auxiliary symbol count")
		}
		n, e := (&pe.COFFSymbol{Name: s.name}).FullName(f.strings)
		if e != nil {
			return nil, e
		}
		if o.info.Arch == "386" {
			// Normalize the x86 C linker underscore, retaining the import
			// marker and any stdcall suffix. Calling conventions stay explicit.
			if strings.HasPrefix(n, "__imp__") {
				n = "__imp_" + coffname.NormalizeI386(strings.TrimPrefix(n, "__imp_"))
			} else if !strings.HasPrefix(n, "__imp_@") && !strings.HasPrefix(n, "__imp_?") {
				// Fastcall and MSVC C++ IAT names have no extra C underscore.
				n = coffname.NormalizeI386(n)
			}
		}
		v := symbol{name: n, section: int(s.section), value: uint64(s.value), global: s.class == 2 || s.class == 105, weak: s.class == 105}
		if o.info.Kind == "object" && (s.section < -2 || s.section > int32(len(f.sections))) {
			return nil, fmt.Errorf("invalid COFF symbol section number")
		}
		if s.section == -2 { // IMAGE_SYM_DEBUG is not tentative/common data.
			v.section = -3
		}
		if v.section == 0 && s.value != 0 {
			v.section = -2
			v.size = uint64(s.value)
			v.value = 0
			v.align = 16
		}
		if s.class == 105 && o.info.Kind == "object" {
			if s.section != 0 || s.value != 0 || s.aux != 1 {
				return nil, fmt.Errorf("invalid COFF weak external record")
			}
			aux := f.record(i + 1)
			v.alias = &weakAlias{target: int(le.Uint32(aux[:4])), search: le.Uint32(aux[4:8])}
			if v.alias.target < 0 || v.alias.target >= count || v.alias.target == i || v.alias.search < 1 || v.alias.search > 3 {
				return nil, fmt.Errorf("invalid COFF weak external fallback or search mode")
			}
		}
		o.symbols[i] = v
		if o.info.Kind == "object" && s.class == 3 && s.value == 0 && s.typ == 0 && s.aux != 0 && s.section > 0 {
			sectionIndex := int(s.section)
			if sectionIndex > len(f.sections) {
				return nil, fmt.Errorf("invalid COFF section definition")
			}
			if f.sections[sectionIndex-1].flags&0x1000 != 0 {
				aux := f.record(i + 1)
				if _, exists := comdats[sectionIndex]; exists {
					return nil, fmt.Errorf("duplicate COFF COMDAT section definition")
				}
				g := sectionGroup{sections: []int{sectionIndex}, selection: aux[14]}
				if aux[14] == 5 {
					g.parent = int(le.Uint16(aux[12:]))
					if f.big {
						g.parent |= int(le.Uint16(aux[16:])) << 16
					}
				}
				comdats[sectionIndex] = g
			}
		}
		for j := 1; j <= int(s.aux); j++ {
			o.symbols[i+j].section = -3
		}
		i += 1 + int(s.aux)
	}
	if o.info.Kind != "object" {
		return finish(o), nil
	}
	for _, s := range o.symbols {
		if s.alias != nil && !o.symbols[s.alias.target].global {
			return nil, fmt.Errorf("COFF weak fallback must reference an external symbol")
		}
	}
	o.sections = make([]*section, len(f.sections)+1)
	for i, s := range f.sections {
		flags := s.flags
		if s.name == ".idata" || strings.HasPrefix(s.name, ".idata$") {
			o.unsupported("COFF long import tables: " + s.name)
		}
		if s.name == ".didat" || strings.HasPrefix(s.name, ".didat$") {
			o.unsupported("COFF delay import tables: " + s.name)
		}
		if flags&0x200 != 0 || strings.HasPrefix(s.name, ".debug") || s.name == ".drectve" {
			continue
		}
		if flags&0x1000 != 0 {
			g, ok := comdats[i+1]
			if !ok {
				return nil, fmt.Errorf("missing COFF COMDAT auxiliary record")
			}
			switch g.selection {
			case 1, 2, 3, 4, 6, 7:
				// The external definition identifies the COMDAT, not the section name.
				for _, sym := range o.symbols {
					if sym.global && sym.section == i+1 && sym.value == 0 {
						g.key = sym.name
						break
					}
				}
				if g.key == "" {
					return nil, fmt.Errorf("COFF COMDAT has no external key")
				}
			case 5:
				if g.parent == i+1 || g.parent <= 0 || g.parent > len(f.sections) {
					return nil, fmt.Errorf("invalid associative COFF COMDAT parent")
				}
				if _, exists := comdats[g.parent]; !exists {
					return nil, fmt.Errorf("associative COFF parent is not COMDAT")
				}
			default:
				o.unsupported(fmt.Sprintf("COFF COMDAT selection %d in %s", g.selection, s.name))
			}
			o.groups = append(o.groups, g)
		}
		kind := lifecycleKind(0)
		if strings.HasPrefix(s.name, ".CRT$XI") {
			kind = lifecycleCInit
		} else if strings.HasPrefix(s.name, ".CRT$XC") {
			kind = lifecycleInit
		} else if strings.HasPrefix(s.name, ".CRT$XP") || strings.HasPrefix(s.name, ".CRT$XT") {
			kind = lifecycleFini
		} else if s.name == ".CRT$XDU" {
			kind = lifecycleThreadInit
		} else if strings.HasPrefix(s.name, ".CRT") {
			o.unsupported("automatic initialization/finalization: " + s.name)
		}
		align := uint64(16)
		a := (flags >> 20) & 15
		if a != 0 {
			align = uint64(1) << (a - 1)
		}
		v := &section{name: s.name, size: uint64(s.size), align: align, exec: flags&0x20000000 != 0, write: flags&0x80000000 != 0}
		if s.name == ".tls" || strings.HasPrefix(s.name, ".tls$") {
			v.nativeTLS = coffTLSSection
		} else if strings.HasPrefix(s.name, ".tls") {
			o.unsupported("nonstandard TLS section " + s.name)
		}
		v.lifecycle = kind
		if kind != 0 {
			if err := configureLifecycle(v, o.info.Bits); err != nil {
				return nil, err
			}
		}
		if err := validSection(v); err != nil {
			return nil, err
		}
		if flags&0x80 == 0 {
			v.data, err = s.contents()
			if err != nil {
				return nil, err
			}
		}
		o.sections[i+1] = v
		for _, r := range s.relocs {
			o.relocs = append(o.relocs, relocation{section: i + 1, offset: uint64(r.VirtualAddress), typ: uint32(r.Type), symbol: int(r.SymbolTableIndex), pair: -1})
		}
	}
	if imported, ok := newCOFFImportGraph([]*object{o}).convert(o); ok {
		o = imported
	}
	return finish(o), nil
}
