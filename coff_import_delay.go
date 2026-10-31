package dylib

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/ramblelabs/dylib-go/internal/coffname"
)

// DelayImportMode controls recognized raw COFF delay-import templates.
// Complete OS libraries continue to use their native delay helpers.
type DelayImportMode uint8

const (
	// DelayImportsDisabled rejects selected raw delay imports before opening DLLs.
	DelayImportsDisabled DelayImportMode = iota
	// DelayImportsEager resolves imports at Link, before any native initializer.
	// It bypasses lazy helper hooks and retains providers through Session.Close.
	DelayImportsEager
	// DelayImportsLazy installs image-owned native entries and resolves on first
	// call. Supports recognized raw templates on Windows amd64/arm64/386 hosts.
	// Go hooks are separate from native __pfnDli*Hook2 variables.
	DelayImportsLazy
)

func checkCOFFDelayImports(objects []*object, mode DelayImportMode) error {
	if mode != DelayImportsDisabled && mode != DelayImportsEager && mode != DelayImportsLazy {
		return fmt.Errorf("invalid delay-import mode %d", mode)
	}
	for _, o := range objects {
		for _, s := range o.symbols {
			if s.imported != nil && s.imported.entry.delay && mode == DelayImportsDisabled {
				return fmt.Errorf("%s: raw COFF delay imports require Options.DelayImports = DelayImportsEager or DelayImportsLazy", o.info.Name)
			}
		}
	}
	return nil
}

func delayRelocations(arch string) (rva, absolute, branch uint32) {
	switch arch {
	case "amd64":
		return 3, 1, 4
	case "386":
		return 7, 6, 20
	case "arm64":
		return 2, 14, 3
	}
	return
}

// A bounded relocation reference, including the absolute fallback pointers in
// a delay IAT. It does not interpret a cached address as a lookup identity.
func (g coffImportGraph) delayReference(o *object, section int, offset uint64, typ uint32, width uint64) (coffLocation, bool) {
	if typ == 0 || width != 0 && width != 4 && width != 8 || section <= 0 || section >= len(o.sections) || o.sections[section] == nil {
		return coffLocation{}, false
	}
	var found *relocation
	for i := range o.relocs {
		r := &o.relocs[i]
		if r.section == section && r.offset == offset {
			if found != nil || r.typ != typ || r.local || r.pair >= 0 || r.addend != 0 {
				return coffLocation{}, false
			}
			found = r
		}
	}
	if found == nil || found.symbol < 0 || found.symbol >= len(o.symbols) {
		return coffLocation{}, false
	}
	s := o.symbols[found.symbol]
	provider := o
	if s.weak || s.alias != nil || s.forward != nil {
		return coffLocation{}, false
	}
	if s.section == 0 {
		if !s.global {
			return coffLocation{}, false
		}
		defs := g.defs[o.info.Arch+"\x00"+s.name]
		if len(defs) != 1 {
			return coffLocation{}, false
		}
		provider, s = defs[0].o, defs[0].sym()
	}
	if provider.info.Bits != o.info.Bits || s.section <= 0 || s.section >= len(provider.sections) || provider.sections[s.section] == nil || s.weak || s.alias != nil || s.forward != nil {
		return coffLocation{}, false
	}
	b := o.sections[section].data
	instruction := width == 0
	if instruction {
		width = 4
	}
	if offset > uint64(len(b)) || width > uint64(len(b))-offset {
		return coffLocation{}, false
	}
	value := uint64(le.Uint32(b[offset:]))
	if instruction {
		w := uint32(value)
		if o.info.Arch != "arm64" || typ == 4 && (w&0x9f000000 != 0x90000000 || w&0x60ffffe0 != 0) || typ == 6 && (w&0xffc00000 != 0x91000000 || w&0x003ffc00 != 0) || typ != 4 && typ != 6 {
			return coffLocation{}, false
		}
		value = 0
	} else if width == 8 {
		value = le.Uint64(b[offset:])
	}
	size := provider.sections[s.section].size
	if value > size || s.value > size-value {
		return coffLocation{}, false
	}
	return coffLocation{provider, s.section, s.value + value}, true
}

type coffDelayImports struct {
	objects   []*object
	prepared  bool
	converted map[*object]*object
	helpers   map[*object]bool
}

func (d *coffDelayImports) prepare(g coffImportGraph) {
	if d.prepared {
		return
	}
	d.prepared = true
	d.converted = make(map[*object]*object)
	d.helpers = make(map[*object]bool)
	for _, o := range d.objects {
		if o.info.Format == "COFF" && coffImportCandidate(o) {
			if out, ok := g.convertDelay(o, d.helpers); ok {
				d.converted[o] = out
				continue
			}
			owners := make(map[*object]bool)
			out, ok := g.convertMixedUsing(o, func(view *object) (*object, bool) {
				return g.convertDelay(view, owners)
			})
			if ok {
				d.converted[o] = out
				for owner := range owners {
					d.helpers[owner] = true
				}
			}
		}
	}
}

func (g coffImportGraph) convertDelay(o *object, owners map[*object]bool) (*object, bool) {
	indexes, ok := coffImportSections(o)
	rva, absolute, branch := delayRelocations(o.info.Arch)
	width := uint64(o.info.Bits / 8)
	text, iat, ilt, hint := indexes[".text"], indexes[".idata$5"], indexes[".idata$4"], indexes[".idata$6"]
	if !ok || rva == 0 || text == 0 || iat == 0 || ilt == 0 || width != 4 && width != 8 {
		return nil, false
	}
	for _, sec := range o.sections {
		if sec != nil && (uint64(len(sec.data)) != sec.size || sec.lifecycle != 0 || sec.nativeTLS != 0) {
			return nil, false
		}
	}
	if !o.sections[text].exec {
		return nil, false
	}
	for index, sec := range o.sections {
		if sec != nil && index != text && sec.exec && sec.size != 0 {
			return nil, false
		}
	}
	if o.sections[iat].size != width || o.sections[ilt].size != width || indexes[".idata$7"] != 0 && o.sections[indexes[".idata$7"]].size != 0 {
		return nil, false
	}
	public, ok := coffSingleImportName(o, iat)
	if !ok {
		return nil, false
	}
	body := o.sections[text].data
	var expected []byte
	var sites []uint64
	var types []uint32
	var fallback, tail uint64
	switch o.info.Arch {
	case "amd64":
		expected = []byte{0xff, 0x25, 0, 0, 0, 0, 0x48, 0x8d, 0x05, 0, 0, 0, 0, 0xe9, 0, 0, 0, 0}
		sites, types, fallback, tail = []uint64{2, 9, 14}, []uint32{branch, branch, branch}, 6, 14
	case "386":
		expected = []byte{0xff, 0x25, 0, 0, 0, 0, 0xb8, 0, 0, 0, 0, 0xe9, 0, 0, 0, 0}
		sites, types, fallback, tail = []uint64{2, 7, 12}, []uint32{absolute, absolute, branch}, 6, 12
	case "arm64":
		expected = make([]byte, 28)
		for i, w := range []uint32{0x90000010, 0x91000210, 0xf9400210, 0xd61f0200, 0x90000010, 0x91000210, 0x14000000} {
			le.PutUint32(expected[i*4:], w)
		}
		sites, types, fallback, tail = []uint64{0, 4, 16, 20, 24}, []uint32{4, 6, 4, 6, branch}, 16, 24
	}
	if len(body) < len(expected) || len(body)-len(expected) > 4 || !bytes.Equal(body[:len(expected)], expected) || !allZero(body[len(expected):]) {
		return nil, false
	}
	var helper string
	for i, site := range sites {
		if site == tail {
			for _, r := range o.relocs {
				if r.section == text && r.offset == site && r.symbol >= 0 && r.symbol < len(o.symbols) && r.typ == branch && !r.local && r.pair < 0 && r.addend == 0 {
					s := o.symbols[r.symbol]
					if s.section == 0 && s.global && s.alias == nil && !s.weak {
						helper = s.name
					}
				}
			}
			continue
		}
		encodedWidth := uint64(4)
		if o.info.Arch == "arm64" {
			encodedWidth = 0
		}
		p, yes := g.delayReference(o, text, site, types[i], encodedWidth)
		if !yes || p != (coffLocation{o, iat, 0}) {
			return nil, false
		}
	}
	if !strings.HasPrefix(helper, "__tailMerge_") {
		return nil, false
	}
	p, ok := g.delayReference(o, iat, 0, absolute, width)
	if !ok || p != (coffLocation{o, text, fallback}) {
		return nil, false
	}
	dll, ok := g.delayDLL(o, helper)
	if !ok {
		return nil, false
	}
	entry := &coffImport{dll: dll, public: public, delay: true}
	word := uint64(le.Uint32(o.sections[ilt].data))
	if width == 8 {
		word = le.Uint64(o.sections[ilt].data)
	}
	flag := uint64(1) << (width*8 - 1)
	named := word&flag == 0
	if !named {
		if word & ^(flag|0xffff) != 0 || uint16(word) == 0 || hint != 0 && o.sections[hint].size != 0 {
			return nil, false
		}
		entry.ordinal = uint16(word)
	} else {
		p, yes := g.delayReference(o, ilt, 0, rva, 4)
		if !yes || p.o != o || p.section != hint || p.offset != 0 || width == 8 && le.Uint32(o.sections[ilt].data[4:]) != 0 {
			return nil, false
		}
		entry.name, ok = p.name(2)
		if !ok {
			return nil, false
		}
		if !allZero(o.sections[hint].data[2+len(entry.name):]) {
			return nil, false
		}
	}
	want := len(sites) + 1
	if named {
		want++
	}
	if len(o.relocs) != want {
		return nil, false
	}
	seen := make(map[coffImportSite]bool)
	for _, r := range o.relocs {
		key := coffImportSite{r.section, r.offset}
		if seen[key] {
			return nil, false
		}
		seen[key] = true
	}
	copy := *o
	copy.sections, copy.relocs, copy.groups, copy.symbols = nil, nil, nil, nil
	thunks := 0
	for _, s := range o.symbols {
		if !s.global {
			continue
		}
		if s.alias != nil || s.weak || s.section == -1 || s.section == -2 {
			return nil, false
		}
		if s.section <= 0 {
			if s.section == 0 && s.name != helper {
				return nil, false
			}
			continue
		}
		if s.value != 0 || s.section != iat && (s.section != text || s.name != public) || s.section == iat && s.name != "__imp_"+public {
			return nil, false
		}
		if s.section == text {
			thunks++
		}
		copy.symbols = append(copy.symbols, symbol{name: s.name, section: -4, global: true, imported: &coffImportSymbol{entry: entry, indirect: s.section == iat}})
	}
	if thunks != 1 {
		return nil, false
	}
	copy.info.Symbols, copy.info.Unsupported = nil, nil
	for _, u := range o.info.Unsupported {
		if !strings.HasPrefix(u, "COFF long import tables:") {
			return nil, false
		}
	}
	copy.info.Imports = []ImportInfo{{DLL: dll, Symbol: public, Name: entry.name, Ordinal: entry.ordinal, Kind: "code", Delay: true}}
	head := g.defs[o.info.Arch+"\x00"+helper][0].o
	for index, sec := range head.sections {
		if sec != nil && sec.name == ".text$2" {
			name, _ := g.delayReference(head, index, 4, rva, 4) // Validated by delayDLL.
			owners[head], owners[name.o] = true, true
		}
	}
	return &copy, true
}

// The shared resolver is metadata-only under eager policy. It may define only
// the tail-merge entry and descriptor, with no ordinary code/data/init exports.
// Its hooks and module cache are deliberately bypassed, never executed.
func (g coffImportGraph) delayDLL(stub *object, helper string) (string, bool) {
	defs := g.defs[stub.info.Arch+"\x00"+helper]
	if len(defs) != 1 {
		return "", false
	}
	d := defs[0]
	o := d.o
	if len(o.groups) != 0 || o.info.Bits != stub.info.Bits || d.sym().value != 0 {
		return "", false
	}
	indexes := make(map[string]int)
	for i, s := range o.sections {
		if s == nil {
			continue
		}
		if indexes[s.name] != 0 || uint64(len(s.data)) != s.size || s.lifecycle != 0 || s.nativeTLS != 0 {
			return "", false
		}
		indexes[s.name] = i
		if s.size == 0 {
			continue
		}
		switch s.name {
		case ".text":
			if s.size > 512 {
				return "", false
			}
		case ".text$2":
			if s.size != 32 {
				return "", false
			}
		case ".data":
			if s.size > uint64(o.info.Bits/4) || !allZero(s.data) {
				return "", false
			}
		case ".idata$4", ".idata$5":
			if s.size != uint64(o.info.Bits/8) || !allZero(s.data) {
				return "", false
			}
		case ".pdata", ".xdata":
			if s.size > 64 {
				return "", false
			}
		default:
			return "", false
		}
	}
	text, descriptor, data := indexes[".text"], indexes[".text$2"], indexes[".data"]
	if text == 0 || descriptor == 0 || data == 0 || o.sections[data].size < uint64(o.info.Bits/8) || d.sym().section != text {
		return "", false
	}
	b := o.sections[descriptor].data
	if le.Uint32(b) != 1 || !allZero(b[20:]) {
		return "", false
	}
	rva, absolute, branch := delayRelocations(o.info.Arch)
	name, ok := g.delayReference(o, descriptor, 4, rva, 4)
	if !ok || name.offset != 0 || !validCOFFDelayNameProvider(name, coffDelaySymbolName(o, descriptor, 4)) {
		return "", false
	}
	dll, ok := name.name(0)
	if !ok {
		return "", false
	}
	dll, err := coffDLLName(dll)
	if err != nil {
		return "", false
	}
	for i, section := range []int{data, indexes[".idata$5"], indexes[".idata$4"]} {
		p, yes := g.delayReference(o, descriptor, uint64(8+i*4), rva, 4)
		offset := uint64(o.info.Bits / 8)
		if i == 0 {
			offset = 0
		}
		if !yes || section == 0 || p != (coffLocation{o, section, offset}) {
			return "", false
		}
	}
	external := "__delayLoadHelper2"
	descName := "__DELAY_IMPORT_DESCRIPTOR_" + strings.TrimPrefix(helper, "__tailMerge_")
	if o.info.Arch == "386" {
		external = coffname.NormalizeI386("___delayLoadHelper2@8")
		descName = coffname.NormalizeI386(descName)
	}
	for _, s := range o.symbols {
		if !s.global {
			continue
		}
		if s.alias != nil || s.weak || s.section == -1 || s.section == -2 {
			return "", false
		}
		if s.section > 0 && (s.name != helper || s.section != text || s.value != 0) && (s.name != descName || s.section != descriptor || s.value != 0) {
			return "", false
		}
		if s.section == 0 && s.name != external && s.name != coffDelaySymbolName(o, descriptor, 4) {
			return "", false
		}
	}
	seen := make(map[coffImportSite]bool)
	codeRefs, helperCalls := 0, 0
	for _, r := range o.relocs {
		key := coffImportSite{r.section, r.offset}
		if seen[key] || r.symbol < 0 || r.symbol >= len(o.symbols) || r.local || r.pair >= 0 || r.addend != 0 || r.section <= 0 || r.section >= len(o.sections) || o.sections[r.section] == nil || r.offset > o.sections[r.section].size || o.sections[r.section].size-r.offset < 4 {
			return "", false
		}
		seen[key] = true
		switch r.section {
		case descriptor:
			if r.typ != rva || r.offset != 4 && r.offset != 8 && r.offset != 12 && r.offset != 16 {
				return "", false
			}
		case text:
			s := o.symbols[r.symbol]
			if s.section == 0 && s.name == external && r.typ == branch {
				helperCalls++
				continue
			}
			typ := branch
			if o.info.Arch == "386" {
				typ = absolute
			}
			if o.info.Arch == "arm64" {
				typ = r.typ
				if typ != 4 && typ != 6 {
					return "", false
				}
			}
			encodedWidth := uint64(4)
			if o.info.Arch == "arm64" {
				encodedWidth = 0
			}
			p, yes := g.delayReference(o, text, r.offset, typ, encodedWidth)
			if !yes || p != (coffLocation{o, descriptor, 0}) {
				return "", false
			}
			codeRefs++
		case indexes[".pdata"]:
			if r.typ != rva || r.offset != 0 && r.offset != 4 && r.offset != 8 {
				return "", false
			}
			p, yes := g.delayReference(o, r.section, r.offset, rva, 4)
			if !yes || p.o != o || r.offset < 8 && p.section != text || r.offset == 8 && p.section != indexes[".xdata"] {
				return "", false
			}
		default:
			return "", false
		}
	}
	want := 1
	if o.info.Arch == "arm64" {
		want = 2
	}
	if codeRefs != want || helperCalls != 1 {
		return "", false
	}
	for _, u := range o.info.Unsupported {
		if !strings.HasPrefix(u, "COFF long import tables:") {
			return "", false
		}
	}
	return dll, true
}

func coffDelaySymbolName(o *object, section int, offset uint64) string {
	for _, r := range o.relocs {
		if r.section == section && r.offset == offset && r.symbol >= 0 && r.symbol < len(o.symbols) {
			return o.symbols[r.symbol].name
		}
	}
	return ""
}

// Consuming a name helper must not erase independent data exports, TLS or a
// second directory. The broader ordinary-import helper validator does not own
// those symbols and therefore cannot establish this stronger condition alone.
func validCOFFDelayNameProvider(name coffLocation, public string) bool {
	o := name.o
	if !validCOFFImportHelper(o) || len(o.relocs) != 0 || name.section <= 0 || o.sections[name.section].name != ".idata$7" {
		return false
	}
	seen := make(map[string]bool)
	for _, sec := range o.sections {
		if sec == nil {
			continue
		}
		if uint64(len(sec.data)) != sec.size || sec.nativeTLS != 0 || sec.size != 0 && (seen[sec.name] || sec.name != ".idata$4" && sec.name != ".idata$5" && sec.name != ".idata$7") {
			return false
		}
		if sec.size != 0 {
			seen[sec.name] = true
		}
	}
	count := 0
	for _, s := range o.symbols {
		if s.global {
			if s.name != public || s.section != name.section || s.value != 0 || s.weak || s.alias != nil || s.forward != nil {
				return false
			}
			count++
		}
	}
	return count == 1
}
