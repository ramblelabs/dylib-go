//go:build libffi && cgo && (linux || darwin || windows)

package dylib

import "testing"

func TestNativeMSVCTLSGoCallbacks(t *testing.T) {
	needMSVCTLS(t)
	testTLSThreadDestructorGoCallbacks(t, true)
}

func TestNativeMSVCTLSConstructorReentry(t *testing.T) {
	needMSVCTLS(t)
	testTLSConstructorReentry(t, true)
}
