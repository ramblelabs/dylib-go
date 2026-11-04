package dylib

import (
	"bytes"
	"debug/elf"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Change only relocation type fields. This permits actual ELF execution with
// baseline CI assemblers that predate the PATCHINST spelling in AAELF64.
func compileARM64Patch(t *testing.T, src, out string, flags ...string) string {
	t.Helper()
	compile(t, src, out, append([]string{"--target=aarch64-linux-gnu"}, flags...)...)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	changed := 0
	for _, sec := range f.Sections {
		if sec.Type != elf.SHT_RELA {
			continue
		}
		for off := sec.Offset; off < sec.Offset+sec.Size; off += 24 {
			info := le.Uint64(data[off+8:])
			if uint32(info) == 258 {
				le.PutUint64(data[off+8:], info&0xffffffff00000000|elfARM64PatchInst)
				changed++
			}
		}
	}
	if changed == 0 {
		t.Fatal("fixture has no ABS32 placeholders")
	}
	if err := os.WriteFile(out, data, 0600); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestARM64PatchInstMetadata(t *testing.T) {
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			path := compileARM64Patch(t, "testdata/elf_arm_patch.S", filepath.Join(t.TempDir(), "consumer.o"), opt)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			input, err := parse(path, data)
			if err != nil || len(input.info.Unsupported) != 0 {
				t.Fatal(input, err)
			}
			patches, calls := 0, 0
			for i, r := range input.obj.relocs {
				if r.typ == elfARM64PatchInst {
					patches++
				}
				if r.typ == 283 {
					calls++
					found := false
					for _, patch := range input.obj.relocs[i+1:] {
						found = found || patch.typ == elfARM64PatchInst && patch.offset == r.offset && patch.section == r.section
					}
					if !found {
						t.Fatal("call is not followed by its patch")
					}
				}
			}
			if patches != 5 || calls != 1 {
				t.Fatalf("patches=%d calls=%d", patches, calls)
			}
			if err := validateELFARM64PatchOrder(input.obj); err != nil {
				t.Fatal(err)
			}
			invalid := compileARM64Patch(t, "testdata/elf_arm_patch_unused.S", filepath.Join(t.TempDir(), "invalid.o"), opt)
			data, err = os.ReadFile(invalid)
			if err != nil {
				t.Fatal(err)
			}
			input, err = parse(invalid, data)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateELFARM64PatchOrder(input.obj); err == nil {
				t.Fatal("reversed patch/call accepted")
			}
		})
	}
}

func TestNativeARM64PatchInst(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" {
		t.Skip("AArch64 ELF execution requires Linux arm64")
	}
	needNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			dir := t.TempDir()
			consumer := compileARM64Patch(t, "testdata/elf_arm_patch.S", filepath.Join(dir, "consumer.o"), opt)
			weak := compileARM64Patch(t, "testdata/elf_arm_patch.S", filepath.Join(dir, "weak.o"), opt, "-DPATCH_WEAK")
			provider := compile(t, "testdata/elf_arm_patch_provider.S", filepath.Join(dir, "provider.o"), "--target=aarch64-linux-gnu", opt)
			partial := compile(t, "testdata/elf_arm_patch_provider.S", filepath.Join(dir, "partial.o"), "--target=aarch64-linux-gnu", opt, "-DPATCH_PARTIAL")
			helper := compile(t, "testdata/elf_arm_patch_helper.c", filepath.Join(dir, "helper.o"), opt)
			invalid := compileARM64Patch(t, "testdata/elf_arm_patch_unused.S", filepath.Join(dir, "invalid.o"), opt)
			archive := filepath.Join(dir, "provider.a")
			command(t, "ar", "rcs", archive, provider, invalid)
			root := filepath.Join(dir, "root.a")
			command(t, "ar", "rcs", root, consumer, helper, provider, invalid)
			for _, tc := range []struct {
				name      string
				inputs    []string
				patched   bool
				extraRoot string
			}{
				{"undefined_strong", []string{consumer, helper}, false, ""},
				{"undefined_weak", []string{weak, helper}, false, ""},
				{"objects", []string{consumer, helper, provider}, true, ""},
				{"objects_reversed", []string{provider, helper, consumer}, true, ""},
				{"undefined_second_patch", []string{consumer, helper, partial}, true, ""},
				{"archive", []string{consumer, helper, archive}, true, ""},
				{"archive_root", []string{root}, true, ""},
				{"weak_selected", []string{weak, helper, provider}, true, ""},
				{"weak_archive_unselected", []string{weak, helper, archive}, false, ""},
				{"weak_archive_root", []string{weak, helper, archive}, true, "patch_word"},
				{"invalid_root_retry", []string{consumer, helper, archive}, true, ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					s := New(Options{})
					defer s.Close()
					load(t, s, tc.inputs...)
					if tc.name == "invalid_root_retry" {
						if err := s.Link("patch_unused_invalid"); err == nil || !strings.Contains(err.Error(), "must follow") || s.image != nil {
							t.Fatal("invalid selected member published", err)
						}
					}
					roots := []string{"patch_eval"}
					if tc.extraRoot != "" {
						roots = append(roots, tc.extraRoot)
					}
					if err := s.Link(roots...); err != nil {
						t.Fatal(err)
					}
					for _, name := range []string{"patch_eval", "patch_addend", "patch_call", "patch_sequence"} {
						for _, args := range [][2]int32{{20, 22}, {-1, 22}} {
							want := args[0] + args[1]
							if tc.patched {
								want = args[0]
								if name == "patch_sequence" && tc.name != "undefined_second_patch" {
									want = args[0] - args[1]
								}
							}
							call(t, s, name, args[0], args[1], want)
						}
					}
					for _, o := range s.image.objects {
						for _, sym := range o.symbols {
							if sym.name == "patch_unused_invalid" {
								t.Fatal("unused invalid archive member selected")
							}
						}
					}
				})
			}
		})
	}
}
