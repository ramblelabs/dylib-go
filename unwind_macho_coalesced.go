package dylib

import (
	"fmt"
	"math"
	"sort"
)

type machoFrameRecord struct {
	start, end, next uint64
	cie              uint64
	common, keep     bool
}

type machoFramePointer struct {
	offset   uint64
	encoding byte
	nullable bool
}

type machoLSDAReference struct {
	address uint64
	keep    bool
}

// Resolve only unwind fixups in Go-owned scratch memory at the input's original
// section spacing. This preserves assembler-resolved AMD64 PC/LSDA pointers
// while reusing the existing ARM64 subtractor and GOT relocation rules. Fake
// external addresses are never called or dereferenced.
func machoOriginalUnwindImage(o *object) (*image, error) {
	c := *o
	c.sections = make([]*section, len(o.sections))
	var occupied []*section
	end := uint64(0)
	for i, s := range o.sections {
		if s == nil {
			continue
		}
		if (s.name == "__eh_frame" || s.name == "__compact_unwind") && (s.exec || s.write || uint64(len(s.data)) != s.size) {
			return nil, fmt.Errorf("invalid coalesced unwind storage")
		}
		if s.original > maxImage || s.size > maxImage-s.original {
			return nil, fmt.Errorf("Mach-O unwind input address span exceeds image limit")
		}
		copy := *s
		copy.offset = s.original
		c.sections[i] = &copy
		if s.size != 0 {
			occupied = append(occupied, s)
		}
		if s.original+s.size > end {
			end = s.original + s.size
		}
	}
	sort.Slice(occupied, func(i, j int) bool { return occupied[i].original < occupied[j].original })
	for i := 1; i < len(occupied); i++ {
		if occupied[i].original < occupied[i-1].original+occupied[i-1].size {
			return nil, fmt.Errorf("overlapping original Mach-O unwind input sections")
		}
	}
	got := alignUp(end, 8)
	reserve := uint64(len(o.relocs)+1) * 8
	if got > maxImage || reserve > maxImage-got {
		return nil, fmt.Errorf("Mach-O unwind scratch storage exceeds image limit")
	}
	defs, err := definitions([]*object{&c})
	if err != nil {
		return nil, err
	}
	im := &image{base: 0x100000, pointerSize: 8, objects: []*object{&c}, defs: defs,
		mem: make([]byte, got+reserve), external: func(string) uintptr { return 0x70000000 },
		resolved: map[string]uintptr{}, got: map[uintptr]uintptr{}, stubs: map[uintptr]uintptr{},
		gotStart: got, gotNext: got, stubStart: got + reserve, stubNext: got + reserve,
	}
	for _, s := range c.sections {
		if s != nil {
			copy(im.mem[s.offset:s.offset+s.size], s.data)
		}
	}
	for _, r := range c.relocs {
		s := c.sections[r.section]
		if s != nil && (s.name == "__eh_frame" || s.name == "__compact_unwind") {
			allowed := r.typ == 0 || r.pair >= 0
			if s.name == "__eh_frame" {
				allowed = allowed || o.info.Arch == "amd64" && (r.typ == 1 || r.typ == 3 || r.typ == 4) || o.info.Arch == "arm64" && r.typ == 7
			} else {
				allowed = allowed && r.width == 8 && !r.pcrel && r.pair < 0
			}
			if !allowed {
				return nil, fmt.Errorf("unsupported coalesced unwind relocation %d", r.typ)
			}
			if err := im.relocate(&c, r); err != nil {
				return nil, err
			}
		}
	}
	return im, nil
}

func (o *object) pruneMachOUnwind(losing []machoCoalescedAtom) error {
	code := false
	for _, atom := range losing {
		code = code || o.sections[atom.section].exec
	}
	metadata := false
	for _, s := range o.sections {
		metadata = metadata || s != nil && (s.name == "__eh_frame" || s.name == "__compact_unwind")
	}
	if !code || !metadata {
		return nil
	}
	im, err := machoOriginalUnwindImage(o)
	if err != nil {
		return err
	}
	owned := func(start, length uint64) (bool, error) {
		for i, s := range o.sections {
			if s == nil || !s.exec || start < uint64(im.base)+s.original || start-uint64(im.base)-s.original >= s.size {
				continue
			}
			offset := start - uint64(im.base) - s.original
			if length == 0 || length > s.size-offset {
				return false, fmt.Errorf("coalesced unwind range is outside input code")
			}
			if atom := machoAtomAt(losing, i, offset); atom != nil {
				if offset+length > atom.end {
					return false, fmt.Errorf("unwind range crosses a coalesced atom boundary")
				}
				return true, nil
			}
			next := sort.Search(len(losing), func(n int) bool { return losing[n].section > i || losing[n].section == i && losing[n].start > offset })
			if next < len(losing) && losing[next].section == i && losing[next].start < offset+length {
				return false, fmt.Errorf("unwind range overlaps a coalesced atom boundary")
			}
			return false, nil
		}
		return false, fmt.Errorf("coalesced unwind range has no input code owner")
	}
	var lsdas []machoLSDAReference
	for index, s := range o.sections {
		if s == nil || s.name != "__eh_frame" && s.name != "__compact_unwind" {
			continue
		}
		var records []machoFrameRecord
		var pointers []machoFramePointer
		if s.name == "__compact_unwind" {
			if s.size%32 != 0 {
				return fmt.Errorf("invalid coalesced compact unwind record width")
			}
			if s.size/32 > 65536 {
				return fmt.Errorf("too many coalesced unwind records")
			}
			for offset := uint64(0); offset < s.size; offset += 32 {
				data := im.mem[s.original+offset:]
				drop, err := owned(le.Uint64(data), uint64(le.Uint32(data[8:])))
				if err != nil {
					return err
				}
				if address := le.Uint64(data[24:]); address != 0 {
					if !im.frameLSDA(address) {
						return fmt.Errorf("coalesced compact LSDA is outside exception data")
					}
					lsdas = append(lsdas, machoLSDAReference{address, !drop})
				}
				records = append(records, machoFrameRecord{start: offset, end: offset + 32, keep: !drop})
			}
		} else {
			for offset := uint64(0); offset < s.size; {
				if len(records) == 65536 {
					return fmt.Errorf("too many coalesced unwind records")
				}
				if s.size-offset < 4 {
					return fmt.Errorf("truncated coalesced frame length")
				}
				length := uint64(le.Uint32(s.data[offset:]))
				if length == 0 {
					if !allZero(s.data[offset:]) {
						return fmt.Errorf("data after coalesced frame terminator")
					}
					break
				}
				if length == 0xffffffff || length < 4 || length > s.size-offset-4 {
					return fmt.Errorf("invalid coalesced frame record length")
				}
				id := uint64(le.Uint32(s.data[offset+4:]))
				r := machoFrameRecord{start: offset, end: offset + 4 + length, common: id == 0}
				if id != 0 {
					if id > offset+4 {
						return fmt.Errorf("invalid coalesced FDE CIE reference")
					}
					r.cie = offset + 4 - id
				}
				records = append(records, r)
				offset = r.end
			}
			currentLSDA := uint64(0)
			pointer := func(nullable bool) func(uint64, byte) error {
				return func(offset uint64, encoding byte) error {
					pointers = append(pointers, machoFramePointer{offset, encoding, nullable})
					currentLSDA = 0
					if nullable {
						reader := frameReader{b: im.mem[s.original+offset : s.original+s.size]}
						var err error
						currentLSDA, err = im.framePointer(&reader, encoding, s.original+offset)
						return err
					}
					return nil
				}
			}
			byStart := make(map[uint64]int)
			for i, r := range records {
				byStart[r.start] = i
			}
			policy := framePolicy{pcRelative: true, language: true,
				fixup: pointer(false), lsdaFixup: pointer(true), personalityFixup: pointer(false),
				record: func(start, length uint64, fde uintptr, _ bool) error {
					drop, err := owned(start, length)
					if err != nil {
						return err
					}
					r := &records[byStart[uint64(fde)-uint64(im.base)-s.original]]
					r.keep = !drop
					if currentLSDA != 0 {
						lsdas = append(lsdas, machoLSDAReference{currentLSDA, !drop})
					}
					if !drop {
						records[byStart[r.cie]].keep = true
					}
					return nil
				},
			}
			if _, err := im.validateFrameRecords(im.mem[s.original:s.original+s.size], s.original, policy); err != nil {
				return fmt.Errorf("coalesced __eh_frame: %w", err)
			}
		}
		if err := o.compactMachOUnwind(index, records, pointers, losing); err != nil {
			return err
		}
	}
	return o.pruneMachOLSDA(lsdas, uint64(im.base), losing)
}

// Keep exception-data offsets stable. Only a private symbol-delimited table
// referenced exclusively by losing frames can lose its fixups and definitions.
// Shared tables and neighboring live tables keep their original bytes.
func (o *object) pruneMachOLSDA(refs []machoLSDAReference, base uint64, losing []machoCoalescedAtom) error {
	owners := make(map[uint64]bool)
	for _, ref := range refs {
		owners[ref.address] = owners[ref.address] || ref.keep
	}
	var live []uint64
	for address, keep := range owners {
		if keep {
			live = append(live, address)
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i] < live[j] })
	for index, s := range o.sections {
		if s == nil || !lsdaSection(o.info.Format, s.name) {
			continue
		}
		private := make(map[uint64]bool)
		for _, sym := range o.symbols {
			if sym.section == index && sym.value < s.size {
				private[sym.value] = private[sym.value] || !sym.global
			}
		}
		var boundaries []uint64
		for offset := range private {
			boundaries = append(boundaries, offset)
		}
		sort.Slice(boundaries, func(i, j int) bool { return boundaries[i] < boundaries[j] })
		type span struct{ start, end uint64 }
		var dead []span
		for address, keep := range owners {
			if keep || address < base+s.original || address-base-s.original >= s.size {
				continue
			}
			start, end := address-base-s.original, s.size
			i := sort.Search(len(boundaries), func(i int) bool { return boundaries[i] > start })
			if !private[start] {
				return fmt.Errorf("coalesced LSDA needs private symbol boundaries")
			}
			if i < len(boundaries) {
				end = boundaries[i]
			}
			first := sort.Search(len(live), func(i int) bool { return live[i] >= address })
			if first == len(live) || live[first] >= base+s.original+end {
				dead = append(dead, span{start, end})
			}
		}
		if len(dead) == 0 {
			continue
		}
		sort.Slice(dead, func(i, j int) bool { return dead[i].start < dead[j].start })
		contains := func(offset uint64) bool {
			i := sort.Search(len(dead), func(i int) bool { return dead[i].end > offset })
			return i < len(dead) && offset >= dead[i].start
		}
		var retained []relocation
		for _, r := range o.relocs {
			if r.section == index && contains(r.offset) || machoAtomAt(losing, r.section, r.offset) != nil {
				continue
			}
			if r.local && r.symbol == index {
				if r.typ != 0 || r.pair >= 0 || r.pcrel || r.width != 8 {
					return fmt.Errorf("unsupported local reference to coalesced LSDA")
				}
				source := o.sections[r.section]
				if r.offset > uint64(len(source.data)) || uint64(r.width) > uint64(len(source.data))-r.offset {
					return fmt.Errorf("unbacked coalesced LSDA reference")
				}
				address := le.Uint64(source.data[r.offset:]) + uint64(r.addend)
				if address >= s.original && contains(address-s.original) {
					return fmt.Errorf("live reference to discarded Mach-O LSDA")
				}
			} else if !r.local {
				for _, operand := range []int{r.symbol, r.pair} {
					if operand >= 0 && operand < len(o.symbols) {
						sym := o.symbols[operand]
						if sym.section == index && contains(sym.value) {
							return fmt.Errorf("live reference to discarded Mach-O LSDA")
						}
					}
				}
			}
			retained = append(retained, r)
		}
		data := append([]byte(nil), s.data...)
		for _, r := range dead {
			clear(data[r.start:r.end])
		}
		for i, sym := range o.symbols {
			if sym.section == index && contains(sym.value) {
				if sym.global || sym.size != 0 {
					return fmt.Errorf("cannot discard public/sized Mach-O LSDA symbols")
				}
				o.symbols[i].section = -3
			}
		}
		s.data, o.relocs = data, retained
	}
	return nil
}

func (o *object) compactMachOUnwind(index int, records []machoFrameRecord, pointers []machoFramePointer, losing []machoCoalescedAtom) error {
	s := o.sections[index]
	data := make([]byte, 0, s.size)
	for i := range records {
		r := &records[i]
		r.next = uint64(len(data))
		if r.keep {
			data = append(data, s.data[r.start:r.end]...)
		}
	}
	if uint64(len(data)) == s.size {
		return nil
	}
	remap := func(offset uint64) (uint64, bool) {
		i := sort.Search(len(records), func(i int) bool { return records[i].end > offset })
		if i < len(records) && records[i].keep && offset >= records[i].start {
			return records[i].next + offset - records[i].start, true
		}
		return 0, false
	}
	// References into shrinking metadata need their own target/addend remapping.
	// Compiler function-start/LSDA relocations target code or exception data.
	for _, r := range o.relocs {
		if r.section == index || machoAtomAt(losing, r.section, r.offset) != nil {
			continue
		}
		for _, operand := range []int{r.symbol, r.pair} {
			if r.local && operand == r.symbol && operand == index || !r.local && operand >= 0 && operand < len(o.symbols) && o.symbols[operand].section == index {
				return fmt.Errorf("cannot prune externally referenced Mach-O unwind section")
			}
		}
	}
	explicit := make(map[uint64]bool)
	anchor := len(o.symbols)
	needsAnchor := false
	var relocs []relocation
	for _, r := range o.relocs {
		if r.section == index {
			explicit[r.offset] = true
			offset, keep := remap(r.offset)
			if !keep {
				continue
			}
			if r.local && r.symbol == index {
				return fmt.Errorf("cannot prune section-local Mach-O frame references")
			}
			if !r.local && r.symbol >= 0 && r.symbol < len(o.symbols) && o.symbols[r.symbol].section == index {
				if r.pair < 0 || r.pcrel || r.width != 4 && r.width != 8 {
					return fmt.Errorf("unsupported coalesced frame anchor relocation")
				}
				// ARM64 FDE pairs subtract an input metadata anchor. Its
				// original CIE may disappear; retain an independent base and
				// account for both the old anchor and the moved pointer field.
				r.addend += int64(r.offset-offset) - int64(o.symbols[r.symbol].value)
				r.symbol, needsAnchor = anchor, true
			}
			r.offset = offset
		}
		relocs = append(relocs, r)
	}
	for _, r := range records {
		if r.keep && !r.common && s.name == "__eh_frame" {
			cie, keep := remap(r.cie)
			if !keep || cie >= r.next {
				return fmt.Errorf("invalid retained coalesced FDE CIE reference")
			}
			le.PutUint32(data[r.next+4:], uint32(r.next+4-cie))
		}
	}
	for _, p := range pointers {
		next, keep := remap(p.offset)
		if !keep || explicit[p.offset] || p.encoding&0x70 != 0x10 {
			continue
		}
		reader := frameReader{b: data[next:]}
		value, err := reader.encoded(p.encoding&^0x80, 8)
		if err != nil {
			return err
		}
		if p.nullable && value == 0 {
			continue
		}
		delta := int64(p.offset - next)
		if value > math.MaxInt64-delta {
			return fmt.Errorf("coalesced frame pointer overflow")
		}
		value += delta
		width, _ := frameEncodingSize(p.encoding&^0x80, 8)
		if width == 8 {
			le.PutUint64(data[next:], uint64(value))
		} else if p.encoding&15 == 11 {
			if err := signed32(data[next:], value); err != nil {
				return err
			}
		} else if err := unsigned32(data[next:], value); err != nil {
			return err
		}
	}
	for i := range o.symbols {
		sym := &o.symbols[i]
		if sym.section != index {
			continue
		}
		if sym.size != 0 {
			return fmt.Errorf("cannot prune sized Mach-O unwind symbols")
		}
		if next, keep := remap(sym.value); keep {
			sym.value = next
		} else {
			sym.section, sym.value = -3, 0
		}
	}
	if needsAnchor {
		o.symbols = append(o.symbols, symbol{section: index})
	}
	o.relocs = relocs
	s.data, s.size = data, uint64(len(data))
	if len(data) == 0 {
		o.sections[index] = nil
	}
	return nil
}
