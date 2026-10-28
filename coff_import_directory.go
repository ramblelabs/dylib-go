package dylib

import (
	"bytes"
	"sort"
	"strings"
)

// A self-contained IMAGE_IMPORT_DESCRIPTOR directory identifies each DLL's
// terminated IAT/ILT explicitly. Repeated metadata sections retain their section
// identity: table offsets or section names alone never associate two DLLs.
// Cached bound addresses are discarded in favor of a complete ILT. The decoder
// never executes cached IAT values or implements the PE loader's delay state.
type coffImportDirectory struct {
	graph    coffImportGraph
	o        *object
	indexes  map[string]int
	template map[int]bool
	sites    map[coffImportSite]relocation
	used     map[coffImportSite]bool
	occupied map[coffImportSite]bool
	rows     map[coffImportSite]*coffDirectoryEntry
	entries  []*coffDirectoryEntry
	width    uint64
	budget   int
}

type coffDirectoryEntry struct {
	imported coffImport
	info     ImportInfo
	symbols  []symbol
	iat      int
	thunk    *uint64
}

func (g coffImportGraph) convertDirectory(o *object) (*object, bool) {
	d := coffImportDirectory{graph: g, o: o, width: uint64(o.info.Bits / 8), budget: maxFile,
		indexes: make(map[string]int), template: make(map[int]bool), sites: make(map[coffImportSite]relocation),
		used: make(map[coffImportSite]bool), occupied: make(map[coffImportSite]bool), rows: make(map[coffImportSite]*coffDirectoryEntry)}
	if o.info.Format != "COFF" || d.width != 4 && d.width != 8 {
		return nil, false
	}
	for i, sec := range o.sections {
		if sec == nil {
			continue
		}
		switch sec.name {
		case ".text", ".idata$2", ".idata$4", ".idata$5", ".idata$6", ".idata$7":
			if (sec.name == ".text" || sec.name == ".idata$2") && d.indexes[sec.name] != 0 {
				return nil, false
			}
			if uint64(len(sec.data)) != sec.size || sec.lifecycle != 0 || sec.nativeTLS != 0 || sec.exec && sec.name != ".text" {
				return nil, false
			}
			if d.indexes[sec.name] == 0 {
				d.indexes[sec.name] = i
			}
			d.template[i] = true
		default:
			if strings.HasPrefix(sec.name, ".idata") {
				return nil, false
			}
		}
	}
	for _, name := range []string{".idata$2", ".idata$5"} {
		if d.indexes[name] == 0 {
			return nil, false
		}
	}
	retained := func(index int) bool {
		return index > 0 && index < len(o.sections) && o.sections[index] != nil && !d.template[index]
	}
	for _, group := range o.groups {
		if len(group.sections) != 1 || !retained(group.sections[0]) || group.selection == 5 && !retained(group.parent) {
			return nil, false
		}
	}
	for _, r := range o.relocs {
		if !d.template[r.section] {
			continue
		}
		site := coffImportSite{r.section, r.offset}
		if _, exists := d.sites[site]; exists {
			return nil, false
		}
		d.sites[site] = r
	}
	if !d.directory() || !d.symbols() || !d.thunks() {
		return nil, false
	}
	for site := range d.sites {
		if !d.used[site] {
			return nil, false
		}
	}
	for index := range d.template {
		if !d.section(index, ".idata$4") && !d.section(index, ".idata$5") {
			continue
		}
		sec := o.sections[index]
		if sec.size%d.width != 0 {
			return nil, false
		}
		for offset := uint64(0); offset < sec.size; offset += d.width {
			if !d.occupied[coffImportSite{index, offset}] && !allZero(sec.data[offset:offset+d.width]) {
				return nil, false
			}
		}
	}
	converted := &object{info: o.info}
	converted.info.Imports, converted.info.Unsupported = nil, nil
	for _, row := range d.entries {
		converted.symbols = append(converted.symbols, row.symbols...)
		converted.info.Imports = append(converted.info.Imports, row.info)
	}
	for _, unsupported := range o.info.Unsupported {
		if !strings.HasPrefix(unsupported, "COFF long import tables:") {
			converted.info.Unsupported = append(converted.info.Unsupported, unsupported)
		}
	}
	return overlayCOFFImports(o, d.template, converted)
}

func (d *coffImportDirectory) section(index int, name string) bool {
	return d.template[index] && d.o.sections[index].name == name
}

func (d *coffImportDirectory) reference(section int, offset uint64) (coffLocation, bool) {
	site := coffImportSite{section, offset}
	r, exists := d.sites[site]
	rva := map[string]uint32{"amd64": 3, "arm64": 2, "386": 7}[d.o.info.Arch]
	if !exists || rva == 0 || r.typ != rva || r.pair >= 0 || r.local || r.addend != 0 || r.symbol < 0 || r.symbol >= len(d.o.symbols) {
		return coffLocation{}, false
	}
	s := d.o.symbols[r.symbol]
	if s.section <= 0 || !d.template[s.section] {
		return coffLocation{}, false
	}
	data := d.o.sections[section].data
	if offset > uint64(len(data)) || uint64(len(data))-offset < 4 {
		return coffLocation{}, false
	}
	addend, size := uint64(le.Uint32(data[offset:])), d.o.sections[s.section].size
	if addend > size || s.value > size-addend {
		return coffLocation{}, false
	}
	d.used[site] = true
	return coffLocation{d.o, s.section, s.value + addend}, true
}

func (d *coffImportDirectory) directory() bool {
	index := d.indexes[".idata$2"]
	sec := d.o.sections[index]
	if sec.size < 40 || sec.size%20 != 0 || sec.size/20 > 65537 {
		return false
	}
	for offset := uint64(0); offset < sec.size; offset += 20 {
		bytes := sec.data[offset : offset+20]
		_, lookupRelocation := d.sites[coffImportSite{index, offset}]
		_, nameRelocation := d.sites[coffImportSite{index, offset + 12}]
		_, addressRelocation := d.sites[coffImportSite{index, offset + 16}]
		if allZero(bytes) && !lookupRelocation && !nameRelocation && !addressRelocation {
			return len(d.entries) != 0 && allZero(sec.data[offset:])
		}
		bound := !allZero(bytes[4:8])
		if !bound && !allZero(bytes[8:12]) {
			return false
		}
		iat, yes := d.reference(index, offset+16)
		name, named := d.reference(index, offset+12)
		if !yes || !named || !d.section(iat.section, ".idata$5") {
			return false
		}
		// An unbound IAT contains the lookup entries when OriginalFirstThunk
		// is absent. A nonzero field or any relocation must still identify an
		// actual ILT; a malformed lookup reference must never become a fallback.
		ilt := iat
		if bound && !lookupRelocation {
			return false // A cached address is never an import lookup entry.
		}
		if lookupRelocation || !allZero(bytes[:4]) {
			var ok bool
			ilt, ok = d.reference(index, offset)
			if !ok || !d.section(ilt.section, ".idata$4") {
				return false
			}
		}
		nativeName := d.o.sections[name.section].name
		if nativeName != ".idata$6" && nativeName != ".idata$7" {
			return false
		}
		dll, ok := name.name(0)
		if !ok {
			return false
		}
		dll, err := coffDLLName(dll)
		if err != nil || !d.table(ilt, iat, dll, bound) {
			return false
		}
	}
	return false // An explicit null descriptor terminates the bounded directory.
}

func (d *coffImportDirectory) table(ilt, iat coffLocation, dll string, bound bool) bool {
	if ilt.offset%d.width != 0 || iat.offset%d.width != 0 {
		return false
	}
	count := 0
	for ; count <= len(d.o.symbols); count++ {
		if ilt.offset > d.o.sections[ilt.section].size || d.width > d.o.sections[ilt.section].size-ilt.offset ||
			iat.offset > d.o.sections[iat.section].size || d.width > d.o.sections[iat.section].size-iat.offset {
			return false
		}
		x, y := coffImportSite{ilt.section, ilt.offset}, coffImportSite{iat.section, iat.offset}
		if d.occupied[x] || d.occupied[y] {
			return false
		}
		d.occupied[x], d.occupied[y] = true, true
		left := d.o.sections[ilt.section].data[ilt.offset : ilt.offset+d.width]
		right := d.o.sections[iat.section].data[iat.offset : iat.offset+d.width]
		_, a := d.sites[x]
		_, b := d.sites[y]
		if allZero(left) && !a {
			return count != 0 && allZero(right) && !b
		}
		if bound {
			// A complete ILT supplies the identity. Bound timestamps,
			// forwarder hints and native addresses are stale cache metadata;
			// resolution below creates fresh session-owned import slots.
			if b || allZero(right) {
				return false
			}
		} else if !bytes.Equal(left, right) {
			return false
		}
		if len(d.entries) >= len(d.o.symbols) || len(d.entries) >= 65536 {
			return false
		}
		entry := &coffDirectoryEntry{iat: iat.section, imported: coffImport{dll: dll}, info: ImportInfo{DLL: dll, Kind: "data"}}
		word := uint64(le.Uint32(left))
		if d.width == 8 {
			word = le.Uint64(left)
		}
		flag := uint64(1) << (8*d.width - 1)
		if word&flag != 0 {
			if a || b || word & ^(flag|0xffff) != 0 || uint16(word) == 0 {
				return false
			}
			entry.imported.ordinal, entry.info.Ordinal = uint16(word), uint16(word)
		} else {
			if word>>32 != 0 {
				return false
			}
			first, ok := d.reference(ilt.section, ilt.offset)
			second, yes := first, ok
			if !bound && x != y {
				second, yes = d.reference(iat.section, iat.offset)
			}
			if !ok || !yes || first != second || !d.section(first.section, ".idata$6") {
				return false
			}
			name, ok := first.name(2)
			if !ok {
				return false
			}
			entry.imported.name, entry.info.Name = name, name
		}
		cost := len(dll) + len(entry.imported.name)
		if cost > d.budget {
			return false
		}
		d.budget -= cost
		d.rows[y] = entry
		d.entries = append(d.entries, entry)
		ilt.offset += d.width
		iat.offset += d.width
	}
	return false
}

func (d *coffImportDirectory) symbols() bool {
	names := make(map[string]bool)
	for _, s := range d.o.symbols {
		if !s.global || !d.template[s.section] {
			continue
		}
		if s.alias != nil || s.name == "" || names[s.name] {
			return false
		}
		names[s.name] = true
		if d.section(s.section, ".idata$5") {
			row := d.rows[coffImportSite{s.section, s.value}]
			if row == nil {
				return false
			}
			if strings.HasPrefix(s.name, "__imp_") {
				public := strings.TrimPrefix(s.name, "__imp_")
				if public == "" || row.imported.public != "" || len(public) > d.budget {
					return false
				}
				row.imported.public, row.info.Symbol = public, public
				d.budget -= len(public)
			}
		} else if s.section != d.indexes[".text"] {
			return false
		}
	}
	public := make(map[string]*coffDirectoryEntry)
	for _, row := range d.entries {
		if row.imported.public == "" {
			return false
		}
		public[row.imported.public] = row
	}
	for _, s := range d.o.symbols {
		if !s.global || !d.template[s.section] {
			continue
		}
		row := d.rows[coffImportSite{s.section, s.value}]
		indirect := d.section(s.section, ".idata$5")
		if !indirect {
			row = public[s.name]
			if row == nil || row.thunk != nil {
				return false
			}
			start := s.value
			row.thunk, row.info.Kind = &start, "code"
		} else if s.name == row.imported.public {
			row.info.Kind = "const"
		}
		row.symbols = append(row.symbols, symbol{name: s.name, section: -4, global: true, imported: &coffImportSymbol{entry: &row.imported, indirect: indirect}})
	}
	return true
}

func (d *coffImportDirectory) thunks() bool {
	text := d.indexes[".text"]
	var rows []*coffDirectoryEntry
	for _, row := range d.entries {
		if row.thunk != nil {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return *rows[i].thunk < *rows[j].thunk })
	if len(rows) == 0 {
		return text == 0 || d.o.sections[text].size == 0
	}
	if text == 0 || *rows[0].thunk != 0 {
		return false
	}
	fixups := make([][]relocation, len(rows))
	for site, r := range d.sites {
		if site.section != text {
			continue
		}
		index := sort.Search(len(rows), func(i int) bool { return *rows[i].thunk > site.offset }) - 1
		if index < 0 || site.offset >= d.o.sections[text].size || r.symbol < 0 || r.symbol >= len(d.o.symbols) {
			return false
		}
		row := rows[index]
		s := d.o.symbols[r.symbol]
		if s.section != row.iat || d.rows[coffImportSite{s.section, s.value}] != row {
			return false
		}
		r.section, r.offset, r.symbol = 1, r.offset-*row.thunk, 0
		fixups[index] = append(fixups[index], r)
		d.used[site] = true
	}
	for i, row := range rows {
		start, end := *row.thunk, d.o.sections[text].size
		if i+1 < len(rows) {
			end = *rows[i+1].thunk
		}
		if end <= start || end > d.o.sections[text].size {
			return false
		}
		mini := &object{info: d.o.info, sections: []*section{nil, coffImportSection(d.o.sections[text], start, end-start), d.o.sections[row.iat]}, symbols: []symbol{{section: 2}}}
		mini.relocs = fixups[i]
		if !d.graph.validLongTables(mini, map[string]int{".text": 1, ".idata$5": 2}, &row.imported) {
			return false
		}
	}
	return true
}
