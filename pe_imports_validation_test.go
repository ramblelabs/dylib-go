package dylib

import (
	"bytes"
	"reflect"
	"sync"
	"testing"

	"github.com/ramblelabs/dylib-go/peinfo"
)

func peFixtureWord(f peImportFixture, b []byte, value uint64) {
	if f.width == 8 {
		le.PutUint64(b, value)
	} else {
		le.PutUint32(b, uint32(value))
	}
}
func TestPEImportMetadataValidation(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			normal := buildPEImportFixture(t, target, "-O2", "normal")
			bound := buildPEImportFixture(t, target, "-O2", "bound")
			delay := buildPEImportFixture(t, target, "-O2", "delay_bound")
			_, ns := normal.directory(1)
			nr, first := normal.descriptor(t, 1, "pe-first.dll")
			lookup, iat := le.Uint32(first), le.Uint32(first[16:])
			dll := le.Uint32(first[12:])
			hint := uint32(0)
			v := normal.at(t, lookup, normal.width)
			if normal.width == 8 {
				hint = uint32(le.Uint64(v))
			} else {
				hint = le.Uint32(v)
			}
			br, bs := bound.directory(11)
			_, ds := delay.directory(13)
			dr, dd := delay.descriptor(t, 13, "pe-first.dll")
			da, dn, db, du := le.Uint32(dd[12:]), le.Uint32(dd[16:]), le.Uint32(dd[20:]), le.Uint32(dd[24:])
			cases := []struct {
				name string
				base peImportFixture
				edit func(peImportFixture)
			}{
				{"overlapping_sections", normal, func(f peImportFixture) {
					header := le.Uint32(f.data[60:])
					start := header + 24 + uint32(le.Uint16(f.data[header+20:]))
					le.PutUint32(f.data[start+40+12:], le.Uint32(f.data[start+12:]))
				}},
				{"section_rva_overflow", normal, func(f peImportFixture) {
					header := le.Uint32(f.data[60:])
					start := header + 24 + uint32(le.Uint16(f.data[header+20:]))
					le.PutUint32(f.data[start+12:], 0xfffffff0)
				}},
				{"section_file_bounds", normal, func(f peImportFixture) {
					header := le.Uint32(f.data[60:])
					start := header + 24 + uint32(le.Uint16(f.data[header+20:]))
					le.PutUint32(f.data[start+20:], 0xffffffff)
				}},
				{"headers_file_bounds", normal, func(f peImportFixture) {
					header := le.Uint32(f.data[60:])
					le.PutUint32(f.data[header+24+60:], 0xffffffff)
				}},
				{"optional_header_magic", normal, func(f peImportFixture) { le.PutUint16(f.data[le.Uint32(f.data[60:])+24:], 0) }},
				{"symbol_allocation", normal, func(f peImportFixture) { le.PutUint32(f.data[le.Uint32(f.data[60:])+16:], 0xffffffff) }},
				{"directory_rva", normal, func(f peImportFixture) { le.PutUint32(f.data[f.directoryOffset+8:], 0xfffffffe) }},
				{"directory_size", normal, func(f peImportFixture) { le.PutUint32(f.data[f.directoryOffset+12:], 0xffffffff) }},
				{"partial_descriptor", normal, func(f peImportFixture) { le.PutUint32(f.data[f.directoryOffset+12:], 19) }},
				{"missing_descriptor_terminator", normal, func(f peImportFixture) { le.PutUint32(f.data[f.directoryOffset+12:], ns-20) }},
				{"dll_rva", normal, func(f peImportFixture) { le.PutUint32(f.at(t, nr+12, 4), 0xffffffff) }},
				{"empty_dll", normal, func(f peImportFixture) { f.at(t, dll, 1)[0] = 0 }},
				{"lookup_rva", normal, func(f peImportFixture) { le.PutUint32(f.at(t, nr, 4), 0xffffffff) }},
				{"iat_rva", normal, func(f peImportFixture) { le.PutUint32(f.at(t, nr+16, 4), 0xffffffff) }},
				{"hint_rva", normal, func(f peImportFixture) { peFixtureWord(f, f.at(t, lookup, f.width), 0x7fffffff) }},
				{"empty_symbol", normal, func(f peImportFixture) { f.at(t, hint+2, 1)[0] = 0 }},
				{"reserved_ordinal_bits", normal, func(f peImportFixture) { peFixtureWord(f, f.at(t, lookup, f.width), uint64(1)<<(f.width*8-1)|0x10007) }},
				{"lookup_terminator", normal, func(f peImportFixture) { peFixtureWord(f, f.at(t, lookup, f.width), 0) }},
				{"iat_terminator", normal, func(f peImportFixture) { peFixtureWord(f, f.at(t, iat+f.width, f.width), 1) }},
				{"bound_rva", bound, func(f peImportFixture) { le.PutUint32(f.data[f.directoryOffset+11*8:], 0xffffffff) }},
				{"partial_bound", bound, func(f peImportFixture) { le.PutUint32(f.data[f.directoryOffset+11*8+4:], 7) }},
				{"bound_forwarder_count", bound, func(f peImportFixture) { le.PutUint16(f.at(t, br+6, 2), 0xffff) }},
				{"bound_name_in_records", bound, func(f peImportFixture) { le.PutUint16(f.at(t, br+4, 2), 1) }},
				{"bound_name_outside", bound, func(f peImportFixture) { le.PutUint16(f.at(t, br+4, 2), uint16(bs)) }},
				{"bound_forwarder_name", bound, func(f peImportFixture) { le.PutUint16(f.at(t, br+12, 2), 0xffff) }},
				{"bound_missing_terminator", bound, func(f peImportFixture) { le.PutUint32(f.data[f.directoryOffset+11*8+4:], 32) }},
				{"bound_empty_name", bound, func(f peImportFixture) { offset := le.Uint16(f.at(t, br+4, 2)); f.at(t, br+uint32(offset), 1)[0] = 0 }},
				{"partial_delay", delay, func(f peImportFixture) { le.PutUint32(f.data[f.directoryOffset+13*8+4:], 31) }},
				{"delay_missing_terminator", delay, func(f peImportFixture) { le.PutUint32(f.data[f.directoryOffset+13*8+4:], ds-32) }},
				{"delay_name_rva", delay, func(f peImportFixture) { le.PutUint32(f.at(t, dr+4, 4), 0xffffffff) }},
				{"delay_module_handle", delay, func(f peImportFixture) { le.PutUint32(f.at(t, dr+8, 4), 0xffffffff) }},
				{"delay_lookup", delay, func(f peImportFixture) { le.PutUint32(f.at(t, dr+16, 4), 0xffffffff) }},
				{"delay_iat", delay, func(f peImportFixture) { le.PutUint32(f.at(t, dr+12, 4), 0xffffffff) }},
				{"delay_bound_table", delay, func(f peImportFixture) { le.PutUint32(f.at(t, dr+20, 4), 0xffffffff) }},
				{"delay_unload_table", delay, func(f peImportFixture) { le.PutUint32(f.at(t, dr+24, 4), 0xffffffff) }},
				{"delay_iat_terminator", delay, func(f peImportFixture) { peFixtureWord(f, f.at(t, da+f.width, f.width), 1) }},
				{"delay_bound_terminator", delay, func(f peImportFixture) { peFixtureWord(f, f.at(t, db+f.width, f.width), 1) }},
				{"delay_unload_terminator", delay, func(f peImportFixture) { peFixtureWord(f, f.at(t, du+f.width, f.width), 1) }},
				{"delay_legacy_below_base", delay, func(f peImportFixture) { le.PutUint32(f.at(t, dr, 4), 0) }},
				{"delay_lookup_reserved", delay, func(f peImportFixture) { peFixtureWord(f, f.at(t, dn, f.width), uint64(1)<<(f.width*8-1)|0x10007) }},
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					f := c.base
					f.data = append([]byte(nil), f.data...)
					c.edit(f)
					before := append([]byte(nil), f.data...)
					got, err := peinfo.Inspect(f.data)
					if err == nil || !reflect.DeepEqual(got, peinfo.Imports{}) {
						t.Fatal("malformed input accepted or partial metadata returned", got, err)
					}
					if !bytes.Equal(before, f.data) {
						t.Fatal("rejected input changed")
					}
				})
			}
		})
	}
}

func TestPEImportMetadataCacheIdentity(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			base := buildPEImportFixture(t, target, "-O2", "normal")
			rva, d := base.descriptor(t, 1, "pe-first.dll")
			iat := le.Uint32(d[16:])
			cached := directoryCachedAddress(target, 0, 0)
			for _, mode := range []string{"ilt_cache", "iat_lookup", "unknown_bound_lookup", "unmarked_cache"} {
				t.Run(mode, func(t *testing.T) {
					f := base
					f.data = append([]byte(nil), base.data...)
					descriptor := f.at(t, rva, 20)
					if mode != "ilt_cache" {
						le.PutUint32(descriptor, 0)
					}
					if mode != "iat_lookup" {
						peFixtureWord(f, f.at(t, iat, f.width), cached)
					}
					if mode == "unknown_bound_lookup" {
						le.PutUint32(descriptor[4:], 1)
					}
					got, err := peinfo.Inspect(f.data)
					if mode == "unmarked_cache" {
						if err == nil {
							t.Fatal("unmarked cache was guessed as a name")
						}
						return
					}
					var entry peinfo.ImportDirectory
					for _, directory := range got.Directories {
						if directory.DLL == "pe-first.dll" {
							entry = directory
						}
					}
					if err != nil || len(entry.Symbols) != 1 {
						t.Fatal(got, err)
					}
					if mode == "unknown_bound_lookup" {
						if entry.LookupAvailable || entry.Symbols[0].Name != "" || entry.Symbols[0].ByOrdinal || entry.Symbols[0].Address != cached {
							t.Fatal("cached pointer became lookup identity", entry)
						}
					} else if !entry.LookupAvailable || entry.Symbols[0].Name != "first" {
						t.Fatal("lookup identity lost", entry)
					}
				})
			}
		})
	}
}

func TestPEImportMetadataConcurrent(t *testing.T) {
	target := "x86_64-pc-windows-msvc"
	f := buildPEImportFixture(t, target, "-O2", "delay_bound")
	want, err := peinfo.Inspect(f.data)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				got, err := peinfo.Inspect(f.data)
				if err != nil || !reflect.DeepEqual(got, want) {
					errors <- "concurrent metadata differed"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}
