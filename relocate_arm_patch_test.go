package dylib

import (
	"math"
	"strings"
	"testing"
)

func TestARM64PatchInstValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  uint64
		addend int64
		want   uint32
		bad    bool
	}{
		{"nop", 0xd503201f, 0, 0xd503201f, false},
		{"zero", 0, 0, 0, false}, {"max", math.MaxUint32, 0, math.MaxUint32, false},
		{"positive_addend", 0xd503201b, 4, 0xd503201f, false},
		{"negative_addend", 0xd5032023, -4, 0xd503201f, false},
		{"uint64_wrap", math.MaxUint64, 1, 0, false},
		{"wide_value", 1 << 32, 0, 0, true}, {"positive_overflow", math.MaxUint32, 1, 0, true},
		{"negative_result", 0, -1, 0, true}, {"high_bit", 1 << 63, 0, 0, true},
		{"wide_value_reduced", 1 << 32, -1, math.MaxUint32, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, im, r := patchInstImage()
			o.symbols[1] = symbol{section: -1, value: tc.value}
			r.addend = tc.addend
			err := im.relocate(o, r)
			if tc.bad {
				if err == nil || le.Uint32(im.mem) != 0x0b010000 {
					t.Fatal("invalid patch accepted or written", err)
				}
				return
			}
			if err != nil || le.Uint32(im.mem) != tc.want {
				t.Fatalf("word=%#x want=%#x error=%v", le.Uint32(im.mem), tc.want, err)
			}
		})
	}
}

func patchInstImage() (*object, *image, relocation) {
	o := &object{info: Info{Format: "ELF", Arch: "arm64"}, sections: []*section{nil, {size: 8}}, symbols: []symbol{{}, {name: "patch_word", global: true}}}
	im := &image{base: 0x10000000, mem: make([]byte, 8), defs: map[string]definition{}}
	le.PutUint32(im.mem, 0x0b010000)
	return o, im, relocation{section: 1, symbol: 1, typ: elfARM64PatchInst, pair: -1}
}

func TestARM64PatchInstDefinitions(t *testing.T) {
	for _, name := range []string{"undefined_strong", "undefined_weak", "null_symbol", "selected_absolute", "selected_zero", "local_absolute", "section_definition", "common_definition", "native_hook", "native_external", "owned_got", "tls_absolute", "tls_undefined", "non_elf_definition", "invalid_symbol"} {
		t.Run(name, func(t *testing.T) {
			o, im, r := patchInstImage()
			want := uint32(0x0b010000)
			bad := false
			provider := &object{info: Info{Format: "ELF", Arch: "arm64"}, symbols: []symbol{{name: "patch_word", global: true, section: -1, value: 0xd503201f}}}
			switch name {
			case "undefined_weak":
				o.symbols[1].weak = true
				r.addend = 42
			case "null_symbol":
				r.symbol = 0
				r.addend = 0xd503201f
			case "selected_absolute":
				im.defs["patch_word"] = definition{provider, 0}
				want = 0xd503201f
			case "selected_zero":
				provider.symbols[0].value = 0
				im.defs["patch_word"] = definition{provider, 0}
				want = 0
			case "local_absolute":
				o.symbols[1] = symbol{name: "patch_word", section: -1, value: 0xd503201f}
				im.defs["patch_word"] = definition{provider, 0}
				provider.symbols[0].value = 0
				want = 0xd503201f
			case "section_definition":
				o.symbols[1].section = 1
				bad = true
			case "common_definition":
				o.symbols[1].section = -2
				bad = true
			case "native_hook":
				im.hooks = map[string]uintptr{"patch_word": 0xd503201f}
				bad = true
			case "native_external":
				im.external = func(string) uintptr { return 0xd503201f }
				bad = true
			case "owned_got":
				o.symbols[1].name = "_GLOBAL_OFFSET_TABLE_"
				bad = true
			case "tls_absolute":
				o.symbols[1].section = -1
				o.symbols[1].tls = true
				bad = true
			case "tls_undefined":
				o.symbols[1].tls = true
				bad = true
			case "non_elf_definition":
				provider.info.Format = "COFF"
				im.defs["patch_word"] = definition{provider, 0}
				bad = true
			case "invalid_symbol":
				r.symbol = 2
				bad = true
			}
			err := im.relocate(o, r)
			if bad {
				if err == nil || le.Uint32(im.mem) != want {
					t.Fatal("invalid definition accepted or written", err)
				}
				return
			}
			if err != nil || le.Uint32(im.mem) != want {
				t.Fatalf("word=%#x want=%#x error=%v", le.Uint32(im.mem), want, err)
			}
		})
	}
}

func TestARM64PatchInstSites(t *testing.T) {
	for _, name := range []string{"unaligned_defined", "unaligned_undefined", "short_defined", "short_undefined", "invalid_section", "last_word"} {
		t.Run(name, func(t *testing.T) {
			o, im, r := patchInstImage()
			if !strings.HasSuffix(name, "undefined") {
				o.symbols[1].section = -1
				o.symbols[1].value = 0xd503201f
			}
			switch name {
			case "unaligned_defined", "unaligned_undefined":
				im.base++
			case "short_defined", "short_undefined":
				r.offset = 5
			case "invalid_section":
				r.section = 2
			case "last_word":
				r.offset = 4
			}
			before := append([]byte(nil), im.mem...)
			err := im.relocate(o, r)
			if name == "last_word" {
				if err != nil || le.Uint32(im.mem[4:]) != 0xd503201f {
					t.Fatal(err)
				}
				return
			}
			if err == nil || string(before) != string(im.mem) {
				t.Fatal("invalid site accepted or written", err)
			}
		})
	}
}

func TestARM64PatchInstOrder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		relocs []relocation
		bad    bool
	}{
		{"call_then_patch", []relocation{{typ: 283}, {typ: 316}}, false},
		{"patch_then_call", []relocation{{typ: 316}, {typ: 283}}, true},
		{"multiple_patches", []relocation{{typ: 283}, {typ: 316}, {typ: 316}}, false},
		{"interleaved_sites", []relocation{{typ: 316}, {typ: 283, offset: 4}, {typ: 316, offset: 4}}, false},
		{"other_section", []relocation{{typ: 316}, {typ: 283, section: 2}}, false},
		{"ignored_none", []relocation{{typ: 316}, {typ: 256}, {typ: 0}}, false},
		{"non_adjacent_violation", []relocation{{typ: 316}, {typ: 283, offset: 4}, {typ: 283}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := &object{info: Info{Format: "ELF", Arch: "arm64"}, relocs: tc.relocs}
			if err := validateELFARM64PatchOrder(o); (err != nil) != tc.bad {
				t.Fatal(err)
			}
		})
	}
}

func TestARM64PatchInstSequence(t *testing.T) {
	o, im, r := patchInstImage()
	o.symbols[1].section = -1
	o.symbols[1].value = 0xd503201f
	if err := im.relocate(o, r); err != nil {
		t.Fatal(err)
	}
	o.symbols = append(o.symbols, symbol{name: "second", global: true})
	r.symbol = 2
	r.addend = 42
	if err := im.relocate(o, r); err != nil || le.Uint32(im.mem) != 0xd503201f {
		t.Fatal("undefined patch changed previous result", err)
	}
	o.symbols[2].section = -1
	o.symbols[2].value = 0x4b010000
	r.addend = 0
	if err := im.relocate(o, r); err != nil || le.Uint32(im.mem) != 0x4b010000 {
		t.Fatal("second patch composed with previous word", err)
	}
}
