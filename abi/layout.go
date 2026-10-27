package abi

import "fmt"

// Layout describes native storage on the current host. Size includes trailing
// padding; Alignment and Offsets are measured in bytes. Offsets lists direct
// struct members or array elements in declaration order, and is nil for scalars
// and pointers. Nested members can be queried separately with LayoutOf.
// The result owns ordinary Go memory and requires no cleanup.
type Layout struct {
	Size, Alignment uint64
	Offsets         []uint64
}

// LayoutOf queries host C storage layouts. Default and CDecl require cgo;
// other conventions also require -tags libffi and backend support. It does not
// compute a foreign target's layout or establish compatibility with an
// arbitrary compiler's record packing.
// Pointer queries describe the address itself, without laying out its pointee.
// Arrays may be queried as storage even though C cannot pass them by value.
// The backend's 64 KiB aggregate limit applies; void has no storage layout.
func LayoutOf(description TypeDesc, convention Convention) (Layout, error) {
	if err := description.Validate(); err != nil {
		return Layout{}, err
	}
	if description.Type == Void {
		return Layout{}, fmt.Errorf("void has no native storage layout")
	}
	if convention > SysV64 {
		return Layout{}, fmt.Errorf("invalid calling convention %d", convention)
	}
	return layoutOf(description, convention)
}
