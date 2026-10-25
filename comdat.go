package dylib

import (
	"bytes"
	"fmt"
)

// coalesceObjects snapshots a link attempt and removes duplicate COMDAT groups
// before dependency discovery. References in discarded groups must not pull in
// archives or fail resolution. Parsed inputs remain intact for link retries.
func coalesceObjects(inputs []*object) ([]*object, error) {
	type groupRef struct {
		object *object
		index  int
	}
	winners := make(map[string]groupRef)
	objects := make([]*object, len(inputs))
	for i, input := range inputs {
		o := *input
		o.symbols = append([]symbol(nil), input.symbols...)
		o.sections = make([]*section, len(input.sections))
		for j, sec := range input.sections {
			if sec != nil {
				c := *sec
				o.sections[j] = &c
			}
		}
		objects[i] = &o
		for j, g := range o.groups {
			if g.selection == 5 {
				continue
			}
			key := o.info.Format + "\x00" + g.key
			old, exists := winners[key]
			if !exists {
				winners[key] = groupRef{&o, j}
				continue
			}
			previous := old.object.groups[old.index]
			if previous.selection != g.selection {
				return nil, fmt.Errorf("COMDAT %s has conflicting selection rules", g.key)
			}
			switch g.selection {
			case 1:
				return nil, fmt.Errorf("duplicate NODUPLICATES COMDAT %s", g.key)
			case 2: // Deterministically retain the first definition.
			case 3:
				if groupSize(&o, g) != groupSize(old.object, previous) {
					return nil, fmt.Errorf("COMDAT %s has different sizes", g.key)
				}
			case 4:
				if !sameGroupContents(&o, g, old.object, previous) {
					return nil, fmt.Errorf("EXACT_MATCH COMDAT %s has different contents", g.key)
				}
			case 6:
				if groupSize(&o, g) > groupSize(old.object, previous) {
					winners[key] = groupRef{&o, j}
				}
			case 7:
				if o.timestamp > old.object.timestamp {
					winners[key] = groupRef{&o, j}
				}
			default:
				return nil, fmt.Errorf("unsupported COMDAT selection %d", g.selection)
			}
		}
	}
	for _, o := range objects {
		bySection := make(map[int]int)
		for i, g := range o.groups {
			for _, section := range g.sections {
				bySection[section] = i
			}
		}
		state := make([]uint8, len(o.groups)) // visiting, retained, discarded.
		var keep func(int) (bool, error)
		keep = func(i int) (bool, error) {
			if state[i] == 1 {
				return false, fmt.Errorf("associative COMDAT dependency cycle")
			}
			if state[i] != 0 {
				return state[i] == 2, nil
			}
			state[i] = 1
			g := o.groups[i]
			retained := false
			if g.selection == 5 {
				parent, ok := bySection[g.parent]
				if !ok {
					return false, fmt.Errorf("missing associative COMDAT parent")
				}
				var err error
				retained, err = keep(parent)
				if err != nil {
					return false, err
				}
			} else {
				winner := winners[o.info.Format+"\x00"+g.key]
				retained = winner.object == o && winner.index == i
			}
			state[i] = 3
			if retained {
				state[i] = 2
			}
			return retained, nil
		}
		discarded := make(map[int]bool)
		for i, g := range o.groups {
			retained, err := keep(i)
			if err != nil {
				return nil, err
			}
			if !retained {
				for _, section := range g.sections {
					discarded[section] = true
					o.sections[section] = nil
				}
			}
		}
		if err := o.pruneELFFrames(discarded); err != nil {
			return nil, err
		}
		for i := range o.symbols {
			s := &o.symbols[i]
			if discarded[s.section] {
				s.section = -3
				if s.global {
					s.section = 0
					s.value = 0
				}
			}
		}
		relocs := make([]relocation, 0, len(o.relocs))
		for _, r := range o.relocs {
			if !discarded[r.section] {
				relocs = append(relocs, r)
			}
		}
		o.relocs = relocs
	}
	if err := coalesceMachOAtoms(objects); err != nil {
		return nil, err
	}
	return objects, nil
}

// Microsoft/LLD EXACT_MATCH compares section contents, not relocation targets
// or alignment. Relocations from the discarded definition are discarded too.
func sameGroupContents(a *object, ag sectionGroup, b *object, bg sectionGroup) bool {
	if len(ag.sections) != len(bg.sections) {
		return false
	}
	for i, index := range ag.sections {
		x, y := a.sections[index], b.sections[bg.sections[i]]
		if x == nil || y == nil || x.size != y.size || !bytes.Equal(x.data, y.data) {
			return false
		}
	}
	return true
}

func groupSize(o *object, g sectionGroup) uint64 {
	var size uint64
	for _, index := range g.sections {
		if s := o.sections[index]; s != nil {
			size += s.size
		}
	}
	return size
}
