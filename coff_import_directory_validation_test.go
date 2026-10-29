package dylib

import (
	"reflect"
	"testing"
)

func directorySection(t *testing.T, o *object, name string) int {
	t.Helper()
	for i, s := range o.sections {
		if s != nil && s.name == name {
			return i
		}
	}
	t.Fatal("missing directory section", name)
	return 0
}

func directoryRelocation(t *testing.T, o *object, section int, offset uint64) *relocation {
	t.Helper()
	for i := range o.relocs {
		r := &o.relocs[i]
		if r.section == section && r.offset == offset {
			return r
		}
	}
	t.Fatal("missing directory relocation", section, offset)
	return nil
}

func copyDirectoryObject(o *object) *object {
	copy := *o
	copy.sections = append([]*section(nil), o.sections...)
	for i, s := range o.sections {
		if s != nil {
			next := *s
			if s.data != nil {
				next.data = append(make([]byte, 0, len(s.data)), s.data...)
			}
			copy.sections[i] = &next
		}
	}
	copy.symbols = append([]symbol(nil), o.symbols...)
	copy.relocs = append([]relocation(nil), o.relocs...)
	copy.groups = append([]sectionGroup(nil), o.groups...)
	for i := range copy.groups {
		copy.groups[i].sections = append([]int(nil), o.groups[i].sections...)
	}
	return &copy
}

func TestCOFFImportDirectoryValidation(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			base := stagedDirectory(t, target)
			dir, ilt, iat, hint, text := directorySection(t, base, ".idata$2"), directorySection(t, base, ".idata$4"), directorySection(t, base, ".idata$5"), directorySection(t, base, ".idata$6"), directorySection(t, base, ".text")
			ordinal := uint64(0)
			for _, s := range base.symbols {
				if s.name == "__imp_numbered" {
					ordinal = s.value
				}
			}
			if ordinal == 0 {
				t.Fatal("ordinal fixture missing")
			}
			cases := []struct {
				name   string
				change func(*object)
			}{
				{"missing_directory", func(o *object) { o.sections[dir].name = ".unknown" }},
				{"missing_ilt", func(o *object) { o.sections[ilt].name = ".unknown" }},
				{"missing_iat", func(o *object) { o.sections[iat].name = ".unknown" }},
				{"partial_directory", func(o *object) { s := o.sections[dir]; s.data = s.data[:len(s.data)-1]; s.size-- }},
				{"missing_null_descriptor", func(o *object) { s := o.sections[dir]; s.data = s.data[:len(s.data)-20]; s.size -= 20 }},
				{"descriptor_timestamp", func(o *object) { o.sections[dir].data[4] = 1 }},
				{"descriptor_forwarder", func(o *object) { o.sections[dir].data[8] = 1 }},
				{"wrong_rva_kind", func(o *object) { directoryRelocation(t, o, dir, 0).typ = 0xffff }},
				{"rva_outside_section", func(o *object) { le.PutUint32(o.sections[dir].data, 0xffffffff) }},
				{"rva_symbol_index", func(o *object) { directoryRelocation(t, o, dir, 0).symbol = len(o.symbols) }},
				{"external_metadata", func(o *object) {
					r := directoryRelocation(t, o, dir, 0)
					r.symbol = len(o.symbols)
					o.symbols = append(o.symbols, symbol{name: "external_table", global: true})
				}},
				{"overlapping_iat", func(o *object) {
					directoryRelocation(t, o, dir, 36).symbol = directoryRelocation(t, o, dir, 16).symbol
					copy(o.sections[dir].data[36:40], o.sections[dir].data[16:20])
				}},
				{"overlapping_ilt", func(o *object) {
					directoryRelocation(t, o, dir, 20).symbol = directoryRelocation(t, o, dir, 0).symbol
					copy(o.sections[dir].data[20:24], o.sections[dir].data[:4])
				}},
				{"partial_iat", func(o *object) { s := o.sections[iat]; s.data = s.data[:len(s.data)-1]; s.size-- }},
				{"different_table_bytes", func(o *object) { o.sections[iat].data[0] ^= 1 }},
				{"different_table_targets", func(o *object) {
					r := directoryRelocation(t, o, iat, 0)
					r.symbol = len(o.symbols)
					o.symbols = append(o.symbols, symbol{section: hint, value: 2})
				}},
				{"unclaimed_iat_bytes", func(o *object) {
					s := o.sections[iat]
					s.data = append(s.data, make([]byte, o.info.Bits/8)...)
					s.data[len(s.data)-1] = 1
					s.size = uint64(len(s.data))
				}},
				{"zero_ordinal", func(o *object) {
					for _, i := range []int{ilt, iat} {
						o.sections[i].data[ordinal] = 0
					}
				}},
				{"ordinal_extra_bits", func(o *object) {
					for _, i := range []int{ilt, iat} {
						o.sections[i].data[ordinal+2] = 1
					}
				}},
				{"ordinal_relocation", func(o *object) {
					r := *directoryRelocation(t, o, iat, 0)
					r.offset = ordinal
					o.relocs = append(o.relocs, r)
				}},
				{"missing_public_slot", func(o *object) {
					for i := range o.symbols {
						if o.symbols[i].name == "__imp_first" {
							o.symbols[i].global = false
						}
					}
				}},
				{"duplicate_public_slot", func(o *object) {
					o.symbols = append(o.symbols, symbol{name: "__imp_other", section: iat, global: true})
				}},
				{"metadata_export", func(o *object) {
					o.symbols = append(o.symbols, symbol{name: "metadata_export", section: hint, value: 1, global: true})
				}},
				{"duplicate_fixup", func(o *object) { o.relocs = append(o.relocs, *directoryRelocation(t, o, dir, 0)) }},
				{"extra_metadata_fixup", func(o *object) {
					r := *directoryRelocation(t, o, dir, 0)
					r.section, r.offset = hint, 0
					o.relocs = append(o.relocs, r)
				}},
				{"thunk_bytes", func(o *object) { o.sections[text].data[0] = 0xcc }},
				{"extra_thunk_bytes", func(o *object) { s := o.sections[text]; s.data = append(s.data, 0, 0, 0, 0); s.size += 4 }},
				{"executable_metadata", func(o *object) { o.sections[iat].exec = true }},
				{"import_comdat", func(o *object) {
					o.groups = append(o.groups, sectionGroup{key: "import_group", selection: 2, sections: []int{iat}})
				}},
				{"unknown_idata", func(o *object) { o.sections = append(o.sections, &section{name: ".idata$8", align: 4}) }},
				{"incoming_helper_reference", func(o *object) {
					o.sections = append(o.sections, &section{name: ".data$helper", data: make([]byte, 8), size: 8, align: 8})
					o.symbols = append(o.symbols, symbol{section: hint})
					o.relocs = append(o.relocs, relocation{section: len(o.sections) - 1, symbol: len(o.symbols) - 1, pair: -1, typ: 1})
				}},
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					o := copyDirectoryObject(base)
					c.change(o)
					before := copyDirectoryObject(o)
					if _, ok := newCOFFImportGraph([]*object{o}).convert(o); ok {
						t.Fatal("unqualified directory was normalized")
					}
					if !reflect.DeepEqual(o, before) {
						t.Fatal("failed conversion mutated staged input")
					}
				})
			}
		})
	}
}
