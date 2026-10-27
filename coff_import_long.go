package dylib

import (
	"bytes"
	"strings"
)

// Long GNU import libraries split an import into an IAT/thunk member, a
// descriptor member, and a DLL-name member. Follow their RVA relocations,
// without laying out or executing the native import tables. Verified templates
// may coexist with ordinary sections; other .idata objects remain unsupported.
type coffImportGraph struct {
	defs map[string][]definition
	dlls map[coffLocation]string // Per-conversion cache for combined import members.
}

type coffLocation struct {
	o       *object
	section int
	offset  uint64
}

func newCOFFImportGraph(objects []*object) coffImportGraph {
	g := coffImportGraph{defs: make(map[string][]definition)}
	for _, o := range objects {
		if o.info.Format != "COFF" {
			continue
		}
		for i, s := range o.symbols {
			if s.global && s.section > 0 {
				key := o.info.Arch + "\x00" + s.name
				g.defs[key] = append(g.defs[key], definition{o, i})
			}
		}
	}
	return g
}

func (g coffImportGraph) reference(o *object, section int, offset uint64) (coffLocation, bool) {
	var result coffLocation
	found := false
	rva := map[string]uint32{"amd64": 3, "arm64": 2, "386": 7}[o.info.Arch]
	for _, r := range o.relocs {
		if r.section != section || r.offset != offset {
			continue
		}
		if found || rva == 0 || r.typ != rva || r.symbol < 0 || r.symbol >= len(o.symbols) {
			return result, false
		}
		s := o.symbols[r.symbol]
		provider := o
		if s.section == 0 {
			defs := g.defs[o.info.Arch+"\x00"+s.name]
			if len(defs) != 1 {
				return result, false
			}
			provider, s = defs[0].o, defs[0].sym()
		}
		if section <= 0 || section >= len(o.sections) || o.sections[section] == nil || s.section <= 0 || s.section >= len(provider.sections) || provider.sections[s.section] == nil {
			return result, false
		}
		data := o.sections[section].data
		if offset > uint64(len(data)) || uint64(len(data))-offset < 4 {
			return result, false
		}
		result = coffLocation{provider, s.section, s.value + uint64(le.Uint32(data[offset:]))}
		if result.offset > uint64(len(provider.sections[s.section].data)) {
			return result, false
		}
		found = true
	}
	return result, found
}

func (p coffLocation) name(skip uint64) (string, bool) {
	data := p.o.sections[p.section].data
	if p.offset > uint64(len(data)) || skip > uint64(len(data))-p.offset {
		return "", false
	}
	data = data[p.offset+skip:]
	end := bytes.IndexByte(data, 0)
	if end <= 0 {
		return "", false
	}
	return string(data[:end]), true
}

func (g coffImportGraph) convert(o *object) (*object, bool) {
	if o.info.Format != "COFF" {
		return nil, false
	}
	candidate := false
	for _, s := range o.symbols {
		if s.global && s.section > 0 && s.section < len(o.sections) && o.sections[s.section] != nil && o.sections[s.section].name == ".idata$5" && strings.HasPrefix(s.name, "__imp_") {
			candidate = true
			break
		}
	}
	if !candidate {
		return nil, false
	}

	if converted, ok := g.convertPure(o); ok {
		return converted, true
	}
	if converted, ok := g.convertMixed(o); ok {
		return converted, true
	}
	return g.convertDirectory(o)
}

func coffImportSections(o *object) (map[string]int, bool) {
	if len(o.groups) != 0 {
		return nil, false
	}
	indexes := make(map[string]int)
	for i, sec := range o.sections {
		if sec == nil {
			continue
		}
		if _, exists := indexes[sec.name]; exists {
			return nil, false
		}
		indexes[sec.name] = i
		switch sec.name {
		case ".text", ".idata$4", ".idata$5", ".idata$6", ".idata$7":
		default:
			if sec.size != 0 {
				return nil, false
			}
		}
	}
	return indexes, true
}

func (g coffImportGraph) convertPure(o *object) (*object, bool) {
	indexes, ok := coffImportSections(o)
	width := uint64(o.info.Bits / 8)
	if !ok || indexes[".idata$5"] == 0 || (width != 4 && width != 8) {
		return nil, false
	}
	if o.sections[indexes[".idata$5"]].size > width {
		return g.convertMultiple(o, indexes)
	}
	return g.convertSingle(o, indexes)
}

func (g coffImportGraph) convertSingle(o *object, indexes map[string]int) (*object, bool) {
	iat, force := indexes[".idata$5"], indexes[".idata$7"]
	width := uint64(o.info.Bits / 8)
	if iat == 0 || force == 0 || (width != 4 && width != 8) || o.sections[iat].size != width || len(o.sections[iat].data) != int(width) || o.sections[force].size != 4 {
		return nil, false
	}
	public := ""
	for _, s := range o.symbols {
		if s.global && s.section == iat && s.value == 0 && strings.HasPrefix(s.name, "__imp_") {
			if public != "" {
				return nil, false
			}
			public = strings.TrimPrefix(s.name, "__imp_")
		}
	}
	if public == "" {
		return nil, false
	}
	dll, ok := g.longImportDLL(o, force)
	if !ok {
		return nil, false
	}
	entry := &coffImport{dll: dll, public: public}
	word := uint64(le.Uint32(o.sections[iat].data))
	if width == 8 {
		word = le.Uint64(o.sections[iat].data)
	}
	ordinalFlag := uint64(1) << (width*8 - 1)
	if word&ordinalFlag != 0 {
		if word & ^(ordinalFlag|0xffff) != 0 || uint16(word) == 0 {
			return nil, false
		}
		entry.ordinal = uint16(word)
	} else {
		if width == 8 && word>>32 != 0 {
			return nil, false
		}
		name, ok := g.reference(o, iat, 0)
		if !ok || name.o != o || o.sections[name.section].name != ".idata$6" {
			return nil, false
		}
		entry.name, ok = name.name(2) // Hint is advisory; use the export name.
		if !ok {
			return nil, false
		}
	}
	if !g.validLongTables(o, indexes, entry) {
		return nil, false
	}
	copy := *o
	copy.sections, copy.relocs, copy.groups = nil, nil, nil
	copy.symbols = nil
	kind := "data"
	for _, s := range o.symbols {
		if s.global && (s.alias != nil || s.section == -1 || s.section == -2) {
			return nil, false
		}
		if !s.global || s.section <= 0 {
			continue
		}
		if s.value != 0 || (s.section != iat && (s.section != indexes[".text"] || s.name != public)) {
			return nil, false
		}
		indirect := s.section == iat
		if !indirect {
			kind = "code"
		} else if s.name == public {
			kind = "const"
		}
		copy.symbols = append(copy.symbols, symbol{name: s.name, section: -4, global: true, imported: &coffImportSymbol{entry: entry, indirect: indirect}})
	}
	copy.info.Symbols, copy.info.Unsupported = nil, nil
	for _, unsupported := range o.info.Unsupported {
		if !strings.HasPrefix(unsupported, "COFF long import tables:") {
			copy.info.Unsupported = append(copy.info.Unsupported, unsupported)
		}
	}
	copy.info.Imports = []ImportInfo{{DLL: dll, Symbol: public, Name: entry.name, Ordinal: entry.ordinal, Kind: kind}}
	return &copy, true
}

func (g coffImportGraph) longImportDLL(o *object, force int) (string, bool) {
	descriptor, ok := g.reference(o, force, 0)
	if !ok || descriptor.o.sections[descriptor.section].name != ".idata$2" || descriptor.offset != 0 || descriptor.o.sections[descriptor.section].size != 20 {
		return "", false
	}
	if dll, exists := g.dlls[descriptor]; exists {
		return dll, true
	}
	if !validCOFFImportHelper(descriptor.o) {
		return "", false
	}
	dllName, ok := g.reference(descriptor.o, descriptor.section, 12)
	if !ok || !validCOFFImportHelper(dllName.o) || dllName.offset != 0 {
		return "", false
	}
	dll, ok := dllName.name(0)
	if !ok {
		return "", false
	}
	dll, err := coffDLLName(dll)
	if err != nil {
		return "", false
	}
	if g.dlls != nil {
		g.dlls[descriptor] = dll
	}
	return dll, true
}

// A descriptor/name member would normally be extracted with its import.
// Do not bypass extra initialization or code by treating it as mere metadata.
func validCOFFImportHelper(o *object) bool {
	if len(o.groups) != 0 {
		return false
	}
	for _, unsupported := range o.info.Unsupported {
		if !strings.HasPrefix(unsupported, "COFF long import tables:") {
			return false
		}
	}
	for _, sec := range o.sections {
		if sec == nil || sec.size == 0 {
			continue
		}
		if sec.exec || sec.lifecycle != 0 {
			return false
		}
		switch sec.name {
		case ".idata$2":
			if sec.size != 20 || len(sec.data) != 20 || !allZero(sec.data[4:12]) {
				return false
			}
		case ".idata$4", ".idata$5":
			if sec.size != uint64(o.info.Bits/8) || !allZero(sec.data) {
				return false
			}
		case ".idata$6", ".idata$7":
			end := bytes.IndexByte(sec.data, 0)
			if end <= 0 || !allZero(sec.data[end:]) {
				return false
			}
		default:
			return false
		}
	}
	rva := map[string]uint32{"amd64": 3, "arm64": 2, "386": 7}[o.info.Arch]
	seen := make(map[uint64]bool)
	for _, r := range o.relocs {
		if r.section <= 0 || r.section >= len(o.sections) || o.sections[r.section] == nil || o.sections[r.section].name != ".idata$2" || (r.offset != 0 && r.offset != 12 && r.offset != 16) || r.typ != rva || r.symbol < 0 || r.symbol >= len(o.symbols) || seen[r.offset] {
			return false
		}
		seen[r.offset] = true
	}
	return true
}

func allZero(data []byte) bool {
	for _, v := range data {
		if v != 0 {
			return false
		}
	}
	return true
}

// Require every relocation to belong to the known import template. This
// prevents normalization from hiding executable code or unrelated references.
func (g coffImportGraph) validLongTables(o *object, indexes map[string]int, entry *coffImport) bool {
	iat, text, force := indexes[".idata$5"], indexes[".text"], indexes[".idata$7"]
	if ilt := indexes[".idata$4"]; ilt != 0 {
		if o.sections[ilt].size != o.sections[iat].size || !bytes.Equal(o.sections[ilt].data, o.sections[iat].data) {
			return false
		}
		if entry.ordinal == 0 {
			x, ok := g.reference(o, ilt, 0)
			y, yes := g.reference(o, iat, 0)
			if !ok || !yes || x != y {
				return false
			}
		}
	}
	textRelocs := 0
	var textSites uint8
	for _, r := range o.relocs {
		switch r.section {
		case iat, indexes[".idata$4"]:
			if r.offset != 0 || entry.ordinal != 0 {
				return false
			}
			p, ok := g.reference(o, r.section, 0)
			if !ok || p.o != o || p.section != indexes[".idata$6"] {
				return false
			}
		case force:
			if r.offset != 0 {
				return false
			}
		case text:
			if r.symbol < 0 || r.symbol >= len(o.symbols) {
				return false
			}
			s := o.symbols[r.symbol]
			if s.section != iat || s.value != 0 {
				return false
			}
			if o.info.Arch == "arm64" {
				if (r.offset != 0 || r.typ != 4) && (r.offset != 4 || r.typ != 6) {
					return false
				}
			} else if r.offset != 2 || (o.info.Arch == "amd64" && r.typ != 4) || (o.info.Arch == "386" && r.typ != 6) {
				return false
			}
			site := uint8(1) << r.offset // Validated thunk offsets are 0, 2 or 4.
			if textSites&site != 0 {
				return false
			}
			textSites |= site
			textRelocs++
		default:
			return false
		}
	}
	if text == 0 || o.sections[text].size == 0 {
		return textRelocs == 0
	}
	data := o.sections[text].data
	if o.info.Arch == "arm64" {
		return textRelocs == 2 && bytes.Equal(data, []byte{0x10, 0, 0, 0x90, 0x10, 2, 0, 0x91, 0x10, 2, 0x40, 0xf9, 0, 2, 0x1f, 0xd6})
	}
	return textRelocs == 1 && (bytes.Equal(data, []byte{0xff, 0x25, 0, 0, 0, 0}) || bytes.Equal(data, []byte{0xff, 0x25, 0, 0, 0, 0, 0x90, 0x90}))
}
