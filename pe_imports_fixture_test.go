package dylib

import (
	"debug/pe"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type peImportFixture struct {
	data            []byte
	file            string
	width           uint32
	base            uint64
	directoryOffset uint64
	sections        []*pe.Section
}

func (f peImportFixture) at(t *testing.T, rva, size uint32) []byte {
	t.Helper()
	for _, s := range f.sections {
		if rva >= s.VirtualAddress {
			offset := uint64(rva - s.VirtualAddress)
			if offset <= uint64(s.Size) && uint64(size) <= uint64(s.Size)-offset {
				return f.data[uint64(s.Offset)+offset : uint64(s.Offset)+offset+uint64(size)]
			}
		}
	}
	t.Fatal("fixture RVA outside file", rva, size)
	return nil
}
func (f peImportFixture) directory(index int) (uint32, uint32) {
	b := f.data[f.directoryOffset+uint64(index)*8:]
	return le.Uint32(b), le.Uint32(b[4:])
}

func (f peImportFixture) descriptor(t *testing.T, index int, dll string) (uint32, []byte) {
	t.Helper()
	rva, size := f.directory(index)
	width, nameOffset := uint32(20), uint32(12)
	if index == 13 {
		width, nameOffset = 32, 4
	}
	for offset := uint32(0); offset+width <= size; offset += width {
		v := f.at(t, rva+offset, width)
		if allZero(v) {
			break
		}
		name := le.Uint32(v[nameOffset:])
		if index == 13 && le.Uint32(v)&1 == 0 {
			name -= uint32(f.base)
		}
		if f.name(t, name) == dll {
			return rva + offset, v
		}
	}
	t.Fatal("missing fixture descriptor", index, dll)
	return 0, nil
}
func (f peImportFixture) name(t *testing.T, rva uint32) string {
	t.Helper()
	for _, s := range f.sections {
		if rva >= s.VirtualAddress && rva-s.VirtualAddress < s.Size {
			b := f.at(t, rva, s.Size-(rva-s.VirtualAddress))
			for i, v := range b {
				if v == 0 {
					return string(b[:i])
				}
			}
		}
	}
	t.Fatal("fixture name not terminated")
	return ""
}
func peFixtureSection(t *testing.T, f peImportFixture, name string) *pe.Section {
	t.Helper()
	for _, s := range f.sections {
		if s.Name == name {
			return s
		}
	}
	t.Fatal("missing PE fixture section", name)
	return nil
}

func buildPEImportFixture(t *testing.T, target, opt, shape string) peImportFixture {
	t.Helper()
	dir := t.TempDir()
	machine := uint16(0x8664)
	prefix := ""
	width := uint32(8)
	if strings.HasPrefix(target, "aarch64") {
		machine = 0xaa64
	}
	if strings.HasPrefix(target, "i686") {
		machine = 0x14c
		prefix = "_"
		width = 4
	}
	var entries [][]byte
	for _, v := range []struct {
		public, dll, export string
		ordinal             uint16
	}{{"first", "pe-first.dll", "first", 0}, {"second", "pe-second.dll", "second", 0}, {"numbered", "pe-second.dll", "", 7}} {
		policy := uint16(4)
		if v.ordinal != 0 {
			policy = 0
		}
		entries = append(entries, shortImport(machine, 0, policy, prefix+v.public, v.dll, v.export, v.ordinal))
	}
	for _, v := range []struct {
		name  string
		bytes int
	}{{"LoadLibraryW", 4}, {"GetModuleFileNameW", 12}, {"GetProcAddress", 8}, {"FreeLibrary", 4}} {
		public := v.name
		policy := uint16(1)
		if width == 4 {
			public = fmt.Sprintf("_%s@%d", v.name, v.bytes)
			policy = 3
		}
		entries = append(entries, shortImport(machine, 0, policy, public, "kernel32.dll", "", 0))
	}
	imports := filepath.Join(dir, "imports.lib")
	objects := writeComponentObjects(t, dir, entries)
	command(t, "llvm-ar", append([]string{"rcs", imports}, objects...)...)
	helper := compile(t, "testdata/peimports/helper.c", filepath.Join(dir, "helper.obj"), "--target="+target, opt)
	consumer := compile(t, "testdata/peimports/consumer.c", filepath.Join(dir, "consumer.obj"), "--target="+target, opt)
	prelude := ""
	if width == 4 {
		prelude = ".def @feat.00; .scl 3; .type 0; .endef\n.set @feat.00,1\n"
	}
	assembly := prelude + `.section .bound,"dr"
.long 1
.short first_name-bound_start,2
.long 2
.short kernel_name-bound_start,0
.long 3
	.short second_name-bound_start,0
.long 0xffffffff
.short second_name-bound_start,0
.zero 8
first_name: .asciz "pe-first.dll"
kernel_name: .asciz "kernel32.dll"
second_name: .asciz "pe-second.dll"
`
	assembly = strings.Replace(assembly, ".long 1\n", "bound_start:\n.long 1\n", 1)
	word := ".quad"
	if width == 4 {
		word = ".long"
	}
	for group, n := range []int{1, 2} {
		assembly += fmt.Sprintf(".section .cache%d,\"dr\"\n", group)
		for slot := 0; slot < n; slot++ {
			assembly += fmt.Sprintf("%s 0x%x\n", word, directoryCachedAddress(target, group, slot))
		}
		assembly += word + " 0\n"
	}
	if shape == "delay" || shape == "delay_bound" || shape == "legacy" {
		for group, names := range [][]string{{"first"}, {"second", "numbered"}} {
			assembly += fmt.Sprintf(".section .unload%d,\"dr\"\n", group)
			for range names {
				// A real image symbol makes the linker emit base relocations.
				// Its word is replaced with the original IAT after linking.
				assembly += word + " " + prefix + "invoke\n"
			}
			assembly += word + " 0\n"
		}
	}
	raw, _ := coffAssembly(t, target, assembly)
	metadata := filepath.Join(dir, "metadata.obj")
	if err := os.WriteFile(metadata, raw, 0600); err != nil {
		t.Fatal(err)
	}
	definition := filepath.Join(dir, "control.def")
	var exports strings.Builder
	exports.WriteString("EXPORTS\n")
	for _, name := range []string{"observed", "unload", "invoke"} {
		exports.WriteString(name)
		exports.WriteString("\n")
	}
	if err := os.WriteFile(definition, []byte(exports.String()), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "control.dll")
	args := []string{"/dll", "/noentry", "/nodefaultlib", "/out:" + output, "/def:" + definition, helper, consumer, metadata, imports}
	if shape == "delay" || shape == "delay_bound" || shape == "legacy" {
		args = append(args, "/delayload:pe-first.dll", "/delayload:pe-second.dll")
	}
	if shape == "legacy" {
		args = append(args, "/base:0x60000000", "/fixed")
	}
	command(t, "lld-link", args...)
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	image, err := pe.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	f := peImportFixture{data: data, file: output, width: width, sections: image.Sections}
	f.directoryOffset = uint64(le.Uint32(data[60:])) + 24
	switch h := image.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		f.directoryOffset += 96
		f.base = uint64(h.ImageBase)
	case *pe.OptionalHeader64:
		f.directoryOffset += 112
		f.base = h.ImageBase
	default:
		t.Fatal("missing optional header")
	}
	if shape == "bound" {
		s := peFixtureSection(t, f, ".bound")
		field := f.data[f.directoryOffset+11*8:]
		le.PutUint32(field, s.VirtualAddress)
		le.PutUint32(field[4:], s.VirtualSize)
	}
	if shape == "delay" || shape == "delay_bound" || shape == "legacy" {
		rva, size := f.directory(13)
		table := f.at(t, rva, size)
		for offset := 0; offset+32 <= len(table) && !allZero(table[offset:offset+32]); offset += 32 {
			d := table[offset : offset+32]
			name := f.name(t, le.Uint32(d[4:]))
			index := 0
			if name == "pe-second.dll" {
				index = 1
			}
			unload := peFixtureSection(t, f, fmt.Sprintf(".unload%d", index))
			le.PutUint32(d[24:], unload.VirtualAddress)
			iat := le.Uint32(d[12:])
			for slot := uint32(0); ; slot++ {
				value := f.at(t, iat+slot*width, width)
				copy(f.at(t, unload.VirtualAddress+slot*width, width), value)
				if allZero(value) {
					break
				}
			}
			if shape == "delay_bound" {
				name := f.name(t, le.Uint32(d[4:]))
				index := 0
				if name == "pe-second.dll" {
					index = 1
				}
				cache := peFixtureSection(t, f, fmt.Sprintf(".cache%d", index))
				le.PutUint32(d[20:], cache.VirtualAddress)
				le.PutUint32(d[28:], uint32(index+1))
			} else if shape == "legacy" {
				names := le.Uint32(d[16:])
				for slot := uint32(0); ; slot++ {
					word := f.at(t, names+slot*4, 4)
					value := le.Uint32(word)
					if value == 0 {
						break
					}
					if value&0x80000000 == 0 {
						le.PutUint32(word, value+uint32(f.base))
					}
				}
				le.PutUint32(d, 0)
				for i := 1; i <= 6; i++ {
					field := d[i*4:]
					value := le.Uint32(field)
					if value != 0 {
						le.PutUint32(field, value+uint32(f.base))
					}
				}
			}
		}
	}
	if err := os.WriteFile(output, f.data, 0600); err != nil {
		t.Fatal(err)
	}
	return f
}
