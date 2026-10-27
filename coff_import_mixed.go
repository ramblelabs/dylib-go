package dylib

import "strings"

// Recognize the existing import-only template in isolation, then retain all
// ordinary sections and their original symbol indices. Only template sections
// may disappear; no code, data or lifecycle section is treated as metadata.
func (g coffImportGraph) convertMixed(o *object) (*object, bool) {
	if o.info.Format != "COFF" {
		return nil, false
	}
	template := make(map[int]bool)
	mixed := len(o.groups) != 0
	view := *o
	view.sections = append([]*section(nil), o.sections...)
	for i, sec := range o.sections {
		if sec == nil {
			continue
		}
		switch sec.name {
		case ".text", ".idata$4", ".idata$5", ".idata$6", ".idata$7":
			template[i] = true
		default:
			if strings.HasPrefix(sec.name, ".idata") {
				return nil, false
			}
			view.sections[i] = nil
			mixed = mixed || sec.size != 0
		}
	}
	if !mixed {
		return nil, false
	}
	// COFF groups contain one original section. Only groups wholly outside
	// the removed template may survive, including their associative parents.
	retained := func(index int) bool {
		return index > 0 && index < len(view.sections) && view.sections[index] == nil && o.sections[index] != nil
	}
	for _, group := range o.groups {
		if len(group.sections) != 1 || !retained(group.sections[0]) || group.selection == 5 && !retained(group.parent) {
			return nil, false
		}
	}
	view.groups = nil
	view.symbols = append([]symbol(nil), o.symbols...)
	for i, s := range view.symbols {
		if !template[s.section] {
			view.symbols[i].global = false
		}
	}
	view.relocs = nil
	for _, r := range o.relocs {
		if template[r.section] {
			view.relocs = append(view.relocs, r)
		}
	}
	converted, ok := g.convertPure(&view)
	if !ok {
		return nil, false
	}
	return overlayCOFFImports(o, template, converted)
}

// Preserve original section/symbol indices and ordinary COMDAT/lifecycle data.
// Incoming references may use public import identities, never removed helpers.
func overlayCOFFImports(o *object, template map[int]bool, converted *object) (*object, bool) {
	imports := make(map[string]symbol)
	for _, s := range converted.symbols {
		imports[s.name] = s
	}
	copy := *o
	copy.sections = append([]*section(nil), o.sections...)
	for i := range template {
		copy.sections[i] = nil
	}
	copy.symbols = append([]symbol(nil), o.symbols...)
	for i, s := range o.symbols {
		if !template[s.section] {
			continue
		}
		if imported, ok := imports[s.name]; ok && s.global {
			copy.symbols[i] = imported
		} else {
			copy.symbols[i] = symbol{section: -3}
		}
	}
	copy.relocs = nil
	for _, r := range o.relocs {
		if template[r.section] {
			continue
		}
		if r.section <= 0 || r.section >= len(copy.sections) || copy.sections[r.section] == nil || r.symbol < 0 || r.symbol >= len(copy.symbols) || copy.symbols[r.symbol].section == -3 {
			return nil, false
		}
		copy.relocs = append(copy.relocs, r)
	}
	copy.info.Symbols = nil
	copy.info.Imports = converted.info.Imports
	copy.info.Unsupported = converted.info.Unsupported
	return &copy, true
}
