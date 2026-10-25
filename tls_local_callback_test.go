//go:build libffi && cgo && (linux || darwin || windows)

package dylib

import "testing"

func TestNativeCXXLocalTLSCallbacks(t *testing.T) {
	needNativeCXXTLS(t)
	t.Setenv("DYLIB_TEST_TLS_LOCAL", "1")
	for _, abi := range localTLSABIs() {
		t.Run(abi, func(t *testing.T) {
			t.Setenv("DYLIB_TEST_TLS_ABI", abi)
			for _, mode := range []string{"native", "emulated"} {
				t.Run(mode, func(t *testing.T) {
					nativeMode := mode == "native"
					t.Run("destructors", func(t *testing.T) { testTLSThreadDestructorGoCallbacks(t, nativeMode) })
					t.Run("constructor_reentry", func(t *testing.T) { testTLSConstructorReentry(t, nativeMode) })
				})
			}
		})
	}
}
