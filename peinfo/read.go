package peinfo

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"sort"
)

const maxInput = 256 << 20
const maxEntries = 65536

var little = binary.LittleEndian

type region struct {
	start, end, fileEnd uint64
	offset              uint32
}
type reader struct {
	data           []byte
	regions        []region
	directories    [16]pe.DataDirectory
	base           uint64
	width          uint32
	entries, names int
}

// Inspect decodes normal, bound and delay imports from a PE32 or PE32+ image.
// It rejects ambiguous mappings, out-of-bounds records and unterminated tables.
// It returns no partial result on failure and does not retain the input buffer.
func Inspect(data []byte) (Imports, error) {
	r, err := newReader(data)
	if err != nil {
		return Imports{}, err
	}
	var result Imports
	if result.Directories, err = r.imports(); err != nil {
		return Imports{}, fmt.Errorf("PE imports: %w", err)
	}
	if result.Bound, err = r.bound(); err != nil {
		return Imports{}, fmt.Errorf("PE bound imports: %w", err)
	}
	if result.Delay, err = r.delay(); err != nil {
		return Imports{}, fmt.Errorf("PE delay imports: %w", err)
	}
	return result, nil
}

func newReader(data []byte) (*reader, error) {
	if len(data) < 64 || len(data) > maxInput || string(data[:2]) != "MZ" {
		return nil, fmt.Errorf("expected PE image of at most %d bytes", maxInput)
	}
	header := uint64(little.Uint32(data[60:]))
	if header > uint64(len(data)) || uint64(len(data))-header < 24 || string(data[header:header+4]) != "PE\x00\x00" {
		return nil, fmt.Errorf("invalid PE header")
	}
	// Bound debug/pe's symbol-table allocation before delegating header parsing.
	symbols := uint64(little.Uint32(data[header+16:]))
	symoff := uint64(little.Uint32(data[header+12:]))
	if symbols > uint64(len(data))/18 || symoff > uint64(len(data)) || symbols*18 > uint64(len(data))-symoff {
		return nil, fmt.Errorf("PE symbol table exceeds input")
	}
	f, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := &reader{data: data, names: maxInput}
	var headers uint32
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		r.width, r.base, r.directories, headers = 4, uint64(h.ImageBase), h.DataDirectory, h.SizeOfHeaders
	case *pe.OptionalHeader64:
		r.width, r.base, r.directories, headers = 8, h.ImageBase, h.DataDirectory, h.SizeOfHeaders
	default:
		return nil, fmt.Errorf("expected PE32 or PE32+ optional header")
	}
	if uint64(headers) > uint64(len(data)) {
		return nil, fmt.Errorf("PE headers exceed input")
	}
	if headers != 0 {
		r.regions = append(r.regions, region{end: uint64(headers), fileEnd: uint64(headers)})
	}
	for _, s := range f.Sections {
		start := uint64(s.VirtualAddress)
		size := uint64(s.Size)
		memory := max(size, uint64(s.VirtualSize))
		if start+memory > 1<<32 || uint64(s.Offset) > uint64(len(data)) || size > uint64(len(data))-uint64(s.Offset) {
			return nil, fmt.Errorf("PE section exceeds image or file bounds")
		}
		if memory != 0 {
			r.regions = append(r.regions, region{start: start, end: start + memory, fileEnd: start + size, offset: s.Offset})
		}
	}
	sort.Slice(r.regions, func(i, j int) bool { return r.regions[i].start < r.regions[j].start })
	for i := 1; i < len(r.regions); i++ {
		if r.regions[i].start < r.regions[i-1].end {
			return nil, fmt.Errorf("overlapping PE virtual sections")
		}
	}
	return r, nil
}

func (r *reader) location(rva uint64, size uint64, backed bool) (region, error) {
	if rva == 0 || rva > 0xffffffff || size > 1<<32-rva {
		return region{}, fmt.Errorf("invalid RVA range")
	}
	i := sort.Search(len(r.regions), func(i int) bool { return r.regions[i].start > rva }) - 1
	if i < 0 {
		return region{}, fmt.Errorf("RVA outside image")
	}
	s := r.regions[i]
	end := s.end
	if backed {
		end = s.fileEnd
	}
	if rva > end || size > end-rva {
		return region{}, fmt.Errorf("RVA outside mapped %s", map[bool]string{true: "file data", false: "image data"}[backed])
	}
	return s, nil
}
func (r *reader) read(rva uint64, size uint64) ([]byte, error) {
	s, err := r.location(rva, size, true)
	if err != nil {
		return nil, err
	}
	offset := uint64(s.offset) + rva - s.start
	return r.data[offset : offset+size], nil
}
func (r *reader) directory(index int) ([]byte, uint64, error) {
	d := r.directories[index]
	if d.Size == 0 {
		return nil, 0, nil
	}
	b, err := r.read(uint64(d.VirtualAddress), uint64(d.Size))
	return b, uint64(d.VirtualAddress), err
}
func (r *reader) name(rva uint64) (string, error) {
	s, err := r.location(rva, 1, true)
	if err != nil {
		return "", err
	}
	b, err := r.read(rva, s.fileEnd-rva)
	if err != nil {
		return "", err
	}
	end := bytes.IndexByte(b, 0)
	if end <= 0 || end > r.names {
		return "", fmt.Errorf("missing, empty or over-budget import name")
	}
	r.names -= end
	return string(b[:end]), nil
}
func (r *reader) word(rva uint64) (uint64, error) {
	b, err := r.read(rva, uint64(r.width))
	if err != nil {
		return 0, err
	}
	if r.width == 8 {
		return little.Uint64(b), nil
	}
	return uint64(little.Uint32(b)), nil
}
func (r *reader) take() error {
	if r.entries >= maxEntries {
		return fmt.Errorf("import record budget exceeded")
	}
	r.entries++
	return nil
}
func zero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

func (r *reader) symbol(word uint64, legacy bool) (Symbol, error) {
	flag := uint64(1) << (r.width*8 - 1)
	if word&flag != 0 {
		if word & ^(flag|0xffff) != 0 || uint16(word) == 0 {
			return Symbol{}, fmt.Errorf("invalid ordinal word")
		}
		return Symbol{Ordinal: uint16(word)}, nil
	}
	if legacy {
		if word < r.base {
			return Symbol{}, fmt.Errorf("legacy import name VA below image base")
		}
		word -= r.base
	}
	b, err := r.read(word, 2)
	if err != nil {
		return Symbol{}, err
	}
	name, err := r.name(word + 2)
	if err != nil {
		return Symbol{}, err
	}
	return Symbol{Name: name, Hint: little.Uint16(b)}, nil
}
func (r *reader) symbols(lookup, iat uint64, known, legacy bool) ([]Symbol, error) {
	var out []Symbol
	for i := uint64(0); ; i++ {
		a, err := r.word(iat + i*uint64(r.width))
		if err != nil {
			return nil, err
		}
		word := a
		if known {
			word, err = r.word(lookup + i*uint64(r.width))
			if err != nil {
				return nil, err
			}
		}
		if word == 0 {
			if a != 0 {
				return nil, fmt.Errorf("IAT terminator differs from lookup table")
			}
			return out, nil
		}
		if err := r.take(); err != nil {
			return nil, err
		}
		s := Symbol{}
		if known {
			if s, err = r.symbol(word, legacy); err != nil {
				return nil, err
			}
		}
		s.Address = a
		out = append(out, s)
	}
}
func (r *reader) imports() ([]ImportDirectory, error) {
	b, _, err := r.directory(1)
	if err != nil || len(b) == 0 {
		return nil, err
	}
	var out []ImportDirectory
	for offset := 0; offset+20 <= len(b); offset += 20 {
		v := b[offset : offset+20]
		if zero(v) {
			return out, nil
		}
		if err := r.take(); err != nil {
			return nil, err
		}
		d := ImportDirectory{LookupRVA: little.Uint32(v), TimeDateStamp: little.Uint32(v[4:]), ForwarderChain: little.Uint32(v[8:]), AddressRVA: little.Uint32(v[16:])}
		if d.DLL, err = r.name(uint64(little.Uint32(v[12:]))); err != nil {
			return nil, err
		}
		lookup := d.LookupRVA
		d.LookupAvailable = lookup != 0 || d.TimeDateStamp == 0
		if lookup == 0 {
			lookup = d.AddressRVA
		}
		if d.Symbols, err = r.symbols(uint64(lookup), uint64(d.AddressRVA), d.LookupAvailable, false); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return nil, fmt.Errorf("missing null import descriptor")
}
