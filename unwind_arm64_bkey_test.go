package dylib

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"
	"testing"
)

func TestARM64BKeyValidation(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "386"} {
		for _, version := range []byte{1, 3} {
			for _, order := range []string{"zRB", "zBR"} {
				t.Run(fmt.Sprintf("%s/v%d/%s", arch, version, order), func(t *testing.T) {
					im := elfUnwindTestImage()
					im.objects[0].info.Arch = arch
					cie := im.mem[128:152]
					copy(cie[12:], cie[11:23]) // Use one of the CIE's existing padding bytes.
					copy(cie[9:], order)
					cie[8] = version
					original := append([]byte(nil), im.mem...)
					_, err := im.elfFrameSections()
					if (err == nil) != (arch == "arm64") {
						t.Fatal("target-specific B-key augmentation", err)
					}
					if !bytes.Equal(im.mem, original) {
						t.Fatal("read-only validation changed frame bytes")
					}
					if arch == "arm64" {
						if err := im.prepareELFUnwind(); err != nil {
							t.Fatal("B-key preparation", err)
						}
						copy(original[137:], "zRB")
						if err := im.prepareELFUnwind(); err != nil {
							t.Fatal("normalization is not idempotent", err)
						}
					}
					if !bytes.Equal(im.mem, original) {
						t.Fatal("normalization changed bytes outside the augmentation string")
					}
					if _, err := im.validateFrameRecords(im.mem[128:200], 128, framePolicy{language: true}); err == nil {
						t.Fatal("generic/Mach-O policy accepted unqualified B-key frames")
					}
				})
			}
		}
	}
	for _, mode := range []string{"duplicate", "extra_data", "missing_data", "mte", "pauth_lr"} {
		t.Run(mode, func(t *testing.T) {
			im := elfUnwindTestImage()
			im.objects[0].info.Arch = "arm64"
			cie := im.mem[128:152]
			copy(cie[12:], cie[11:23])
			copy(cie[9:], "zRB")
			switch mode {
			case "duplicate":
				copy(cie[13:], cie[12:23])
				cie[12] = 'B'
			case "extra_data":
				cie[16] = 2
			case "missing_data":
				cie[16] = 0
			case "mte":
				cie[11] = 'G'
			case "pauth_lr":
				cie[11] = 'N'
			}
			if _, err := im.elfFrameSections(); err == nil {
				t.Fatal("invalid/unqualified B-key metadata accepted")
			}
		})
	}
}

func TestARM64BKeyCompilerRecords(t *testing.T) {
	for _, protection := range []string{"pac-ret+b-key", "pac-ret+leaf+b-key"} {
		for _, order := range []string{"compiler", "b_first"} {
			t.Run(protection+"/"+order, func(t *testing.T) {
				testARM64SigningCompilerRecords(t, protection, order == "b_first")
			})
		}
	}
}

// These helpers edit only the zero-data B marker in actual compiler records;
// every data field, relocation, CIE reference and FDE stays at its original offset.
func bKeyCIEAugmentations(t testing.TB, frame []byte) [][]byte {
	t.Helper()
	var result [][]byte
	for offset := 0; offset < len(frame); {
		if len(frame)-offset < 4 {
			t.Fatal("short producer frame length")
		}
		size := int(binary.LittleEndian.Uint32(frame[offset:]))
		if size == 0 {
			break
		}
		if size < 5 || size > len(frame)-offset-4 {
			t.Fatal("bad producer frame length")
		}
		end := offset + 4 + size
		if binary.LittleEndian.Uint32(frame[offset+4:]) == 0 {
			letters := frame[offset+9 : end]
			zero := bytes.IndexByte(letters, 0)
			if zero < 0 {
				t.Fatal("unterminated producer CIE augmentation")
			}
			result = append(result, letters[:zero])
		}
		offset = end
	}
	return result
}

func rewriteBKeyFrameFile(t *testing.T, path string) {
	t.Helper()
	data, records := bKeyFileAugmentations(t, path)
	count := 0
	for _, letters := range records {
		if key := bytes.IndexByte(letters, 'B'); key > 0 {
			copy(letters[2:key+1], letters[1:key])
			letters[1] = 'B'
			count++
		}
	}
	if count == 0 {
		t.Fatal("producer emitted no B-key frame")
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func bKeyFileAugmentations(t *testing.T, path string) ([]byte, [][]byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var records [][]byte
	for _, section := range f.Sections {
		if !elfUnwindSection(section.Name) {
			continue
		}
		records = append(records, bKeyCIEAugmentations(t, data[section.Offset:section.Offset+section.Size])...)
	}
	return data, records
}
