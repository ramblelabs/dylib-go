package dylib

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func privateCOFFFiles(t *testing.T, target, optimization, encoding string) []string {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for _, entry := range []string{"a", "b"} {
		data, _ := privateCOFF(t, target, optimization, "private_"+entry, entry)
		if encoding == "bigobj" {
			data = promoteBigCOFF(t, data)
		}
		path := filepath.Join(dir, entry+".obj")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	helper := compile(t, "testdata/coff_private_observer.c", filepath.Join(dir, "observer.obj"), "--target="+target, optimization, "-ffreestanding", "-funwind-tables", "-ffunction-sections", "-fdata-sections")
	return append(paths, helper)
}

func privateCOFFLibrary(t *testing.T, target string, paths []string) string {
	t.Helper()
	output := filepath.Join(t.TempDir(), "private.dll")
	args := []string{"/dll", "/noentry", "/nodefaultlib", "/out:" + output, "/export:private_a", "/export:private_b"}
	if strings.Contains(target, "gnu") {
		args = append(args, "/lldmingw")
	}
	command(t, "lld-link", append(args, paths...)...)
	return output
}

func TestCOFFPrivateCOMDATLinkedControls(t *testing.T) {
	command(t, "llvm-readobj", "--version")
	for _, arch := range []string{"x86_64", "aarch64", "i686"} {
		for _, dialect := range []string{"w64-windows-gnu", "pc-windows-msvc"} {
			for _, optimization := range []string{"-O0", "-O2"} {
				for _, encoding := range []string{"ordinary", "bigobj"} {
					target := arch + "-" + dialect
					t.Run(target+"/"+optimization+"/"+encoding, func(t *testing.T) {
						paths := privateCOFFFiles(t, target, optimization, encoding)
						library := privateCOFFLibrary(t, target, paths)
						text, err := exec.Command("llvm-readobj", "--coff-exports", library).CombinedOutput()
						if err != nil || !strings.Contains(string(text), "Name: private_a") || !strings.Contains(string(text), "Name: private_b") {
							t.Fatalf("independent LLD export control: %v\n%s", err, text)
						}
					})
				}
			}
		}
	}
}

func TestNativeCOFFPrivateCOMDAT(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("COFF execution needs Windows")
	}
	needNative(t)
	for _, dialect := range []string{"w64-windows-gnu", "pc-windows-msvc"} {
		t.Run(dialect, func(t *testing.T) {
			testNativeCOFFPrivateCOMDAT(t, dialect)
		})
	}
}

func testNativeCOFFPrivateCOMDAT(t *testing.T, dialect string) {
	t.Helper()
	target := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH] + "-" + dialect
	for _, optimization := range []string{"-O0", "-O2"} {
		for _, encoding := range []string{"ordinary", "bigobj"} {
			t.Run(optimization+"/"+encoding, func(t *testing.T) {
				paths := privateCOFFFiles(t, target, optimization, encoding)
				archive := filepath.Join(t.TempDir(), "private.a")
				command(t, "ar", "rcs", archive, paths[0], paths[1])
				library := privateCOFFLibrary(t, target, paths)
				for _, input := range []struct {
					name  string
					paths []string
				}{{"objects", paths[:2]}, {"reversed", []string{paths[1], paths[0]}}, {"archive", []string{archive}}, {"library", []string{library}}} {
					modes := []string{"default"}
					if runtime.GOARCH != "386" && input.name != "library" {
						modes = append(modes, "registered")
					}
					for _, mode := range modes {
						t.Run(input.name+"/"+mode, func(t *testing.T) {
							s := New(Options{RegisterUnwind: mode == "registered"})
							defer s.Close()
							load(t, s, input.paths...)
							if input.name != "library" {
								if err := s.Link("private_a", "private_b"); err == nil || !strings.Contains(err.Error(), "private_observe") {
									t.Fatal("missing observer accepted before retry", err)
								}
								load(t, s, paths[2])
							}
							if err := s.Link("private_a", "private_b"); err != nil {
								t.Fatal(err)
							}
							call(t, s, "private_a", 20, 22, 42)
							call(t, s, "private_b", 19, 23, 42)
							if err := s.Close(); err != nil {
								t.Fatal(err)
							}
						})
					}
				}
			})
		}
	}
}
