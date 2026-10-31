//go:build !windows

package dylib

import "fmt"

func registerCOFFLazyImage(*coffLazyImage) (uintptr, uintptr, error) {
	return 0, 0, fmt.Errorf("raw COFF lazy imports require Windows")
}
func unregisterCOFFLazyImage(uintptr)              {}
func coffLazyNativeOperations() coffLazyOperations { return coffLazyOperations{} }
