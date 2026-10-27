package dylib

import (
	"debug/pe"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const directoryConsumer = "__declspec(dllimport) int first(int,int),second(int,int),numbered(int,int);\n__declspec(dllimport) extern int first_value,second_value;\nint directory_imported(int a,int b){return first(a,b)+second(a,b)+numbered(a,b)+first_value+second_value;}\n"

func directoryControl(t *testing.T, target, output, imports, consumer string) {
	t.Helper()
	machine := "X64"
	if strings.HasPrefix(target, "aarch64") {
		machine = "ARM64"
	}
	if strings.HasPrefix(target, "i686") {
		machine = "X86"
	}
	command(t, "lld-link", "/dll", "/noentry", "/nodefaultlib", "/machine:"+machine, "/out:"+output, "/export:directory_imported", imports, consumer)
	f, err := pe.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	names, err := f.ImportedSymbols()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	want := []string{"add:first.dll", "add:second.dll", "value:first.dll", "value:second.dll"}
	if !reflect.DeepEqual(names, want) {
		t.Fatal("system-linked PE imports", names, want)
	}
}

func TestCOFFImportDirectorySystemControl(t *testing.T) {
	if _, err := exec.LookPath("lld-link"); err != nil {
		t.Skip("optional cross-target PE control; native Windows import tests require lld-link")
	}
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			data, _ := coffAssembly(t, target, importDirectoryAssembly(target, directoryGroups()))
			dir := t.TempDir()
			imports := filepath.Join(dir, "imports.obj")
			source := filepath.Join(dir, "consumer.c")
			if err := os.WriteFile(imports, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, []byte(directoryConsumer), 0600); err != nil {
				t.Fatal(err)
			}
			obj := compile(t, source, source+".obj", "--target="+target, "-O2")
			directoryControl(t, target, filepath.Join(dir, "control.dll"), imports, obj)
		})
	}
}

type directoryFixture struct {
	dll     string
	entries []longImportFixture
}

func importDirectoryAssembly(target string, groups []directoryFixture) string {
	return importDirectoryAssemblyOrder(target, groups, nil)
}

func importDirectoryAssemblyOrder(target string, groups []directoryFixture, order []int) string {
	var text strings.Builder
	if strings.HasPrefix(target, "i686") {
		// These leaf import thunks install no exception handlers.
		text.WriteString(".def @feat.00; .scl 3; .type 0; .endef\n.set @feat.00,1\n")
	}
	text.WriteString(".section .idata$2,\"dr\"\n")
	if len(order) == 0 {
		for i := range groups {
			order = append(order, i)
		}
	}
	for _, i := range order {
		fmt.Fprintf(&text, ".rva ilt%d\n.long 0,0\n.rva dll%d\n.rva iat%d\n", i, i, i)
	}
	text.WriteString(".zero 20\n")
	word := ".quad 0\n"
	if strings.HasPrefix(target, "i686") {
		word = ".long 0\n"
	}
	for i, group := range groups {
		fmt.Fprintf(&text, ".section .idata$4,\"dr\"\n.balign 8\nilt%d:\n.section .idata$5,\"dr\"\n.balign 8\niat%d:\n", i, i)
		for j, entry := range group.entries {
			hint := fmt.Sprintf("hint%d_%d", i, j)
			body := strings.NewReplacer(".rva hint\n", ".rva "+hint+"\n", "hint:\n", hint+":\n", ".rva dll_head\n", "").Replace(longImportAssembly(target, entry))
			text.WriteString(body)
		}
		text.WriteString(".section .idata$4,\"dr\"\n" + word + ".section .idata$5,\"dr\"\n" + word)
		fmt.Fprintf(&text, ".section .idata$7,\"dr\"\ndll%d:\n.asciz %q\n", i, group.dll)
	}
	return text.String()
}

func TestCOFFImportDirectoryOrder(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			_, o := coffAssembly(t, target, importDirectoryAssemblyOrder(target, directoryGroups(), []int{1, 0}))
			want := []string{"second", "numbered", "second_value", "first", "first_value"}
			if len(o.info.Imports) != len(want) {
				t.Fatal(o.info)
			}
			for i, name := range want {
				if o.info.Imports[i].Symbol != name {
					t.Fatal("descriptor order guessed from physical IAT order", o.info.Imports)
				}
			}
		})
	}
}

func directoryGroups() []directoryFixture {
	return []directoryFixture{{"first.dll", []longImportFixture{{"first", "add", 0, false}, {"first_value", "value", 0, true}}},
		{"second.dll", []longImportFixture{{"second", "add", 0, false}, {"numbered", "", 7, false}, {"second_value", "value", 0, true}}}}
}

// Delay the parser's normal eager recognition so validation/COW tests can
// inspect staged input. Native and parser tests also use unchanged objects.
func stagedDirectory(t *testing.T, target string) *object {
	t.Helper()
	assembly := strings.Replace(importDirectoryAssembly(target, directoryGroups()), ".idata$2", ".unused2", 1)
	_, o := coffAssembly(t, target, assembly)
	for _, sec := range o.sections {
		if sec != nil && sec.name == ".unused2" {
			sec.name = ".idata$2"
		}
	}
	return o
}

func TestCOFFImportDirectory(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			_, parsed := coffAssembly(t, target, importDirectoryAssembly(target, directoryGroups()))
			if len(parsed.info.Imports) != 5 || len(parsed.info.Unsupported) != 0 {
				t.Fatal("parser did not recognize unmodified object", parsed.info)
			}
			o := stagedDirectory(t, target)
			symbols, relocs := append([]symbol(nil), o.symbols...), append([]relocation(nil), o.relocs...)
			converted, ok := newCOFFImportGraph([]*object{o}).convert(o)
			if !ok {
				t.Fatal("self-contained import directory was not recognized")
			}
			if len(converted.info.Imports) != 5 || len(converted.info.Unsupported) != 0 || len(converted.relocs) != 0 {
				t.Fatal(converted.info, converted.relocs)
			}
			for i, entry := range converted.info.Imports {
				dll := "first.dll"
				if i >= 2 {
					dll = "second.dll"
				}
				if entry.DLL != dll {
					t.Fatal("descriptor-qualified DLL", entry)
				}
			}
			if converted.info.Imports[3].Ordinal != 7 {
				t.Fatal("ordinal import lost")
			}
			if len(converted.symbols) != len(o.symbols) {
				t.Fatal("original symbol indices changed")
			}
			if !reflect.DeepEqual(symbols, o.symbols) || !reflect.DeepEqual(relocs, o.relocs) {
				t.Fatal("conversion mutated staged input")
			}
			for _, s := range converted.symbols {
				if s.imported != nil && (!s.global || s.section != -4 || s.imported.entry.dll == "") {
					t.Fatal("invalid import identity", s)
				}
			}
		})
	}
}

func TestNativeCOFFImportDirectory(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native DLL imports require Windows")
	}
	needNative(t)
	dll, _, _ := nativeImportFixture(t)
	dir := filepath.Dir(dll)
	data, err := os.ReadFile(dll)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "first.dll"), data, 0600); err != nil {
		t.Fatal(err)
	}
	secondary := filepath.Join(dir, "secondary.c")
	if err := os.WriteFile(secondary, []byte("int add(int a,int b){return a+b+1000;} int value=84;"), 0600); err != nil {
		t.Fatal(err)
	}
	secondaryObject := compile(t, secondary, secondary+".obj")
	definition := filepath.Join(dir, "second.def")
	if err := os.WriteFile(definition, []byte("EXPORTS\nadd @7\nvalue DATA\n"), 0600); err != nil {
		t.Fatal(err)
	}
	command(t, "lld-link", "/dll", "/noentry", "/nodefaultlib", "/def:"+definition, "/out:"+filepath.Join(dir, "second.dll"), secondaryObject)
	target := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH] + "-w64-windows-gnu"
	imports, _ := coffAssembly(t, target, importDirectoryAssemblyOrder(target, directoryGroups(), []int{1, 0}))
	obj := filepath.Join(dir, "directory.obj")
	if err := os.WriteFile(obj, imports, 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "directory.lib")
	if err := os.WriteFile(archive, longImportArchive([][]byte{imports}), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "consumer.c")
	if err := os.WriteFile(source, []byte(directoryConsumer), 0600); err != nil {
		t.Fatal(err)
	}
	for _, opt := range []string{"-O0", "-O2"} {
		consumer := compile(t, source, source+opt+".obj", "--target="+target, opt)
		control := filepath.Join(dir, "control"+opt+".dll")
		directoryControl(t, target, control, obj, consumer)
		for _, kind := range []string{"object", "archive", "system_library"} {
			t.Run(opt+"/"+kind, func(t *testing.T) {
				s := New(Options{})
				defer s.Close()
				input := obj
				if kind == "archive" {
					input = archive
				}
				if kind == "system_library" {
					// LoadLibrary's dependency search does not inherit our raw
					// import file's directory. Keep both exact providers loaded
					// before the OS independently binds the control's PE tables.
					load(t, s, filepath.Join(dir, "first.dll"), filepath.Join(dir, "second.dll"), control)
					call(t, s, "directory_imported", 20, 22, 2252)
					return
				}
				load(t, s, input, consumer)
				if err := s.Link("directory_imported", "first", "second", "numbered"); err != nil {
					t.Fatal(err)
				}
				for _, entry := range []struct {
					name string
					want int32
				}{{"directory_imported", 2252}, {"first", 42}, {"second", 1042}, {"numbered", 1042}} {
					call(t, s, entry.name, 20, 22, entry.want)
				}
				if len(s.libs) != 2 {
					t.Fatal("expected independently retained DLLs", len(s.libs))
				}
			})
		}
	}
}
