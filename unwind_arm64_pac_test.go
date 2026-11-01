package dylib

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestARM64ReturnAddressSigningValidation(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "386"} {
		for _, record := range []string{"cie", "fde"} {
			t.Run(arch+"/"+record, func(t *testing.T) {
				im := elfUnwindTestImage()
				im.objects[0].info.Arch = arch
				offset := 151
				if record == "fde" {
					offset = 175
				}
				im.mem[offset] = 0x2d
				_, err := im.elfFrameSections()
				if (err == nil) != (arch == "arm64") {
					t.Fatal("architecture-specific CFI validation", err)
				}
				if _, err := im.validateDWARFFrames(im.mem[128:200], 128, true, nil); err == nil {
					t.Fatal("generic/Mach-O validator accepted unqualified signing metadata")
				}
			})
		}
	}
	for _, code := range [][]byte{{0x2d, 14, 8, 0x2d}, {0x2d, 14, 0x80}} {
		t.Run(fmt.Sprintf("operands/%x", code), func(t *testing.T) {
			r := frameReader{b: code}
			err := r.cfi(true)
			if (err == nil) != (len(code) == 4) {
				t.Fatal("signing CFI consumed a following operand", err)
			}
		})
	}
	for _, code := range []byte{0x2b, 0x2c} {
		t.Run(fmt.Sprintf("unqualified/%x", code), func(t *testing.T) {
			r := frameReader{b: []byte{code}}
			if err := r.cfi(true); err == nil {
				t.Fatal("accepted a different pointer-authentication instruction")
			}
		})
	}
}

func TestARM64ReturnAddressSigningCompilerRecords(t *testing.T) {
	testARM64SigningCompilerRecords(t, "pac-ret", false)
}

func testARM64SigningCompilerRecords(t *testing.T, protection string, bFirst bool) {
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			path := compile(t, "testdata/unwind_frames.c", filepath.Join(t.TempDir(), "pac.o"), "--target=aarch64-linux-gnu", "-funwind-tables", "-mbranch-protection="+protection, opt)
			if bFirst {
				rewriteBKeyFrameFile(t, path)
			}
			data, err := readFile(path)
			if err != nil {
				t.Fatal(err)
			}
			f, err := parse(path, data)
			if err != nil {
				t.Fatal(err)
			}
			o := f.obj
			defs, err := definitions([]*object{o})
			if err != nil {
				t.Fatal(err)
			}
			// Relocate the real producer records entirely in Go. Foreign
			// instructions are inspected here; native CI executes them below.
			size := uint64(128)
			for _, s := range o.sections {
				if s != nil {
					s.offset = alignUp(size, s.align)
					size = s.offset + s.size + s.tail
				}
			}
			im := &image{base: 0x100000, pointerSize: 8, mem: make([]byte, size), objects: []*object{o}, defs: defs, resolved: make(map[string]uintptr), external: func(name string) uintptr {
				if name == "capture_raw_frames" {
					return 0x101000
				}
				return 0
			}}
			for _, s := range o.sections {
				if s != nil {
					copy(im.mem[s.offset:], s.data)
				}
			}
			for _, r := range o.relocs {
				if err := im.relocate(o, r); err != nil {
					t.Fatal(err)
				}
			}
			staged := make(map[*section]string)
			for _, s := range o.sections {
				if s != nil {
					staged[s] = string(s.data)
				}
			}
			if err := im.prepareELFUnwind(); err != nil {
				t.Fatal("signed-return frame preparation", err)
			}
			frames, err := im.elfFrameSections()
			if err != nil || len(frames) != 1 {
				t.Fatal("actual signed-return FDEs", frames, err)
			}
			for _, s := range o.sections {
				if s != nil && s.name == ".eh_frame" {
					if string(s.data) != staged[s] {
						t.Fatal("frame normalization changed staged producer bytes")
					}
					if strings.Contains(protection, "b-key") {
						count := 0
						for _, augmentation := range bKeyCIEAugmentations(t, im.mem[s.offset:s.offset+s.size]) {
							if strings.ContainsRune(string(augmentation), 'B') {
								count++
								if augmentation[len(augmentation)-1] != 'B' {
									t.Fatal("B-key marker is not canonical", string(augmentation))
								}
							}
						}
						if count == 0 {
							t.Fatal("compiler emitted no B-key CIE")
						}
					}
					_, err := im.validateFrameRecords(im.mem[s.offset:s.offset+s.size], s.offset, framePolicy{language: true})
					if err == nil || protection == "pac-ret" && !strings.Contains(err.Error(), "return-address signing") {
						t.Fatal("compiler did not emit parsed signing CFI", err)
					}
				}
			}
		})
	}
}
