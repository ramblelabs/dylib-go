package dylib

// AAELF64 gives an unresolved weak reference S=P for relative relocations,
// S=0 for absolute ones. This session has no dynamically preemptible PLT.
func (im *image) elfARM64Weak(o *object, r relocation, b []byte, place uintptr) (uintptr, bool, error) {
	switch r.typ {
	case 260, 261, 262, 273, 274, 275, 276, 279, 280, 282, 283, 287, 288, 289, 290, 291, 292, 293, 314:
	default:
		return 0, false, nil // GOT slots still hold the absolute target zero.
	}
	d, err := im.resolveDefinition(o, r.symbol)
	if err != nil {
		return 0, false, err
	}
	if s := d.sym(); s.section != 0 || !s.weak {
		return 0, false, nil // A selected SHN_ABS zero is a definition.
	}
	if r.typ == 282 || r.typ == 283 {
		if err := armELFBranchInstruction(b, r.typ, place); err != nil {
			return 0, false, err
		}
		// GNU ld replaces unresolved weak calls/jumps with NOP, including
		// nonzero addends. A NOP also leaves the caller's LR intact.
		le.PutUint32(b, 0xd503201f)
		return 0, true, nil
	}
	return place, false, nil
}
