package dylib

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestARM64InstructionSiteMetadata(t *testing.T) {
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			for _, tc := range []struct {
				source string
				types  []uint32
			}{
				{"testdata/elf_arm_low12.S", []uint32{275, 277, 278, 284, 285, 286, 299, 311, 312, 282, 283}},
				{"testdata/elf_arm_low12_provider.c", []uint32{275, 278, 284, 285, 286, 299}},
			} {
				path := compile(t, tc.source, filepath.Join(t.TempDir(), "input.o"), "--target=aarch64-linux-gnu", "-ffreestanding", opt)
				data, err := readFile(path)
				if err != nil {
					t.Fatal(err)
				}
				input, err := parse(path, data)
				if err != nil || len(input.info.Unsupported) != 0 {
					t.Fatal(input, err)
				}
				counts := make(map[uint32]int)
				for _, r := range input.obj.relocs {
					counts[r.typ]++
				}
				for _, typ := range tc.types {
					if counts[typ] == 0 {
						t.Fatalf("%s omitted relocation %d: %v", tc.source, typ, counts)
					}
				}
			}
		})
	}
}

func TestNativeARM64InstructionSites(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" {
		t.Skip("AArch64 ELF execution requires Linux arm64")
	}
	needNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			dir := t.TempDir()
			consumer := compile(t, "testdata/elf_arm_low12.S", filepath.Join(dir, "consumer.o"), opt)
			provider := compile(t, "testdata/elf_arm_low12_provider.c", filepath.Join(dir, "provider.o"), opt)
			unused := compile(t, "testdata/elf_arm_low12_unused.S", filepath.Join(dir, "unused.o"), opt)
			archive := filepath.Join(dir, "provider.a")
			command(t, "ar", "rcs", archive, provider, unused)
			rootArchive := filepath.Join(dir, "root.a")
			command(t, "ar", "rcs", rootArchive, consumer, provider, unused)
			control := filepath.Join(dir, "control.so")
			// The system linker binds these direct page references within the DSO,
			// matching the raw loader's eager, non-preemptible object definitions.
			command(t, compiler(), "-shared", "-Wl,-Bsymbolic", consumer, provider, "-o", control)
			for _, tc := range []struct {
				name   string
				inputs []string
			}{
				{"objects", []string{consumer, provider}},
				{"objects_reversed", []string{provider, consumer}},
				{"archive", []string{consumer, archive}},
				{"archive_root", []string{rootArchive}},
				{"missing_definition_retry", []string{consumer}},
				{"invalid_root_retry", []string{consumer, archive}},
				{"system_library", []string{control}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					s := New(Options{})
					defer s.Close()
					load(t, s, tc.inputs...)
					if tc.name == "missing_definition_retry" {
						if err := s.Link("low12_eval"); err == nil || !strings.Contains(err.Error(), "unresolved") || s.image != nil {
							t.Fatal("missing dependency published", err)
						}
						load(t, s, provider)
					}
					if tc.name == "invalid_root_retry" {
						if err := s.Link("low12_unused_invalid"); err == nil || !strings.Contains(err.Error(), "unsigned-offset") || s.image != nil {
							t.Fatal("invalid selected archive site published", err)
						}
					}
					if err := s.Link("low12_eval"); err != nil {
						t.Fatal(err)
					}
					for _, name := range []string{"low12_eval", "low12_simd_eval", "low12_signed_eval", "low12_compiler_eval", "low12_jump_eval", "low12_call_eval"} {
						call(t, s, "low12_clear", 0, 0, 0)
						call(t, s, name, 20, 22, 84)
						call(t, s, "low12_stored", 20, 22, 84)
						call(t, s, name, -1, 22, 63)
					}
					for _, name := range []string{"low12_address_eval", "low12_got_eval"} {
						call(t, s, name, 20, 22, 48)
						call(t, s, name, -1, 22, 27)
					}
					if tc.name != "system_library" && len(s.image.objects) != 2 {
						t.Fatal("unused invalid archive member retained", len(s.image.objects))
					}
				})
			}
		})
	}
}
