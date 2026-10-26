package dylib

import (
	"fmt"
	"strings"
)

func weakAliases(objects []*object) (map[string]definition, error) {
	aliases := make(map[string]definition)
	for _, o := range objects {
		for i, v := range o.symbols {
			if v.alias == nil {
				continue
			}
			if v.alias.target < 0 || v.alias.target >= len(o.symbols) {
				return nil, fmt.Errorf("invalid COFF weak fallback index")
			}
			if old, ok := aliases[v.name]; ok {
				a := old.sym().alias
				if a.search != v.alias.search || old.o.symbols[a.target].name != o.symbols[v.alias.target].name {
					return nil, fmt.Errorf("conflicting COFF weak fallbacks for %s", v.name)
				}
				continue
			}
			aliases[v.name] = definition{o, i}
		}
	}
	return aliases, nil
}

func (im *image) externalSymbol(o *object, name string) uintptr {
	if o.info.Format == "COFF" {
		name = strings.TrimPrefix(name, "__imp_")
	}
	if p := im.hooks[name]; p != 0 {
		return p
	}
	if im.external != nil {
		return im.external(name)
	}
	return 0
}

// resolveDefinition is shared by address, section-relative and size relocations.
// Mach-O forwarding definitions follow their target names. A strong definition
// or explicitly provided native symbol wins over a COFF weak fallback.
// Cycles are detected after archive selection has stabilized.
func (im *image) resolveDefinition(o *object, index int) (definition, error) {
	d, _, err := im.resolveAddressDefinition(o, index)
	return d, err
}

func (im *image) resolveAddressDefinition(o *object, index int) (definition, uint64, error) {
	var offset uint64
	seen := make(map[definition]bool)
	for {
		if index < 0 || index >= len(o.symbols) {
			return definition{}, 0, fmt.Errorf("invalid symbol index %d", index)
		}
		d := definition{o, index}
		v := d.sym()
		if v.global {
			if strong, ok := im.defs[v.name]; ok {
				d = strong
				o, index, v = d.o, d.index, d.sym()
			}
		}
		if v.forward != nil {
			if seen[d] {
				return definition{}, 0, fmt.Errorf("Mach-O indirect alias cycle at %s", v.name)
			}
			seen[d] = true
			if v.forward.offset > ^uint64(0)-offset {
				return definition{}, 0, fmt.Errorf("Mach-O alias offset overflow at %s", v.name)
			}
			offset += v.forward.offset
			index = v.forward.target
			continue
		}
		if offset != 0 && (v.tls || nativeTLSSection(o, v) != nil) {
			return definition{}, 0, fmt.Errorf("Mach-O offset alias cannot target native TLS %s", v.name)
		}
		if v.section != 0 || v.global && elfOwnedGOT(o.info, v.name) || im.externalSymbol(o, v.name) != 0 {
			return d, offset, nil
		}
		alias, ok := im.aliases[v.name]
		if !ok {
			return d, offset, nil
		}
		if seen[alias] {
			return definition{}, 0, fmt.Errorf("COFF weak alias cycle at %s", v.name)
		}
		seen[alias] = true
		o, index = alias.o, alias.sym().alias.target
	}
}

// SEARCH_LIBRARY tries archive definitions of the weak name before its
// fallback. NOLIBRARY and ALIAS may use an already selected strong definition,
// but do not extract archive members just to define that weak name.
func (im *image) weakLibraryNames(names []string) []string {
	var wanted []string
	seen := make(map[string]bool)
	for _, name := range names {
		for !seen[name] {
			seen[name] = true
			d, ok := im.aliases[name]
			if !ok {
				break
			}
			if _, defined := im.defs[name]; defined || im.externalSymbol(d.o, name) != 0 {
				break
			}
			a := d.sym().alias
			if a.search == 2 {
				wanted = append(wanted, name)
			}
			name = d.o.symbols[a.target].name
		}
	}
	return wanted
}
