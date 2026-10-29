package peinfo

import "fmt"

func (r *reader) delayRVA(value uint32, attributes uint32) (uint32, error) {
	if value == 0 || attributes&1 != 0 {
		return value, nil
	}
	if uint64(value) < r.base || uint64(value)-r.base > 0xffffffff {
		return 0, fmt.Errorf("legacy delay VA outside image address range")
	}
	return uint32(uint64(value) - r.base), nil
}
func (r *reader) addresses(rva uint32, count int) ([]uint64, error) {
	if rva == 0 {
		return nil, nil
	}
	var out []uint64
	for i := 0; i <= count; i++ {
		word, err := r.word(uint64(rva) + uint64(i)*uint64(r.width))
		if err != nil {
			return nil, err
		}
		if i == count {
			if word != 0 {
				return nil, fmt.Errorf("missing auxiliary delay table terminator")
			}
			break
		}
		if err := r.take(); err != nil {
			return nil, err
		}
		out = append(out, word)
	}
	return out, nil
}
func (r *reader) delay() ([]DelayImport, error) {
	b, _, err := r.directory(13)
	if err != nil || len(b) == 0 {
		return nil, err
	}
	var out []DelayImport
	for offset := 0; offset+32 <= len(b); offset += 32 {
		v := b[offset : offset+32]
		if zero(v) {
			return out, nil
		}
		if err := r.take(); err != nil {
			return nil, err
		}
		d := DelayImport{Attributes: little.Uint32(v), TimeDateStamp: little.Uint32(v[28:])}
		var fields [6]uint32
		for i := range fields {
			fields[i], err = r.delayRVA(little.Uint32(v[4+i*4:]), d.Attributes)
			if err != nil {
				return nil, err
			}
		}
		if d.DLL, err = r.name(uint64(fields[0])); err != nil {
			return nil, err
		}
		d.ModuleHandleRVA, d.AddressRVA, d.NameTableRVA, d.BoundAddressRVA, d.UnloadAddressRVA = fields[1], fields[2], fields[3], fields[4], fields[5]
		if d.ModuleHandleRVA != 0 {
			if _, err = r.location(uint64(d.ModuleHandleRVA), uint64(r.width), false); err != nil {
				return nil, err
			}
		}
		if d.Symbols, err = r.symbols(uint64(d.NameTableRVA), uint64(d.AddressRVA), true, d.Attributes&1 == 0); err != nil {
			return nil, err
		}
		if d.BoundAddresses, err = r.addresses(d.BoundAddressRVA, len(d.Symbols)); err != nil {
			return nil, err
		}
		if d.UnloadAddresses, err = r.addresses(d.UnloadAddressRVA, len(d.Symbols)); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return nil, fmt.Errorf("missing null delay descriptor")
}
