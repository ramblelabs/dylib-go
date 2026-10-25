//go:build !cgo || (!darwin && !linux && !windows)

package native

import "fmt"

type EmulatedTLS struct{}

func NewEmulatedTLS() (*EmulatedTLS, error) {
	return nil, fmt.Errorf("emulated TLS requires cgo or llgo")
}
func EmulatedTLSHelper() uintptr           { return 0 }
func (*EmulatedTLS) Add(uintptr) error     { return fmt.Errorf("emulated TLS requires cgo or llgo") }
func (*EmulatedTLS) LiveValues() int       { return 0 }
func (*EmulatedTLS) Close()                {}
func (*EmulatedTLS) Context() uintptr      { return 0 }
func EmulatedTLSThreadExitHelper() uintptr { return 0 }
func EmulatedTLSMSVCInitHelper() uintptr   { return 0 }
func EmulatedTLSMSVCExitHelper() uintptr   { return 0 }
func (*EmulatedTLS) SetThreadInitializers(uintptr, []uintptr) error {
	return fmt.Errorf("emulated TLS requires cgo or llgo")
}
func (*EmulatedTLS) DrainThreadDestructors() (uint64, bool) { return 0, false }

func (*EmulatedTLS) SetNativeThreadGuard(uintptr) error {
	return fmt.Errorf("native TLS requires cgo or llgo")
}
