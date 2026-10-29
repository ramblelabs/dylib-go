package dylib

import (
	"reflect"
	"testing"
)

func TestCOFFDelayValidation(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		_, base := delayFixtureParts(t, target, delayEntries)
		cases := []struct {
			name  string
			index int
			edit  func([]*object) []*object
		}{
			{"missing_head", 0, func(o []*object) []*object { return o[1:] }},
			{"missing_name_provider", 1, func(o []*object) []*object { return o[:len(o)-1] }},
			{"ambiguous_helper", 1, func(o []*object) []*object { return append(o, copyDirectoryObject(o[0])) }},
			{"descriptor_attributes", 1, func(o []*object) []*object {
				le.PutUint32(o[0].sections[directorySection(t, o[0], ".text$2")].data, 0)
				return o
			}},
			{"bound_table", 1, func(o []*object) []*object {
				le.PutUint32(o[0].sections[directorySection(t, o[0], ".text$2")].data[20:], 1)
				return o
			}},
			{"unload_table", 1, func(o []*object) []*object {
				le.PutUint32(o[0].sections[directorySection(t, o[0], ".text$2")].data[24:], 1)
				return o
			}},
			{"module_offset", 1, func(o []*object) []*object {
				le.PutUint32(o[0].sections[directorySection(t, o[0], ".text$2")].data[8:], 0xffffffff)
				return o
			}},
			{"table_marker", 1, func(o []*object) []*object {
				le.PutUint32(o[0].sections[directorySection(t, o[0], ".idata$4")].data, 1)
				return o
			}},
			{"module_cache", 1, func(o []*object) []*object { o[0].sections[directorySection(t, o[0], ".data")].data[0] = 1; return o }},
			{"head_export", 1, func(o []*object) []*object {
				o[0].symbols = append(o[0].symbols, symbol{name: "ordinary", global: true, section: directorySection(t, o[0], ".text")})
				return o
			}},
			{"head_initializer", 1, func(o []*object) []*object {
				o[0].sections = append(o[0].sections, &section{name: ".CRT$XCU", size: 8, data: make([]byte, 8), lifecycle: lifecycleInit})
				return o
			}},
			{"head_tls", 1, func(o []*object) []*object { o[0].sections[directorySection(t, o[0], ".data")].nativeTLS = 1; return o }},
			{"head_unsupported", 1, func(o []*object) []*object {
				o[0].info.Unsupported = append(o[0].info.Unsupported, "unrelated feature")
				return o
			}},
			{"name_export", 1, func(o []*object) []*object {
				n := o[len(o)-1]
				n.symbols = append(n.symbols, symbol{name: "ordinary_data", section: directorySection(t, n, ".idata$5"), global: true})
				return o
			}},
			{"name_initializer", 1, func(o []*object) []*object {
				n := o[len(o)-1]
				n.sections = append(n.sections, &section{name: ".CRT$XCU", size: 8, data: make([]byte, 8), lifecycle: lifecycleInit})
				return o
			}},
			{"name_tls", 1, func(o []*object) []*object {
				n := o[len(o)-1]
				n.sections[directorySection(t, n, ".idata$5")].nativeTLS = 1
				return o
			}},
			{"name_extra_table", 1, func(o []*object) []*object {
				n := o[len(o)-1]
				n.sections = append(n.sections, &section{name: ".idata$2", size: 20, data: make([]byte, 20)})
				return o
			}},
			{"name_duplicate_section", 1, func(o []*object) []*object {
				n := o[len(o)-1]
				sec := *n.sections[directorySection(t, n, ".idata$7")]
				n.sections = append(n.sections, &sec)
				return o
			}},
			{"head_relocation_bounds", 1, func(o []*object) []*object { o[0].relocs[0].offset = 0xffffffff; return o }},
			{"stub_code", 1, func(o []*object) []*object {
				o[1].sections[directorySection(t, o[1], ".text")].data[0] = 0x90
				return o
			}},
			{"fallback_pointer", 1, func(o []*object) []*object {
				le.PutUint32(o[1].sections[directorySection(t, o[1], ".idata$5")].data, 0xffffffff)
				return o
			}},
			{"duplicate_fixup", 1, func(o []*object) []*object { o[1].relocs = append(o[1].relocs, o[1].relocs[0]); return o }},
			{"wrong_fixup", 1, func(o []*object) []*object { o[1].relocs[0].typ = 0; return o }},
			{"weak_import", 1, func(o []*object) []*object {
				for i, s := range o[1].symbols {
					if s.global && s.section > 0 {
						o[1].symbols[i].weak = true
					}
				}
				return o
			}},
			{"extra_export", 1, func(o []*object) []*object {
				o[1].symbols = append(o[1].symbols, symbol{name: "ordinary", global: true, section: directorySection(t, o[1], ".text")})
				return o
			}},
			{"extra_data", 1, func(o []*object) []*object {
				s := o[1].sections[directorySection(t, o[1], ".data")]
				s.size = 4
				s.data = make([]byte, 4)
				return o
			}},
			{"lookup_missing", 1, func(o []*object) []*object { o[1].sections[directorySection(t, o[1], ".idata$4")] = nil; return o }},
			{"empty_name", 1, func(o []*object) []*object {
				o[1].sections[directorySection(t, o[1], ".idata$6")].data[2] = 0
				return o
			}},
			{"unclaimed_name_data", 1, func(o []*object) []*object {
				s := o[1].sections[directorySection(t, o[1], ".idata$6")]
				s.data = append(s.data, 1)
				s.size++
				return o
			}},
			{"ordinal_zero", 2, func(o []*object) []*object {
				o[2].sections[directorySection(t, o[2], ".idata$4")].data[0] = 0
				return o
			}},
			{"ordinal_reserved", 2, func(o []*object) []*object {
				o[2].sections[directorySection(t, o[2], ".idata$4")].data[2] = 1
				return o
			}},
			{"foreign_provider", 1, func(o []*object) []*object { o[len(o)-1].info.Arch = "foreign"; return o }},
			{"bad_dll_name", 1, func(o []*object) []*object {
				o[len(o)-1].sections[directorySection(t, o[len(o)-1], ".idata$7")].data[0] = 0
				return o
			}},
		}
		for _, c := range cases {
			t.Run(target+"/"+c.name, func(t *testing.T) {
				var objects []*object
				for _, o := range base {
					objects = append(objects, copyDirectoryObject(o))
				}
				objects = c.edit(objects)
				var before []*object
				for _, o := range objects {
					before = append(before, copyDirectoryObject(o))
				}
				if _, ok := newCOFFImportGraph(objects).convert(objects[c.index]); ok {
					t.Fatal("unqualified delay template accepted")
				}
				for i, o := range objects {
					if !reflect.DeepEqual(o, before[i]) {
						t.Fatal("rejection changed snapshot")
					}
				}
			})
		}
	}
}

func TestCOFFDelayCrossArchive(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			parts, _ := delayFixtureParts(t, target, delayEntries)
			var members []*object
			for _, data := range [][][]byte{{parts[0], parts[2]}, {parts[1], parts[3]}} {
				f, err := parse("fragment.lib", longImportArchive(data))
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range f.members {
					members = append(members, m.obj)
				}
			}
			_, views := prepareCOFFImportInputs(nil, members)
			count := 0
			for _, o := range views {
				for _, i := range o.info.Imports {
					if !i.Delay {
						t.Fatal(i)
					}
					count++
				}
			}
			if count != 2 {
				t.Fatal("cross-archive delay references lost", count)
			}
		})
	}
}

func TestCOFFDelayStagedHelpers(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			parts, objects := delayFixtureParts(t, target, delayEntries)
			before := make([]*object, len(objects))
			for i, o := range objects {
				before[i] = copyDirectoryObject(o)
			}
			views, _ := prepareCOFFImportInputs(objects, nil)
			for _, i := range []int{0, len(views) - 1} {
				if len(views[i].sections) != 0 || len(views[i].symbols) != 0 || len(views[i].info.Unsupported) != 0 {
					t.Fatal("staged helper exposed native resolver or table", views[i].info)
				}
			}
			if err := checkCOFFDelayImports(views, DelayImportsDisabled); err == nil {
				t.Fatal("staged delay imports bypassed policy")
			}
			for i, o := range objects {
				if !reflect.DeepEqual(o, before[i]) {
					t.Fatal("staged helper conversion changed source")
				}
			}
			// One valid stub must not consume archive helpers needed to compose
			// another archive's still-isolated stub at a later Link.
			var members []*object
			for _, subset := range [][][]byte{{parts[0], parts[1], parts[3]}, {parts[2]}} {
				f, err := parse("partial.lib", longImportArchive(subset))
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range f.members {
					members = append(members, m.obj)
				}
			}
			_, out := prepareCOFFImportInputs(nil, members)
			count := 0
			for _, o := range out {
				count += len(o.info.Imports)
			}
			if count != 2 {
				t.Fatal("early normalization lost later archive import", count)
			}
		})
	}
}
