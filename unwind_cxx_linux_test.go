//go:build linux

package dylib

import (
	"runtime"
	"testing"
)

func TestNativeELFCXXExceptions(t *testing.T) { testNativeCXXExceptions(t) }

func TestNativeELFARM64PACFrames(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("AArch64 return-address signing requires a native ARM64 host")
	}
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			testNativePOSIXFrameRegistration(t, opt, "-mbranch-protection=pac-ret", "-fno-optimize-sibling-calls")
		})
	}
}

func TestNativeELFARM64PACExceptions(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("AArch64 return-address signing requires a native ARM64 host")
	}
	testNativeCXXExceptionsMode(t, false, "-mbranch-protection=pac-ret")
}
