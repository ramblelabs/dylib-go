package dylib

import (
	"reflect"
	"testing"
)

func TestCOFFImportDirectoryIATLookupValidation(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			base := stagedIATLookup(t, target)
			dir, iat := directorySection(t, base, ".idata$2"), directorySection(t, base, ".idata$5")
			width := uint64(base.info.Bits / 8)
			var ordinal uint64
			for _, s := range base.symbols {
				if s.name == "__imp_numbered" {
					ordinal = s.value
				}
			}
			if ordinal == 0 {
				t.Fatal("missing ordinal fixture")
			}
			remove := func(o *object, section int, offset uint64) {
				for i, r := range o.relocs {
					if r.section == section && r.offset == offset {
						o.relocs = append(o.relocs[:i], o.relocs[i+1:]...)
						return
					}
				}
				t.Fatal("missing relocation to remove")
			}
			cases := []struct {
				name string
				edit func(*object)
			}{
				{"nonzero_lookup_without_relocation", func(o *object) { o.sections[dir].data[0] = 1 }},
				{"invalid_lookup_relocation", func(o *object) {
					r := *directoryRelocation(t, o, dir, 16)
					r.offset, r.typ = 0, 0xffff
					o.relocs = append(o.relocs, r)
				}},
				{"explicit_lookup_to_iat", func(o *object) {
					r := *directoryRelocation(t, o, dir, 16)
					r.offset = 0
					o.relocs = append(o.relocs, r)
				}},
				{"bound_timestamp", func(o *object) { o.sections[dir].data[4] = 1 }},
				{"forwarder_chain", func(o *object) { o.sections[dir].data[8] = 1 }},
				{"missing_name_reference", func(o *object) { remove(o, dir, 12) }},
				{"missing_iat_reference", func(o *object) { remove(o, dir, 16) }},
				{"bound_iat_address", func(o *object) {
					remove(o, iat, 0)
					le.PutUint32(o.sections[iat].data, 0x7fff1234)
				}},
				{"named_entry_high_bits", func(o *object) { o.sections[iat].data[width-1] |= 0x40 }},
				{"ordinal_extra_bits", func(o *object) { o.sections[iat].data[ordinal+2] = 1 }},
				{"zero_ordinal", func(o *object) { o.sections[iat].data[ordinal] = 0 }},
				{"ordinal_relocation", func(o *object) {
					r := *directoryRelocation(t, o, iat, 0)
					r.offset = ordinal
					o.relocs = append(o.relocs, r)
				}},
				{"missing_iat_terminator", func(o *object) {
					s := o.sections[iat]
					s.size -= width
					s.data = s.data[:s.size]
				}},
				{"missing_null_descriptor", func(o *object) {
					s := o.sections[dir]
					s.size -= 20
					s.data = s.data[:s.size]
				}},
				{"overlapping_iat", func(o *object) {
					directoryRelocation(t, o, dir, 36).symbol = directoryRelocation(t, o, dir, 16).symbol
					copy(o.sections[dir].data[36:40], o.sections[dir].data[16:20])
				}},
				{"unclaimed_ilt_bytes", func(o *object) {
					b := make([]byte, width)
					b[0] = 1
					o.sections = append(o.sections, &section{name: ".idata$4", data: b, size: width, align: width})
				}},
				{"unclaimed_ilt_relocation", func(o *object) {
					o.sections = append(o.sections, &section{name: ".idata$4", data: make([]byte, width), size: width, align: width})
					r := *directoryRelocation(t, o, iat, 0)
					r.section = len(o.sections) - 1
					o.relocs = append(o.relocs, r)
				}},
				{"null_descriptor_relocation", func(o *object) {
					r := *directoryRelocation(t, o, dir, 12)
					r.offset = 52
					o.relocs = append(o.relocs, r)
				}},
				{"duplicate_iat_relocation", func(o *object) { o.relocs = append(o.relocs, *directoryRelocation(t, o, iat, 0)) }},
			}
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					o := copyDirectoryObject(base)
					test.edit(o)
					before := copyDirectoryObject(o)
					if _, ok := newCOFFImportGraph([]*object{o}).convert(o); ok {
						t.Fatal("malformed IAT lookup accepted")
					}
					if !reflect.DeepEqual(o, before) {
						t.Fatal("failed conversion changed input")
					}
				})
			}
		})
	}
}
