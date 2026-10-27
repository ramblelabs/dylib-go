package dylib

import "fmt"

// Frame PCs describe the input's storage, including a retained body whose
// public alias was independently overridden. Personality pointers still bind
// through their public names. Parsed inputs are untouched by this link step.
func (o *object) canonicalizeMachOAliasFrames() error {
	var scratch *image
	for index, s := range o.sections {
		if s == nil || s.name != "__eh_frame" && s.name != "__compact_unwind" {
			continue
		}
		needed := false
		for _, r := range o.relocs {
			if r.section != index || r.local || s.name == "__compact_unwind" && r.offset%32 != 0 {
				continue
			}
			for _, operand := range []int{r.symbol, r.pair} {
				if operand < 0 || operand >= len(o.symbols) {
					continue
				}
				alias := o.symbols[operand].forward
				if alias != nil && alias.storage {
					key := o.symbols[alias.target]
					needed = needed || key.section > 0 && o.sections[key.section] != nil && o.sections[key.section].exec
				}
			}
		}
		if !needed {
			continue
		}
		pcs := make(map[uint64]bool)
		if s.name == "__compact_unwind" {
			if s.size%32 != 0 || s.size/32 > 65536 {
				return fmt.Errorf("invalid aliased compact unwind storage")
			}
			for offset := uint64(0); offset < s.size; offset += 32 {
				pcs[offset] = true
			}
		} else {
			if _, err := machoFrameRecords(s); err != nil {
				return err
			}
			if scratch == nil {
				var err error
				scratch, err = machoOriginalUnwindImage(o)
				if err != nil {
					return err
				}
			}
			policy := framePolicy{pcRelative: true, language: true,
				fixup: func(offset uint64, _ byte) error { pcs[offset] = true; return nil },
			}
			if _, err := scratch.validateFrameRecords(scratch.mem[s.original:s.original+s.size], s.original, policy); err != nil {
				return fmt.Errorf("aliased Mach-O frames: %w", err)
			}
		}
		for i, r := range o.relocs {
			if r.section != index || !pcs[r.offset] || r.local {
				continue
			}
			for _, operand := range []struct {
				index *int
				sign  int64
			}{{&r.symbol, 1}, {&r.pair, 1}} {
				if *operand.index < 0 || *operand.index >= len(o.symbols) {
					continue
				}
				alias := o.symbols[*operand.index].forward
				if alias == nil || !alias.storage {
					continue
				}
				if operand.index == &r.symbol && r.pair < 0 && machoAtomAddressRelocation(o, r) {
					r.targetOffset += alias.offset
				} else {
					if alias.offset != 0 && !machoAtomOffsetRelocation(o, r) {
						return fmt.Errorf("unsupported public alias frame relocation %d", r.typ)
					}
					if operand.index == &r.symbol && r.pair >= 0 {
						operand.sign = -1
					}
					r.addend += operand.sign * int64(alias.offset)
				}
				*operand.index = alias.target
			}
			o.relocs[i] = r
		}
	}
	return nil
}
