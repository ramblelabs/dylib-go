//go:build linux

package dylib

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeELFARM64BKeyFrames(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("AArch64 B-key return-address signing requires a native ARM64 host")
	}
	for _, order := range []string{"compiler", "b_first"} {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(order+"/"+opt, func(t *testing.T) {
				var edit func(*testing.T, string)
				if order == "b_first" {
					edit = rewriteBKeyFrameFile
				}
				testNativePOSIXFrameInputs(t, edit, opt, "-mbranch-protection=pac-ret+leaf+b-key", "-fno-optimize-sibling-calls")
			})
		}
	}
}

func TestNativeELFARM64BKeyExceptions(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("AArch64 B-key return-address signing requires a native ARM64 host")
	}
	for _, mode := range []string{"compiler", "b_first", "mixed", "b_first_mixed"} {
		t.Run(mode, func(t *testing.T) {
			edit := func(t *testing.T, path string, flags []string) {
				if strings.Contains(mode, "mixed") && filepath.Base(path) == "catch.o" {
					// The raw thrower and OS-loaded host use B; catch/relay uses A.
					cpp := os.Getenv("DYLIB_CXX")
					if cpp == "" {
						cpp = "clang++"
					}
					args := append(append([]string(nil), flags...), "-mbranch-protection=pac-ret", "-c", "testdata/unwind_cxx_catch.cpp", "-o", path)
					command(t, cpp, args...)
					_, records := bKeyFileAugmentations(t, path)
					for _, letters := range records {
						if bytes.IndexByte(letters, 'B') >= 0 {
							t.Fatal("mixed-key catcher did not use A-key CIEs")
						}
					}
				} else if strings.Contains(mode, "b_first") {
					rewriteBKeyFrameFile(t, path)
				}
			}
			if mode == "compiler" {
				edit = nil
			}
			testNativeCXXExceptionInputs(t, false, edit, "-mbranch-protection=pac-ret+b-key")
		})
	}
}
