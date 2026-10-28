package dylib

import (
	"debug/pe"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

var iatLookupShapes = []struct {
	name string
	omit []bool
}{
	{"absent", []bool{true, true}},
	{"first_absent", []bool{true, false}},
	{"second_absent", []bool{false, true}},
}

func stagedIATLookup(t *testing.T, target string) *object {
	t.Helper()
	assembly := importDirectoryAssemblyTables(target, directoryGroups(), nil, []bool{true, true})
	_, o := coffAssembly(t, target, strings.Replace(assembly, ".idata$2", ".unused2", 1))
	for _, s := range o.sections {
		if s != nil && s.name == ".unused2" {
			s.name = ".idata$2"
		}
	}
	return o
}

func TestCOFFImportDirectoryIATLookupTargets(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		for _, shape := range iatLookupShapes {
			for _, reverse := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/reverse_%t", target, shape.name, reverse), func(t *testing.T) {
					order := []int{0, 1}
					if reverse {
						order = []int{1, 0}
					}
					_, control := coffAssembly(t, target, importDirectoryAssemblyTables(target, directoryGroups(), order, nil))
					_, o := coffAssembly(t, target, importDirectoryAssemblyTables(target, directoryGroups(), order, shape.omit))
					if len(o.info.Unsupported) != 0 || len(o.info.Imports) != 5 || !reflect.DeepEqual(o.info.Imports, control.info.Imports) {
						t.Fatal("IAT lookup changed DLL/name/ordinal identities", o.info, control.info.Imports)
					}
				})
			}
		}
		t.Run(target+"/copy_on_write", func(t *testing.T) {
			o := stagedIATLookup(t, target)
			directory := o.sections[directorySection(t, o, ".idata$2")]
			// Every stored RVA in the first row is zero: only the name/IAT
			// relocations distinguish this valid descriptor from a terminator.
			if !allZero(directory.data[:20]) {
				t.Fatal("fixture must exercise a zero-byte relocated descriptor")
			}
			before := copyDirectoryObject(o)
			converted, ok := newCOFFImportGraph([]*object{o}).convert(o)
			if !ok || len(converted.info.Imports) != 5 || !reflect.DeepEqual(o, before) {
				t.Fatal("conversion lost imports or mutated staged input", ok)
			}
		})
		t.Run(target+"/empty_optional_ilt", func(t *testing.T) {
			assembly := importDirectoryAssemblyTables(target, directoryGroups(), nil, []bool{true, true}) + ".section .idata$4,\"dr\"\n"
			_, o := coffAssembly(t, target, assembly)
			if len(o.info.Imports) != 5 || len(o.info.Unsupported) != 0 {
				t.Fatal(o.info)
			}
		})
		t.Run(target+"/const_alias", func(t *testing.T) {
			name := "first_value"
			if strings.HasPrefix(target, "i686") {
				name = "_" + name
			}
			assembly := importDirectoryAssemblyTables(target, directoryGroups(), nil, []bool{true, true}) + ".global " + name + "\n.set " + name + ",__imp_" + name + "\n"
			_, o := coffAssembly(t, target, assembly)
			if len(o.info.Imports) != 5 || len(o.info.Unsupported) != 0 || o.info.Imports[1].Kind != "const" {
				t.Fatal("const IAT alias lost", o.info)
			}
		})
		for _, comdat := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/mixed_%t", target, comdat), func(t *testing.T) {
				assembly := importDirectoryAssemblyTables(target, directoryGroups(), nil, []bool{true, true}) + directoryMixedAssembly(target, comdat)
				_, o := coffAssembly(t, target, assembly)
				if len(o.info.Imports) != 5 || len(o.info.Unsupported) != 0 {
					t.Fatal(o.info)
				}
				if comdat && len(o.groups) != 4 {
					t.Fatal("retained COMDAT groups lost", o.groups)
				}
			})
		}
	}
}

// Verify the system linker preserved the deliberately absent OriginalFirstThunk
// values. Native system_library cases then exercise Windows' own IAT lookup.
func checkDirectoryLookupRVAs(t *testing.T, path string, absent []bool) {
	t.Helper()
	// debug/pe.ImportedSymbols stops at a zero OriginalFirstThunk. LLVM's
	// independent PE reader inspects the unbound IAT, including ordinal rows.
	output, err := exec.Command("llvm-readobj", "--coff-imports", path).CombinedOutput()
	if err != nil {
		t.Fatalf("independent PE import reader: %v\n%s", err, output)
	}
	for _, text := range []string{"Name: first.dll", "Name: second.dll", "Symbol: add", "Symbol: value", "(7)"} {
		if !strings.Contains(string(output), text) {
			t.Fatalf("missing %q in independent PE imports\n%s", text, output)
		}
	}
	if strings.Count(string(output), "Symbol: add") != 2 || strings.Count(string(output), "Symbol: value") != 2 {
		t.Fatalf("independent PE reader lost same-name imports from different DLLs\n%s", output)
	}
	f, err := pe.Open(path)
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
		t.Fatal("missing PE optional header")
	}
	for _, section := range f.Sections {
		if rva < section.VirtualAddress || uint64(rva-section.VirtualAddress)+uint64(len(absent))*20+20 > uint64(section.Size) {
			continue
		}
		data, err := section.Data()
		if err != nil {
			t.Fatal(err)
		}
		data = data[rva-section.VirtualAddress:]
		for i, zero := range absent {
			if (le.Uint32(data[i*20:]) == 0) != zero {
				t.Fatal("system linker changed lookup-table presence", i, zero)
			}
		}
		if !allZero(data[len(absent)*20 : len(absent)*20+20]) {
			t.Fatal("missing explicit descriptor terminator")
		}
		return
	}
	t.Fatal("import directory outside file sections")
}

func TestCOFFImportDirectoryIATLookupSystemControl(t *testing.T) {
	if _, err := exec.LookPath("lld-link"); err != nil {
		t.Skip("optional cross-target PE control; native Windows tests require lld-link")
	}
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		for _, shape := range iatLookupShapes {
			t.Run(target+"/"+shape.name, func(t *testing.T) {
				b, _ := coffAssembly(t, target, importDirectoryAssemblyTables(target, directoryGroups(), nil, shape.omit))
				dir := t.TempDir()
				imports, source, output := filepath.Join(dir, "imports.obj"), filepath.Join(dir, "consumer.c"), filepath.Join(dir, "control.dll")
				if err := os.WriteFile(imports, b, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(source, []byte(directoryConsumer), 0600); err != nil {
					t.Fatal(err)
				}
				consumer := compile(t, source, source+".obj", "--target="+target, "-O2")
				linkDirectoryControl(t, target, output, imports, consumer)
				checkDirectoryLookupRVAs(t, output, shape.omit)
			})
		}
	}
}

func TestNativeCOFFImportDirectoryIATLookup(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native import directories require Windows")
	}
	for _, shape := range iatLookupShapes {
		t.Run(shape.name, func(t *testing.T) { testNativeCOFFImportDirectory(t, shape.omit, false) })
	}
}

func TestNativeCOFFImportDirectoryIATLookupMixed(t *testing.T) {
	testNativeCOFFImportDirectoryMixed(t, []bool{true, true}, false)
}
