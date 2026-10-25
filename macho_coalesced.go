package dylib

import (
	"debug/macho"
	"fmt"
	"math"
	"sort"
)

// Original section offsets remain stable. A partially discarded section keeps
// dead bytes as padding, but none of their fixups or local definitions survive.
type machoCoalescedAtom struct {
	section, key int
	start, end   uint64
}

func configureMachOCoalescing(o *object, f *macho.File) error {
	var atoms []machoCoalescedAtom
	bySection := make(map[int][]int)
	if f.Symtab != nil {
		for i, s := range o.symbols[:len(f.Symtab.Syms)] {
			if s.section > 0 {
				bySection[s.section] = append(bySection[s.section], i)
			}
		}
	}
	for index, input := range f.Sections {
		if input.Flags&0xff != 0xb || input.Name == "__eh_frame" || input.Size == 0 || o.sections[index+1] == nil {
			continue
		}
		section := index + 1
		if input.Addr > math.MaxInt64-input.Size {
			return fmt.Errorf("%s original address is too large", input.Name)
		}
		byOffset := make(map[uint64][]int)
		starts := []uint64{0, input.Size}
		if f.Symtab == nil {
			return fmt.Errorf("%s has no symbol boundaries", input.Name)
		}
		for _, i := range bySection[section] {
			s := o.symbols[i]
			if s.value > input.Size {
				return fmt.Errorf("%s symbol outside section", input.Name)
			}
			if s.global && s.weak && s.value == input.Size {
				return fmt.Errorf("%s is a zero-size coalesced weak definition", s.name)
			}
			if f.Symtab.Syms[i].Desc&0x200 != 0 { // N_ALT_ENTRY shares the preceding atom.
				if s.global {
					return fmt.Errorf("%s is a public alternate entry", s.name)
				}
				continue
			}
			if f.Flags&macho.FlagSubsectionsViaSymbols == 0 && s.value != 0 {
				return fmt.Errorf("%s needs subsections_via_symbols for multiple boundaries", input.Name)
			}
			byOffset[s.value] = append(byOffset[s.value], i)
			starts = append(starts, s.value)
		}
		sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
		for i, start := range starts {
			if start == input.Size || i+1 == len(starts) || starts[i+1] == start {
				continue
			}
			for _, key := range byOffset[start] {
				s := o.symbols[key]
				if !s.global || !s.weak {
					continue
				}
				globals := 0
				for _, i := range byOffset[start] {
					if o.symbols[i].global {
						globals++
					}
				}
				// LLVM ARM64 emits local ltmp aliases at section starts. They
				// share this atom; only competing public identities are ambiguous.
				if s.name == "" || f.Symtab.Syms[key].Desc&0x80 == 0 || globals != 1 {
					return fmt.Errorf("%s needs one named public weak definition", s.name)
				}
				atom := machoCoalescedAtom{section: section, key: key, start: start, end: starts[i+1]}
				atoms = append(atoms, atom)
			}
		}
	}
	if len(atoms) == 0 {
		return nil
	}
	for i, s := range o.symbols[:len(f.Symtab.Syms)] {
		if atom := machoAtomAt(atoms, s.section, s.value); atom != nil && i != atom.key && s.global {
			return fmt.Errorf("%s has a public alternate entry", o.symbols[atom.key].name)
		}
	}
	// Normalize references before any discard decisions. This also preserves
	// local pointers to a winning definition in another object, not dead bytes.
	relocs := append([]relocation(nil), o.relocs...)
	for i, r := range relocs {
		if atom := machoAtomAt(atoms, r.section, r.offset); atom != nil && (r.width <= 0 || uint64(r.width) > atom.end-r.offset) {
			return fmt.Errorf("relocation crosses a coalesced atom boundary")
		}
		next := sort.Search(len(atoms), func(i int) bool {
			return atoms[i].section > r.section || atoms[i].section == r.section && atoms[i].start > r.offset
		})
		if next < len(atoms) && atoms[next].section == r.section && r.width > 0 && atoms[next].start-r.offset < uint64(r.width) {
			return fmt.Errorf("relocation overlaps a coalesced atom boundary")
		}
		if r.local {
			var err error
			r, err = normalizeMachOLocalAtom(o, r, atoms)
			if err != nil {
				return err
			}
		} else {
			for _, operand := range []struct {
				index *int
				sign  int64
			}{{&r.symbol, 1}, {&r.pair, 1}} {
				if *operand.index < 0 || *operand.index >= len(o.symbols) {
					continue // Ordinary relocation validation diagnoses bad indices.
				}
				s := o.symbols[*operand.index]
				atom := machoAtomAt(atoms, s.section, s.value)
				if atom == nil && s.section > 0 && s.section < len(o.sections) && o.sections[s.section] != nil && s.value == o.sections[s.section].size && s.value != 0 && !s.global && *operand.index < len(f.Symtab.Syms) && f.Symtab.Syms[*operand.index].Desc&0x200 != 0 {
					atom = machoAtomAt(atoms, s.section, s.value-1) // A named alternate may be one-past.
				}
				if atom == nil || *operand.index == atom.key {
					continue
				}
				if operand.index == &r.symbol && r.pair < 0 && machoAtomAddressRelocation(o, r) {
					r.targetOffset += s.value - atom.start
					*operand.index = atom.key
					continue
				}
				if s.value != atom.start && !machoAtomOffsetRelocation(o, r) {
					return fmt.Errorf("unsupported alternate-entry relocation %d", r.typ)
				}
				if operand.index == &r.symbol && r.pair >= 0 {
					operand.sign = -1
				}
				r.addend += operand.sign * int64(s.value-atom.start)
				*operand.index = atom.key
			}
		}
		relocs[i] = r
	}
	o.machoAtoms, o.relocs = atoms, relocs
	return nil
}

func machoAtomAt(atoms []machoCoalescedAtom, section int, offset uint64) *machoCoalescedAtom {
	i := sort.Search(len(atoms), func(i int) bool {
		return atoms[i].section > section || atoms[i].section == section && atoms[i].start > offset
	}) - 1
	if i >= 0 && atoms[i].section == section && offset < atoms[i].end {
		return &atoms[i]
	}
	return nil
}

func machoAtomOffsetRelocation(o *object, r relocation) bool {
	if r.pair >= 0 || r.typ == 0 {
		return true
	}
	if o.info.Arch == "amd64" {
		return r.typ == 1 || r.typ == 2 || r.typ >= 6 && r.typ <= 8
	}
	return o.info.Arch == "arm64" && r.typ >= 2 && r.typ <= 4
}

// These fixups first take the address of a symbol's GOT cell or regenerate an
// indirect entry. An interior atom offset belongs to the cell's target, not to
// the relocation field or the address of the cell itself.
func machoAtomAddressRelocation(o *object, r relocation) bool {
	if r.typ == machoPointerFixup || r.typ == machoStubFixup {
		return true
	}
	if o.info.Arch == "amd64" {
		return r.typ == 3 || r.typ == 4
	}
	return o.info.Arch == "arm64" && r.typ >= 5 && r.typ <= 7
}

func normalizeMachOLocalAtom(o *object, r relocation, atoms []machoCoalescedAtom) (relocation, error) {
	first := sort.Search(len(atoms), func(i int) bool { return atoms[i].section >= r.symbol })
	if first == len(atoms) || atoms[first].section != r.symbol {
		return r, nil
	}
	if r.section <= 0 || r.section >= len(o.sections) || o.sections[r.section] == nil || r.width != 4 && r.width != 8 {
		return r, fmt.Errorf("invalid local coalesced relocation")
	}
	source, target := o.sections[r.section], o.sections[r.symbol]
	if r.offset > uint64(len(source.data)) || uint64(r.width) > uint64(len(source.data))-r.offset {
		return r, fmt.Errorf("unbacked local coalesced relocation")
	}
	b := source.data[r.offset : r.offset+uint64(r.width)]
	var address, adjustment int64
	if r.typ == 0 && !r.pcrel && r.pair < 0 {
		address = int64(le.Uint32(b))
		if r.width == 8 {
			address = int64(le.Uint64(b))
		}
		adjustment = -int64(target.original)
	} else if o.info.Arch == "amd64" && r.width == 4 && r.pcrel && r.pair < 0 && machoAtomOffsetRelocation(o, r) {
		if source.original > math.MaxInt64-r.offset-4 || source.original+r.offset+4 > math.MaxInt64-math.MaxInt32-4 {
			return r, fmt.Errorf("local coalesced source address is too large")
		}
		address = int64(int32(le.Uint32(b))) + int64(source.original+r.offset+4)
		adjustment = int64(source.original+r.offset+4) - int64(target.original)
		if r.typ >= 6 && r.typ <= 8 {
			address += int64(1) << (r.typ - 6) // SIGNED_1/2/4 trailing immediate bytes.
		}
	} else {
		return r, fmt.Errorf("unsupported local coalesced relocation %d", r.typ)
	}
	if r.addend > 0 && address > math.MaxInt64-r.addend || r.addend < 0 && address < math.MinInt64-r.addend {
		return r, fmt.Errorf("local coalesced target address overflow")
	}
	address += r.addend
	if address < int64(target.original) || uint64(address)-target.original > target.size {
		return r, fmt.Errorf("local coalesced target outside section")
	}
	offset := uint64(address) - target.original
	atom := machoAtomAt(atoms, r.symbol, offset)
	if atom == nil && offset == target.size && offset != 0 {
		atom = machoAtomAt(atoms, r.symbol, offset-1) // One past the final atom.
	}
	if atom != nil {
		r.local, r.symbol = false, atom.key
		r.addend += adjustment - int64(atom.start)
	}
	return r, nil
}

func coalesceMachOAtoms(objects []*object) error {
	found := false
	for _, o := range objects {
		found = found || len(o.machoAtoms) != 0
	}
	if !found {
		return nil
	}
	defs, err := definitions(objects)
	if err != nil {
		return err
	}
	for _, o := range objects {
		var losing []machoCoalescedAtom
		unwind := false
		for _, section := range o.sections {
			unwind = unwind || section != nil && (section.name == "__eh_frame" || section.name == "__compact_unwind")
		}
		for _, atom := range o.machoAtoms {
			if o.sections[atom.section] == nil || o.symbols[atom.key].section <= 0 {
				continue // Default Load may have removed unwind-only sections.
			}
			if winner := defs[o.symbols[atom.key].name]; winner != (definition{o, atom.key}) {
				losing = append(losing, atom)
				if o.sections[atom.section].exec && unwind {
					return fmt.Errorf("coalesced code discard with retained unwind metadata is unsupported")
				}
			}
		}
		if len(losing) == 0 {
			continue
		}
		for i, s := range o.symbols {
			if machoAtomAt(losing, s.section, s.value) != nil {
				o.symbols[i].section = -3
				if s.global {
					o.symbols[i].section, o.symbols[i].value = 0, 0
				}
			}
		}
		relocs := make([]relocation, 0, len(o.relocs))
		for _, r := range o.relocs {
			if machoAtomAt(losing, r.section, r.offset) == nil {
				relocs = append(relocs, r)
			}
		}
		o.relocs = relocs
		// Drop a whole section only when weak atoms cover all its bytes and
		// no independent end symbol requires its original storage.
		ends := make(map[int]uint64)
		for _, atom := range losing {
			if atom.start == ends[atom.section] {
				ends[atom.section] = atom.end
			}
		}
		live := make(map[int]bool)
		for _, s := range o.symbols {
			if s.section > 0 {
				live[s.section] = true
			}
		}
		for section, end := range ends {
			if end != o.sections[section].size {
				continue
			}
			if !live[section] {
				o.sections[section] = nil
			}
		}
	}
	return nil
}
