//go:build cgo && (linux || darwin)

package cbridge_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeGCCHeaderBridge(t *testing.T) {
	parser := os.Getenv("CLANG")
	if parser == "" {
		parser = "clang"
	}
	parser = tool(t, parser)
	// Both the producer and facade use GCC. Clang supplies only the logical field
	// model; its native record offsets/classes never cross the pointer boundary.
	testNativeHeader(t, parser, gccTool(t))
}

func TestNativeGCCHeaderConstLargeArray(t *testing.T) {
	testNativeConstLargeArray(t, clangTool(t), gccTool(t))
}

func TestNativeGCCHeaderFunctionPointers(t *testing.T) {
	testNativeEntries(t, clangTool(t), gccTool(t))
}

func TestNativeGCCHeaderCallingConventions(t *testing.T) {
	testNativeConventions(t, clangTool(t), gccTool(t))
}

func gccTool(t *testing.T) string {
	t.Helper()
	candidates := []string{"gcc"}
	if fc := os.Getenv("DYLIB_FC"); strings.Contains(filepath.Base(fc), "gfortran") {
		candidates = append(candidates, filepath.Join(filepath.Dir(fc), strings.Replace(filepath.Base(fc), "gfortran", "gcc", 1)))
	}
	candidates = append(candidates, "gcc-16", "gcc-15", "gcc-14")
	var gcc string
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		version, err := exec.Command(path, "--version").CombinedOutput()
		if err == nil && bytes.Contains(version, []byte("Free Software Foundation")) {
			gcc = path
			break
		}
	}
	if gcc == "" {
		if os.Getenv("DYLIB_TEST_REQUIRE_TOOLS") == "1" {
			t.Fatal("GNU GCC required for original-header producer controls")
		}
		t.Skip("GNU GCC unavailable")
	}
	return gcc
}
