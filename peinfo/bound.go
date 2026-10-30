package peinfo

import (
	"bytes"
	"fmt"
)

// InspectBoundDirectory decodes an explicitly bounded IMAGE_BOUND_IMPORT_DESCRIPTOR
// directory, including its forwarder records and relative-name strings. An empty
// input represents an absent directory. Offsets are relative to data[0], not a PE
// image or COFF section. It returns no partial result and retains no input bytes.
// This reads metadata only; timestamps never authorize using cached addresses.
func InspectBoundDirectory(data []byte) ([]BoundImport, error) {
	if len(data) > maxInput {
		return nil, fmt.Errorf("bound directory exceeds %d bytes", maxInput)
	}
	r := &reader{names: maxInput}
	return r.boundDirectory(data)
}

func (r *reader) bound() ([]BoundImport, error) {
	b, _, err := r.directory(11)
	if err != nil {
		return nil, err
	}
	return r.boundDirectory(b)
}

func (r *reader) boundDirectory(b []byte) ([]BoundImport, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var err error
	type names struct {
		dll        uint16
		forwarders []uint16
	}
	var out []BoundImport
	var offsets []names
	position := 0
	for {
		if len(b)-position < 8 {
			return nil, fmt.Errorf("missing null bound descriptor")
		}
		v := b[position : position+8]
		position += 8
		if zero(v) {
			break
		}
		if err := r.take(); err != nil {
			return nil, err
		}
		d := BoundImport{TimeDateStamp: little.Uint32(v)}
		n := names{dll: little.Uint16(v[4:])}
		count := int(little.Uint16(v[6:]))
		if count > (len(b)-position)/8 {
			return nil, fmt.Errorf("bound forwarders exceed directory")
		}
		for i := 0; i < count; i++ {
			if err := r.take(); err != nil {
				return nil, err
			}
			v := b[position : position+8]
			position += 8
			d.Forwarders = append(d.Forwarders, BoundForwarder{TimeDateStamp: little.Uint32(v), Reserved: little.Uint16(v[6:])})
			n.forwarders = append(n.forwarders, little.Uint16(v[4:]))
		}
		out = append(out, d)
		offsets = append(offsets, n)
	}
	name := func(offset uint16) (string, error) {
		if int(offset) >= len(b) {
			return "", fmt.Errorf("bound name outside directory")
		}
		tail := b[int(offset):]
		end := bytes.IndexByte(tail, 0)
		if end <= 0 || end > r.names {
			return "", fmt.Errorf("missing, empty or over-budget bound name")
		}
		r.names -= end
		return string(tail[:end]), nil
	}
	for i, n := range offsets {
		if out[i].DLL, err = name(n.dll); err != nil {
			return nil, err
		}
		for j, offset := range n.forwarders {
			if out[i].Forwarders[j].DLL, err = name(offset); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
