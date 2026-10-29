package dylib

import (
	"sort"
	"strings"
)

// Only explicit metadata references join objects. Ordinary consumers and
// unrelated archive members never join a directory by name or input order.
type coffImportComponents struct {
	objects   []*object
	prepared  bool
	converted map[*object]*object
}

// Resolve explicit metadata references across all staged inputs before archive
// selection. The returned views retain each member's original origin/order;
// staged snapshots and unrelated members remain unchanged and unselected.
func prepareCOFFImportInputs(objects, archives []*object) ([]*object, []*object) {
	all := make([]*object, 0, len(objects)+len(archives))
	all = append(all, objects...)
	all = append(all, archives...)
	// Raw roots and archive candidates are stored separately for selection,
	// but descriptor contributions must validate in their original input order.
	ordered := append([]*object(nil), all...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.inputOrder != b.inputOrder {
			return a.inputOrder < b.inputOrder
		}
		return a.memberOrder < b.memberOrder
	})
	graph := newCOFFImportGraph(ordered)
	for i, o := range all {
		if converted, ok := graph.convert(o); ok {
			all[i] = converted
		}
	}
	// Selection appends members to roots. Keep that append from overwriting
	// unselected archive candidates sharing the temporary backing array.
	return all[:len(objects):len(objects)], all[len(objects):]
}

func (c *coffImportComponents) prepare(g coffImportGraph) {
	if c.prepared {
		return
	}
	c.prepared = true
	c.converted = make(map[*object]*object)
	indexes := make(map[*object]int)
	parents := make([]int, len(c.objects))
	for i, o := range c.objects {
		indexes[o], parents[i] = i, i
	}
	root := func(i int) int {
		for parents[i] != i {
			parents[i] = parents[parents[i]]
			i = parents[i]
		}
		return i
	}
	for i, o := range c.objects {
		if o.info.Format != "COFF" {
			continue
		}
		for _, r := range o.relocs {
			if r.section <= 0 || r.section >= len(o.sections) || o.sections[r.section] == nil || !strings.HasPrefix(o.sections[r.section].name, ".idata$") || r.symbol < 0 || r.symbol >= len(o.symbols) {
				continue
			}
			s := o.symbols[r.symbol]
			if s.section != 0 || !s.global || s.alias != nil {
				continue
			}
			defs := g.defs[o.info.Arch+"\x00"+s.name]
			if len(defs) != 1 {
				continue
			}
			if j, ok := indexes[defs[0].o]; ok {
				parents[root(i)] = root(j)
			}
		}
	}
	groups := make(map[int][]*object)
	for i, o := range c.objects {
		groups[root(i)] = append(groups[root(i)], o)
	}
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		if converted, ok := g.composeDirectory(group); ok {
			for i, o := range group {
				c.converted[o] = converted[i]
			}
		}
	}
}

type coffImportPlacement struct {
	sections   []int
	symbols    int
	textOffset uint64
}

// Validate a temporary joined view with the existing directory decoder, then
// project its import identities onto the original section/symbol indices.
// Only the recognized thunk text is concatenated; ordinary sections retain
// their boundaries, COMDAT associations and lifecycle records.
func (g coffImportGraph) composeDirectory(inputs []*object) ([]*object, bool) {
	view := &object{info: inputs[0].info, sections: []*section{nil}}
	view.info.Imports, view.info.Unsupported, view.info.Symbols = nil, nil, nil
	placements := make(map[*object]coffImportPlacement)
	text := &section{name: ".text", exec: true}
	view.sections = append(view.sections, text)
	budget := uint64(maxFile)
	for _, o := range inputs {
		if o.info.Format != "COFF" || o.info.Arch != view.info.Arch || o.info.Bits != view.info.Bits {
			return nil, false
		}
		cost := uint64(len(o.symbols))*64 + uint64(len(o.sections))*64 + uint64(len(o.relocs))*64
		for _, s := range o.symbols {
			cost += uint64(len(s.name))
		}
		if cost > budget {
			return nil, false
		}
		budget -= cost
		p := coffImportPlacement{sections: make([]int, len(o.sections)), symbols: len(view.symbols), textOffset: text.size}
		textSeen := false
		for i, s := range o.sections {
			if s == nil {
				continue
			}
			if uint64(len(s.data)) > budget {
				return nil, false
			}
			budget -= uint64(len(s.data))
			if s.name == ".text" {
				if textSeen || uint64(len(s.data)) != s.size || s.lifecycle != 0 || s.nativeTLS != 0 {
					return nil, false
				}
				textSeen = true
				p.sections[i] = 1
				text.data = append(text.data, s.data...)
				text.size += s.size
			} else {
				p.sections[i] = len(view.sections)
				view.sections = append(view.sections, s)
			}
		}
		for _, s := range o.symbols {
			if s.section > 0 {
				if s.section >= len(p.sections) || p.sections[s.section] == 0 {
					return nil, false
				}
				if o.sections[s.section].name == ".text" {
					if s.global && s.value >= o.sections[s.section].size {
						return nil, false
					}
					s.value += p.textOffset
				}
				s.section = p.sections[s.section]
			}
			view.symbols = append(view.symbols, s)
		}
		placements[o] = p
	}
	for _, o := range inputs {
		p := placements[o]
		for _, r := range o.relocs {
			if r.section <= 0 || r.section >= len(p.sections) || p.sections[r.section] == 0 || r.symbol < 0 || r.symbol >= len(o.symbols) || r.pair >= 0 {
				return nil, false
			}
			s := o.symbols[r.symbol]
			r.symbol += p.symbols
			// Resolve references only to metadata/thunks inside this component.
			// This also lets the overlay reject ordinary references to helpers.
			if s.section == 0 && s.global && s.alias == nil {
				defs := g.defs[o.info.Arch+"\x00"+s.name]
				if len(defs) == 1 {
					d := defs[0]
					if q, ok := placements[d.o]; ok {
						v := d.sym()
						if v.section > 0 && v.section < len(q.sections) && d.o.sections[v.section] != nil && coffImportTemplate(d.o.sections[v.section].name) {
							r.symbol = q.symbols + d.index
						}
					}
				}
			}
			if o.sections[r.section].name == ".text" {
				r.offset += p.textOffset
			}
			r.section = p.sections[r.section]
			view.relocs = append(view.relocs, r)
		}
		for _, group := range o.groups {
			copy := group
			copy.sections = nil
			for _, i := range group.sections {
				if i <= 0 || i >= len(p.sections) || p.sections[i] == 0 {
					return nil, false
				}
				copy.sections = append(copy.sections, p.sections[i])
			}
			if group.parent != 0 {
				if group.parent < 0 || group.parent >= len(p.sections) {
					return nil, false
				}
				copy.parent = p.sections[group.parent]
			}
			view.groups = append(view.groups, copy)
		}
	}
	out, ok := g.convertDirectory(view)
	if !ok {
		return nil, false
	}
	var result []*object
	for _, o := range inputs {
		p := placements[o]
		normalized := &object{}
		names := make(map[string]bool)
		for i := range o.symbols {
			s := out.symbols[p.symbols+i]
			if s.imported != nil {
				normalized.symbols = append(normalized.symbols, s)
				names[s.imported.entry.public] = true
			}
		}
		for _, info := range out.info.Imports {
			if names[info.Symbol] {
				normalized.info.Imports = append(normalized.info.Imports, info)
			}
		}
		for _, unsupported := range o.info.Unsupported {
			if !strings.HasPrefix(unsupported, "COFF long import tables:") {
				normalized.info.Unsupported = append(normalized.info.Unsupported, unsupported)
			}
		}
		template := make(map[int]bool)
		for i, s := range o.sections {
			if s != nil && coffImportTemplate(s.name) {
				template[i] = true
			}
		}
		projected, ok := overlayCOFFImports(o, template, normalized)
		if !ok {
			return nil, false
		}
		result = append(result, projected)
	}
	for _, o := range result {
		o.coffImportGroup = result
	}
	return result, true
}

func coffImportTemplate(name string) bool {
	switch name {
	case ".text", ".idata$2", ".idata$3", ".idata$4", ".idata$5", ".idata$6", ".idata$7":
		return true
	}
	return false
}

// Incomplete raw COFF imports can be staged before their metadata providers.
// All diagnostics are checked again after composition and before opening DLLs.
func checkCOFFImportStage(o *object) error {
	if o.info.Format != "COFF" {
		return checkTarget(o)
	}
	copy := *o
	copy.info.Unsupported = nil
	for _, unsupported := range o.info.Unsupported {
		name := strings.TrimPrefix(unsupported, "COFF long import tables: ")
		if name == unsupported || !coffImportTemplate(name) {
			copy.info.Unsupported = append(copy.info.Unsupported, unsupported)
		}
	}
	return checkTarget(&copy)
}

func expandCOFFImportGroups(inputs []*object, used map[*object]bool) []*object {
	seen := make(map[*object]bool)
	for _, o := range inputs {
		seen[o] = true
	}
	for i := 0; i < len(inputs); i++ {
		o := inputs[i]
		used[o] = true
		for _, member := range o.coffImportGroup {
			if !seen[member] {
				seen[member] = true
				inputs = append(inputs, member)
			}
		}
	}
	return inputs
}
