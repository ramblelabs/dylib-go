package dylib

import (
	"debug/pe"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type boundDirectoryShape struct {
	name                   string
	timestamps, forwarders []uint32
	omit                   []bool
}

var boundDirectoryShapes = []boundDirectoryShape{
	{"first", []uint32{1, 0}, nil, nil},
	{"second", []uint32{0, 0x12345678}, nil, nil},
	{"both", []uint32{1, 0x12345678}, nil, nil},
	{"timestamp_marker", []uint32{0xffffffff, 0xffffffff}, nil, nil},
	{"forwarders", []uint32{1, 1}, []uint32{0xffffffff, 1}, nil},
	{"other_ilt_absent", []uint32{1, 0}, nil, []bool{false, true}},
}

func (shape boundDirectoryShape) layout(repeated bool) directoryLayout {
	return directoryLayout{repeated: repeated, timestamps: shape.timestamps, forwarders: shape.forwarders}
}

func stagedBoundDirectory(t *testing.T, target string, repeated bool) *object {
	t.Helper()
	assembly := importDirectoryAssemblyOptions(target, directoryGroups(), nil, nil, boundDirectoryShapes[2].layout(repeated))
	_, o := coffDirectoryAssembly(t, target, strings.Replace(assembly, ".idata$2", ".unused2", 1))
	for _, sec := range o.sections {
		if sec != nil && sec.name == ".unused2" {
			sec.name = ".idata$2"
		}
	}
	return o
}

func TestCOFFBoundImportTargets(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		for _, repeated := range []bool{false, true} {
			for _, reverse := range []bool{false, true} {
				for _, kind := range []string{"pure", "ordinary", "comdat"} {
					for _, shape := range boundDirectoryShapes {
						if kind != "pure" && shape.name != "both" {
							continue
						}
						t.Run(fmt.Sprintf("%s/repeated_%t/reverse_%t/%s/%s", target, repeated, reverse, kind, shape.name), func(t *testing.T) {
							order := []int{0, 1}
							if reverse {
								order = []int{1, 0}
							}
							extra := ""
							if kind != "pure" {
								extra = directoryMixedAssembly(target, kind == "comdat")
							}
							_, control := coffDirectoryAssembly(t, target, importDirectoryAssemblyLayout(target, directoryGroups(), order, shape.omit, repeated)+extra)
							data, o := coffDirectoryAssembly(t, target, importDirectoryAssemblyOptions(target, directoryGroups(), order, shape.omit, shape.layout(repeated))+extra)
							if len(o.info.Unsupported) != 0 || !reflect.DeepEqual(o.info.Imports, control.info.Imports) || !reflect.DeepEqual(o.groups, control.groups) {
								t.Fatal("bound cache changed import identity or ordinary groups", o.info, o.groups, control.info.Imports)
							}
							t.Run("bigobj", func(t *testing.T) {
								file, err := parse("bound.obj", promoteBigCOFF(t, data))
								if err != nil {
									t.Fatal(err)
								}
								if len(file.obj.info.Unsupported) != 0 || !reflect.DeepEqual(file.obj.info.Imports, o.info.Imports) || !reflect.DeepEqual(file.obj.groups, o.groups) {
									t.Fatal("bigobj changed bound identities", file.obj.info)
								}
							})
						})
					}
				}
			}
			t.Run(fmt.Sprintf("%s/repeated_%t/copy_on_write", target, repeated), func(t *testing.T) {
				o := stagedBoundDirectory(t, target, repeated)
				before := copyDirectoryObject(o)
				out, ok := newCOFFImportGraph([]*object{o}).convert(o)
				if !ok || len(out.info.Imports) != 5 || !reflect.DeepEqual(o, before) {
					t.Fatal("bound conversion lost imports or changed input", ok)
				}
			})
		}
	}
}

func TestCOFFBoundImportValidation(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			base := stagedBoundDirectory(t, target, true)
			dir := directorySection(t, base, ".idata$2")
			iat := repeatedSections(base, ".idata$5")[1]
			ilt := repeatedSections(base, ".idata$4")[1]
			width := uint64(base.info.Bits / 8)
			var firstHint relocation
			firstHint = *directoryRelocation(t, base, ilt, 0)
			removeLookup := func(o *object) {
				for i, r := range o.relocs {
					if r.section == dir && r.offset == 20 {
						o.relocs = append(o.relocs[:i], o.relocs[i+1:]...)
						return
					}
				}
				t.Fatal("missing second lookup reference")
			}
			cases := []struct {
				name string
				edit func(*object)
			}{
				{"missing_ilt", func(o *object) { removeLookup(o) }},
				{"nonzero_missing_ilt", func(o *object) { removeLookup(o); le.PutUint32(o.sections[dir].data[20:], 1) }},
				{"lookup_to_iat", func(o *object) { directoryRelocation(t, o, dir, 20).symbol = directoryRelocation(t, o, dir, 36).symbol }},
				{"unknown_lookup_relocation", func(o *object) { directoryRelocation(t, o, dir, 20).typ = 0xffff }},
				{"zero_cache_slot", func(o *object) { clear(o.sections[iat].data[:width]) }},
				{"zero_later_cache_slot", func(o *object) { clear(o.sections[iat].data[width : 2*width]) }},
				{"relocated_cache", func(o *object) { r := firstHint; r.section = iat; o.relocs = append(o.relocs, r) }},
				{"relocated_cache_terminator", func(o *object) {
					r := firstHint
					r.section = iat
					r.offset = o.sections[iat].size - width
					o.relocs = append(o.relocs, r)
				}},
				{"nonzero_cache_terminator", func(o *object) { o.sections[iat].data[o.sections[iat].size-width] = 1 }},
				{"missing_cache_terminator", func(o *object) { s := o.sections[iat]; s.size -= width; s.data = s.data[:s.size] }},
				{"partial_cache_slot", func(o *object) { s := o.sections[iat]; s.size--; s.data = s.data[:s.size] }},
				{"missing_ilt_name_reference", func(o *object) {
					for i, r := range o.relocs {
						if r.section == ilt && r.offset == 0 {
							o.relocs = append(o.relocs[:i], o.relocs[i+1:]...)
							return
						}
					}
					t.Fatal("missing hint")
				}},
				{"bad_ilt_name", func(o *object) {
					hint := directoryRelocation(t, o, ilt, 0)
					hint.symbol = directoryRelocation(t, o, dir, 32).symbol
				}},
				{"zero_ordinal", func(o *object) { o.sections[ilt].data[width] = 0 }},
				{"ordinal_reserved_bits", func(o *object) { o.sections[ilt].data[width+2] = 1 }},
				{"unmarked_cache", func(o *object) { clear(o.sections[dir].data[24:28]) }},
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					o := copyDirectoryObject(base)
					c.edit(o)
					before := copyDirectoryObject(o)
					if _, ok := newCOFFImportGraph([]*object{o}).convert(o); ok {
						t.Fatal("malformed bound table accepted")
					}
					if !reflect.DeepEqual(o, before) {
						t.Fatal("failed conversion changed input")
					}
				})
			}
		})
	}
}

// The system linker consumes the original compiler-produced cache bytes.
// Reading its PE output independently proves the timestamps/cache were retained.
func checkBoundDirectoryControl(t *testing.T, target, filename string, layout directoryLayout, order []int) {
	t.Helper()
	output, err := exec.Command("llvm-readobj", "--coff-imports", filename).CombinedOutput()
	if err != nil {
		t.Fatalf("independent bound PE reader: %v\n%s", err, output)
	}
	for _, text := range []string{"Name: first.dll", "Name: second.dll", "Symbol: add", "Symbol: value", "(7)"} {
		if !strings.Contains(string(output), text) {
			t.Fatalf("missing %q in bound PE imports\n%s", text, output)
		}
	}
	f, err := pe.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rva uint32
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		rva = h.DataDirectory[1].VirtualAddress
	case *pe.OptionalHeader64:
		rva = h.DataDirectory[1].VirtualAddress
	default:
		t.Fatal("missing PE import directory")
	}
	read := func(rva uint32, size uint64) []byte {
		t.Helper()
		for _, s := range f.Sections {
			if rva < s.VirtualAddress {
				continue
			}
			offset := uint64(rva - s.VirtualAddress)
			if offset > uint64(s.Size) || size > uint64(s.Size)-offset {
				continue
			}
			data, err := s.Data()
			if err != nil {
				t.Fatal(err)
			}
			return data[offset : offset+size]
		}
		t.Fatal("PE RVA outside sections", rva, size)
		return nil
	}
	directory := read(rva, uint64(len(order)+1)*20)
	for row, group := range order {
		descriptor := directory[row*20 : row*20+20]
		if le.Uint32(descriptor[4:]) != layout.timestamp(group) || le.Uint32(descriptor[8:]) != layout.forwarder(group) {
			t.Fatal("linker changed bound cache metadata", row)
		}
		if layout.timestamp(group) == 0 {
			continue
		}
		if le.Uint32(descriptor) == 0 {
			t.Fatal("bound descriptor lost ILT")
		}
		width := uint64(8)
		if strings.HasPrefix(target, "i686") {
			width = 4
		}
		entries := directoryGroups()[group].entries
		cache := read(le.Uint32(descriptor[16:]), uint64(len(entries)+1)*width)
		for slot := range entries {
			got := uint64(le.Uint32(cache[uint64(slot)*width:]))
			if width == 8 {
				got = le.Uint64(cache[uint64(slot)*width:])
			}
			if got != directoryCachedAddress(target, group, slot) {
				t.Fatal("linker changed bound address", row, slot, got)
			}
		}
		if !allZero(cache[uint64(len(entries))*width:]) {
			t.Fatal("bound PE cache lacks null terminator")
		}
	}
	if !allZero(directory[len(order)*20:]) {
		t.Fatal("bound PE directory lacks null descriptor")
	}
}

func TestCOFFBoundImportSystemControl(t *testing.T) {
	if _, err := exec.LookPath("lld-link"); err != nil {
		t.Skip("optional cross-target PE control; native Windows tests require lld-link")
	}
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "consumer.c")
			if err := os.WriteFile(source, []byte(directoryConsumer), 0600); err != nil {
				t.Fatal(err)
			}
			object := compile(t, source, source+".obj", "--target="+target, "-O2")
			for _, repeated := range []bool{false, true} {
				for _, shape := range boundDirectoryShapes {
					t.Run(fmt.Sprintf("repeated_%t/%s", repeated, shape.name), func(t *testing.T) {
						data, _ := coffDirectoryAssembly(t, target, importDirectoryAssemblyOptions(target, directoryGroups(), []int{1, 0}, shape.omit, shape.layout(repeated)))
						temporary := t.TempDir()
						imports := filepath.Join(temporary, "imports.obj")
						output := filepath.Join(temporary, "control.dll")
						if err := os.WriteFile(imports, data, 0600); err != nil {
							t.Fatal(err)
						}
						linkDirectoryControl(t, target, output, imports, object)
						checkBoundDirectoryControl(t, target, output, shape.layout(repeated), []int{1, 0})
					})
				}
			}
		})
	}
}

// A stale cache must force the independent OS loader to resolve the ILT too.
func checkBoundProviders(t *testing.T, dir string, layout directoryLayout) {
	t.Helper()
	for i, group := range directoryGroups() {
		timestamp := layout.timestamp(i)
		if timestamp == 0 {
			continue
		}
		file, err := pe.Open(filepath.Join(dir, group.dll))
		if err != nil {
			t.Fatal(err)
		}
		actual := file.TimeDateStamp
		file.Close()
		if actual == timestamp {
			t.Fatal("fixture cache unexpectedly matches provider timestamp", group.dll, actual)
		}
	}
}

func TestNativeCOFFBoundImports(t *testing.T) {
	for _, repeated := range []bool{false, true} {
		for _, shape := range boundDirectoryShapes {
			t.Run(fmt.Sprintf("repeated_%t/%s", repeated, shape.name), func(t *testing.T) { testNativeCOFFImportDirectoryLayout(t, shape.omit, shape.layout(repeated)) })
		}
	}
}
func TestNativeCOFFBoundImportMixed(t *testing.T) {
	for _, repeated := range []bool{false, true} {
		for _, shape := range []boundDirectoryShape{boundDirectoryShapes[2], boundDirectoryShapes[4]} {
			t.Run(fmt.Sprintf("repeated_%t/%s", repeated, shape.name), func(t *testing.T) { testNativeCOFFImportDirectoryMixedLayout(t, shape.omit, shape.layout(repeated)) })
		}
	}
}
