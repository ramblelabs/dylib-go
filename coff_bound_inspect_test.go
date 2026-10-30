package dylib

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/peinfo"
)

func coffBoundFixture(t *testing.T, target, extra string) ([]byte, uint64) {
	t.Helper()
	source, err := os.ReadFile("testdata/coff_bound.S")
	if err != nil {
		t.Fatal(err)
	}
	// Give the root a nonzero offset and a different section spelling. Neither
	// section naming nor section-relative offsets can discover this directory.
	assembly := strings.Replace(string(source), ".section .bound,\"dr\"", ".section .custom,\"dr\"\n.zero 13", 1) + extra
	b, o := coffAssembly(t, target, assembly)
	var start, end uint64
	for _, s := range o.symbols {
		if s.name == "bound_start" {
			start = s.value
		} else if s.name == "bound_end" {
			end = s.value
		}
	}
	if start != 13 || end <= start {
		t.Fatal("missing assembler directory bounds", start, end)
	}
	return b, end - start
}

func TestCOFFBoundDirectoryInspection(t *testing.T) {
	want := []peinfo.BoundImport{
		{DLL: "pe-first.dll", TimeDateStamp: 1, Forwarders: []peinfo.BoundForwarder{{DLL: "kernel32.dll", TimeDateStamp: 2}, {DLL: "pe-second.dll", TimeDateStamp: 3}}},
		{DLL: "pe-second.dll", TimeDateStamp: 0xffffffff},
	}
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		for _, big := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/bigobj_%t", target, big), func(t *testing.T) {
				data, size := coffBoundFixture(t, target, ".section .other,\"dr\"\n.long unrelated_external\n")
				if big {
					data = promoteBigCOFF(t, data)
				}
				path := filepath.Join(t.TempDir(), "metadata.obj")
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				got, err := InspectCOFFBoundImportDirectory(path, "bound_start", size)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatal(got, err)
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(data, after) {
					t.Fatal("metadata inspection changed object", err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal("returned metadata retained its source", got)
				}
			})
		}
	}
}

func TestCOFFBoundDirectoryInspectionValidation(t *testing.T) {
	data, size := coffBoundFixture(t, "x86_64-pc-windows-msvc", "")
	for _, tc := range []struct {
		name, root string
		size       uint64
		edit       func([]byte) []byte
	}{
		{name: "empty_root", size: size},
		{name: "nul_root", root: "bound_start\x00", size: size},
		{name: "missing_root", root: "absent", size: size},
		{name: "zero_size", root: "bound_start"},
		{name: "oversized", root: "bound_start", size: ^uint64(0)},
		{name: "past_storage", root: "bound_start", size: size + 1},
		{name: "missing_name_terminator", root: "bound_start", size: size - 1},
		{name: "duplicate_root", root: "bound_start", size: size, edit: func(b []byte) []byte {
			f, err := readCOFF(b)
			if err != nil {
				t.Fatal(err)
			}
			var root []byte
			for i := 0; i < len(f.records)/f.entrySize; i++ {
				if f.symbol(i).value == 13 && f.symbol(i).class == 2 {
					root = f.record(i)[:8]
				}
			}
			if root == nil {
				t.Fatal("missing root symbol record")
			}
			for i := 0; i < len(f.records)/f.entrySize; i++ {
				if f.symbol(i).value == uint32(13+size) && f.symbol(i).class == 2 {
					copy(f.record(i)[:8], root)
				}
			}
			return b
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := append([]byte(nil), data...)
			if tc.edit != nil {
				b = tc.edit(b)
			}
			path := filepath.Join(t.TempDir(), "metadata.obj")
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			if got, err := InspectCOFFBoundImportDirectory(path, tc.root, tc.size); err == nil || got != nil {
				t.Fatal("invalid explicit bound directory accepted", got, err)
			}
		})
	}
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target+"/relocation", func(t *testing.T) {
			b, size := coffBoundFixture(t, target, ".section .custom,\"dr\"\n.long unresolved\n")
			path := filepath.Join(t.TempDir(), "relocated.obj")
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			if got, err := InspectCOFFBoundImportDirectory(path, "bound_start", size); err == nil || got != nil || !strings.Contains(err.Error(), "relocation-free") {
				t.Fatal("unresolved section bytes accepted", got, err)
			}
		})
	}
}

func TestCOFFBoundDirectoryInspectionRootStorage(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
	}{
		{"undefined", ".globl invalid_root\n"},
		{"absolute", ".globl invalid_root\n.set invalid_root,13\n"},
		{"zero_fill", ".section .bss,\"bw\"\n.globl invalid_root\ninvalid_root: .zero 128\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, size := coffBoundFixture(t, "x86_64-pc-windows-msvc", tc.extra)
			path := filepath.Join(t.TempDir(), "metadata.obj")
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			if got, err := InspectCOFFBoundImportDirectory(path, "invalid_root", size); err == nil || got != nil {
				t.Fatal("root without file-backed definition accepted", got, err)
			}
		})
	}
}

func TestCOFFBoundDirectoryInspectionFileValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.obj")
	if got, err := InspectCOFFBoundImportDirectory(path, "root", 8); err == nil || got != nil {
		t.Fatal("missing input accepted", got, err)
	}
	for _, data := range [][]byte{nil, []byte("!<arch>\n"), []byte("!<thin>\n"), []byte("\x7fELF")} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if got, err := InspectCOFFBoundImportDirectory(path, "root", 8); err == nil || got != nil {
			t.Fatal("non-object input accepted", got, err)
		}
	}
}
