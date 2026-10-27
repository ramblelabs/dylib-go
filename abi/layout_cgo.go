//go:build cgo && !libffi

package abi

func layoutOf(d TypeDesc, convention Convention) (Layout, error) {
	if convention != Default && convention != CDecl {
		return Layout{}, ErrUnavailable
	}
	return hostStorageLayout(d)
}
