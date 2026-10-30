//go:build !windows || !cgo

package native

func COFFDelayHelper() uintptr { return 0 }
func COFFDelayEntrySize() int  { return 0 }
func ConfigureCOFFDelayEntry(address, callback, owner, slot, descriptor, cell, dll, name uintptr, ordinal uint16) bool {
	return false
}
func ReadCOFFDelayRequest(address uintptr) (COFFDelayRequest, bool) {
	return COFFDelayRequest{}, false
}
func SetCOFFDelayResult(address, target, module uintptr, exception, nativeError uint32) bool {
	return false
}
