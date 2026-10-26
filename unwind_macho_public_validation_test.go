package dylib

import (
	"strings"
	"testing"
)

// Force original frame PCs to name the independently replaceable keep alias,
// rather than relying on the assembler's choice of same-address spelling.
func machoPublicFrameAlias(t *testing.T, o *object, mode string) int {
	t.Helper()
	keep := -1
	for i, s := range o.symbols {
		if s.name == "coal_frame_keep" {
			keep = i
		}
	}
	if keep < 0 || o.symbols[keep].forward == nil {
		t.Fatal("missing public code alias")
	}
	changed := 0
	sectionIndex := 0
	for i, r := range o.relocs {
		s := o.sections[r.section]
		if s == nil || s.name != "__eh_frame" && s.name != "__compact_unwind" {
			continue
		}
		if mode == "compact" && s.name != "__compact_unwind" || mode == "dwarf" && s.name != "__eh_frame" {
			continue
		}
		for _, operand := range []*int{&r.symbol, &r.pair} {
			if *operand < 0 || *operand >= len(o.symbols) {
				continue
			}
			name := o.symbols[*operand].name
			if name == "coal_frame_keep" || name == "coal_frame_zkeep" {
				*operand = keep
				changed++
				sectionIndex = r.section
			}
		}
		o.relocs[i] = r
	}
	if changed == 0 {
		t.Fatal("no explicit alias PC producer")
	}
	return sectionIndex
}

func TestMachOPublicAliasFramePC(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		for _, mode := range []string{"dwarf", "compact"} {
			// AMD64 DWARF PCs are implicit; the native exception suite covers
			// their retained storage. Test explicit rebinding only here.
			if target[0] == 'x' && mode == "dwarf" {
				continue
			}
			t.Run(target+"/"+mode, func(t *testing.T) {
				_, o := machoCoalescedFrames(t, target, "-O0", mode, false, "-DDYLIB_FRAME_PUBLIC_ALIASES")
				index := machoPublicFrameAlias(t, o, mode)
				before := append([]relocation(nil), o.relocs...)
				if err := o.canonicalizeMachOAliasFrames(); err != nil {
					t.Fatal(err)
				}
				changed := 0
				for i, r := range o.relocs {
					if r.section == index && (r.symbol != before[i].symbol || r.pair != before[i].pair) {
						changed++
					}
				}
				if changed == 0 {
					t.Fatal("independently overridden frame alias was not rebound to its storage owner")
				}
			})
		}
	}
}

func TestMachOPublicAliasFrameValidation(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		for _, failure := range []string{"short_storage", "length", "writable", "records_budget"} {
			t.Run(target+"/"+failure, func(t *testing.T) {
				_, o := machoCoalescedFrames(t, target, "-O0", "dwarf", false, "-DDYLIB_FRAME_PUBLIC_ALIASES")
				index := 0
				keep := -1
				for i, s := range o.sections {
					if s != nil && s.name == "__eh_frame" {
						index = i
					}
				}
				for i, s := range o.symbols {
					if s.name == "coal_frame_keep" {
						keep = i
					}
				}
				// A synthetic explicit PC forces validation on both architectures;
				// malformed metadata is rejected before any relocation is published.
				o.relocs = append(o.relocs, relocation{section: index, offset: 16, symbol: keep, pair: -1, width: 4, typ: 1, pcrel: true})
				s := o.sections[index]
				s.data = append([]byte(nil), s.data...)
				switch failure {
				case "short_storage":
					s.data = s.data[:len(s.data)-1]
				case "length":
					le.PutUint32(s.data, 0xffffffff)
				case "writable":
					s.write = true
				case "records_budget":
					s.data = make([]byte, 8*65537)
					s.size = uint64(len(s.data))
					for i := 0; i < len(s.data); i += 8 {
						le.PutUint32(s.data[i:], 4)
					}
				}
				err := o.canonicalizeMachOAliasFrames()
				if err == nil {
					t.Fatal("invalid aliased frame storage accepted")
				}
				if failure == "records_budget" && !strings.Contains(err.Error(), "too many coalesced unwind records") {
					t.Fatal("record budget was not checked before scratch allocation", err)
				}
			})
		}
	}
}
