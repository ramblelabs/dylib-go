package dylib

import "fmt"

const elfARM64PatchInst = 316

// PATCHINST starts a new composition sequence: its explicit S+A replaces the
// word, independently of any relocation already applied to that instruction.
func (im *image) relocELFARM64Patch(o *object, r relocation, b []byte, place uintptr) error {
	if err := armInstructionPlace(place); err != nil {
		return err
	}
	d, err := im.resolveDefinition(o, r.symbol)
	if err != nil {
		return err
	}
	v := d.sym()
	if v.section == 0 && !v.tls && !elfOwnedGOT(d.o.info, v.name) && im.externalSymbol(d.o, v.name) == 0 {
		return nil // Undefined, including strong and null symbols, leaves the word intact.
	}
	if d.o.info.Format != "ELF" || v.section != -1 || v.tls {
		return fmt.Errorf("R_AARCH64_PATCHINST requires an absolute ELF symbol: %s", v.name)
	}
	// ELF64 arithmetic precedes the unsigned 32-bit result check. Do not pass
	// the absolute value through uintptr: inspection also runs on 32-bit hosts.
	return unsigned32(b, int64(v.value+uint64(r.addend)))
}

// Check selected objects only, so an unused archive member cannot break a link.
// Preserve producer order; PATCHINST must follow every other relocation at P.
func validateELFARM64PatchOrder(o *object) error {
	if o.info.Format != "ELF" || o.info.Arch != "arm64" {
		return nil
	}
	type site struct {
		section int
		offset  uint64
	}
	var patched map[site]bool
	for _, r := range o.relocs {
		if elfNoRelocation(o, r.typ) {
			continue
		}
		p := site{r.section, r.offset}
		if r.typ == elfARM64PatchInst {
			if patched == nil {
				patched = make(map[site]bool)
			}
			patched[p] = true
		} else if patched[p] {
			return fmt.Errorf("%s: R_AARCH64_PATCHINST must follow other relocations at section %d+%#x", o.info.Name, r.section, r.offset)
		}
	}
	return nil
}
