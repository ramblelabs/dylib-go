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
	linkDirectoryControl(t, target, output, imports, consumer)
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

func linkDirectoryControl(t *testing.T, target, output, imports, consumer string) {
	linkDirectoryInputs(t, target, output, []string{imports}, consumer)
}

func linkDirectoryInputs(t *testing.T, target, output string, imports []string, consumer string) {
	t.Helper()
	machine := "X64"
	if strings.HasPrefix(target, "aarch64") {
		machine = "ARM64"
	}
	if strings.HasPrefix(target, "i686") {
		machine = "X86"
	}
	args := []string{"/dll", "/noentry", "/nodefaultlib", "/machine:" + machine, "/out:" + output, "/export:directory_imported"}
	args = append(args, imports...)
	args = append(args, consumer)
	command(t, "lld-link", args...)
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
	return importDirectoryAssemblyTables(target, groups, order, nil)
}

func importDirectoryAssemblyTables(target string, groups []directoryFixture, order []int, omit []bool) string {
	return importDirectoryAssemblyLayout(target, groups, order, omit, false)
}

type directoryLayout struct {
	repeated                               bool
	fragments, separateNull, publicHelpers bool
	timestamps, forwarders                 []uint32
}

func (layout directoryLayout) timestamp(group int) uint32 {
	if group < len(layout.timestamps) {
		return layout.timestamps[group]
	}
	return 0
}
func (layout directoryLayout) forwarder(group int) uint32 {
	if group < len(layout.forwarders) {
		return layout.forwarders[group]
	}
	return 0
}

func directoryCachedAddress(target string, group, slot int) uint64 {
	base := uint64(0xfedcba9800000000)
	if strings.HasPrefix(target, "i686") {
		base = 0xfedc0000
	}
	return base + uint64(group*0x100+slot+1)
}

func directoryCachedWord(target string, group, slot int) string {
	word := ".quad"
	if strings.HasPrefix(target, "i686") {
		word = ".long"
	}
	return fmt.Sprintf("%s 0x%x\n", word, directoryCachedAddress(target, group, slot))
}

func importDirectoryAssemblyLayout(target string, groups []directoryFixture, order []int, omit []bool, repeated bool) string {
	return importDirectoryAssemblyOptions(target, groups, order, omit, directoryLayout{repeated: repeated})
}

func importDirectoryAssemblyOptions(target string, groups []directoryFixture, order []int, omit []bool, layout directoryLayout) string {
	var text strings.Builder
	if strings.HasPrefix(target, "i686") {
		// These leaf import thunks install no exception handlers.
		text.WriteString(".def @feat.00; .scl 3; .type 0; .endef\n.set @feat.00,1\n")
	}
	if !layout.fragments {
		text.WriteString(".section .idata$2,\"dr\"\n")
	}
	if len(order) == 0 {
		for i := range groups {
			order = append(order, i)
		}
	}
	for _, i := range order {
		if layout.fragments {
			fmt.Fprintf(&text, ".section .id%03d$2,\"dr\"\n", i+1)
		}
		if layout.publicHelpers {
			fmt.Fprintf(&text, ".global directory%d\ndirectory%d:\n", i, i)
		}
		if i < len(omit) && omit[i] {
			text.WriteString(".long 0\n")
		} else {
			fmt.Fprintf(&text, ".rva ilt%d\n", i)
		}
		fmt.Fprintf(&text, ".long %d,%d\n.rva dll%d\n.rva iat%d\n", layout.timestamp(i), layout.forwarder(i), i, i)
	}
	if layout.separateNull {
		text.WriteString(".section .idata$3,\"dr\"\n")
	}
	if layout.publicHelpers {
		text.WriteString(".global directory_end\ndirectory_end:\n")
	}
	text.WriteString(".zero 20\n")
	word := ".quad 0\n"
	if strings.HasPrefix(target, "i686") {
		word = ".long 0\n"
	}
	for i, group := range groups {
		var part strings.Builder
		missing := i < len(omit) && omit[i]
		if !missing {
			fmt.Fprintf(&part, ".section .idata$4,\"dr\"\n.balign 8\nilt%d:\n", i)
		}
		fmt.Fprintf(&part, ".section .idata$5,\"dr\"\n.balign 8\niat%d:\n", i)
		for j, entry := range group.entries {
			hint := fmt.Sprintf("hint%d_%d", i, j)
			words := []string{}
			if layout.timestamp(i) != 0 {
				words = append(words, directoryCachedWord(target, i, j))
			}
			body := strings.NewReplacer(".rva hint\n", ".rva "+hint+"\n", "hint:\n", hint+":\n", ".rva dll_head\n", "").Replace(longImportAssembly(target, entry, words...))
			if missing {
				start := strings.Index(body, ".section .idata$4,")
				end := strings.Index(body, ".section .idata$5,")
				body = body[:start] + body[end:]
			}
			part.WriteString(body)
		}
		if !missing {
			part.WriteString(".section .idata$4,\"dr\"\n" + word)
		}
		part.WriteString(".section .idata$5,\"dr\"\n" + word)
		part.WriteString(".section .idata$7,\"dr\"\n")
		if layout.publicHelpers {
			fmt.Fprintf(&part, ".global dll%d\n", i)
		}
		fmt.Fprintf(&part, "dll%d:\n.asciz %q\n", i, group.dll)
		body := part.String()
		if layout.repeated {
			// Older assemblers cannot spell repeated ordinary COFF sections.
			// Compile distinct fixed-width names; coffDirectoryAssembly restores
			// their names without changing section or relocation indexes.
			for _, name := range []string{".idata$4", ".idata$5", ".idata$6", ".idata$7"} {
				body = strings.ReplaceAll(body, fmt.Sprintf(".section %s,\"dr\"\n", name), fmt.Sprintf(".section .id%03d$%c,\"dr\"\n", i+1, name[len(name)-1]))
			}
		}
		text.WriteString(body)
	}
	return text.String()
}

// coffDirectoryAssembly creates genuine repeated ordinary sections using only
// old assembler syntax. The independent LLD/PE controls consume these same bytes.
func coffDirectoryAssembly(t *testing.T, target, assembly string) ([]byte, *object) {
	t.Helper()
	data, original := coffAssembly(t, target, assembly)
	input, err := readCOFF(data)
	if err != nil || input.big || input.kind != "object" || le.Uint16(data[16:]) != 0 {
		t.Fatalf("ordinary fixture: %v", err)
	}
	renamed := make(map[int]string)
	for i, sec := range input.sections {
		name := sec.name
		if len(name) != 8 || !strings.HasPrefix(name, ".id") || name[6] != '$' || name[7] < '2' || name[7] > '7' {
			continue
		}
		if name[3] < '0' || name[3] > '9' || name[4] < '0' || name[4] > '9' || name[5] < '0' || name[5] > '9' {
			continue
		}
		renamed[i+1] = ".idata$" + name[7:]
		copy(data[20+i*40:20+i*40+8], renamed[i+1])
	}
	if len(renamed) == 0 {
		return data, original
	}
	for i := 0; i < len(input.records)/input.entrySize; {
		symbol := input.symbol(i)
		if name := renamed[int(symbol.section)]; name != "" && symbol.class == 3 && symbol.value == 0 && symbol.aux != 0 && string(symbol.name[:]) == input.sections[symbol.section-1].name {
			copy(input.record(i)[:8], name)
		}
		i += int(symbol.aux) + 1
	}
	file, err := parse("directory.obj", data)
	if err != nil {
		t.Fatal(err)
	}
	return data, file.obj
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
	testNativeCOFFImportDirectory(t, nil, false)
}

func testNativeCOFFImportDirectory(t *testing.T, omit []bool, repeated bool) {
	testNativeCOFFImportDirectoryLayout(t, omit, directoryLayout{repeated: repeated})
}

func testNativeCOFFImportDirectoryLayout(t *testing.T, omit []bool, layout directoryLayout) {
	testNativeCOFFImportDirectoryComponent(t, omit, layout, nil)
}

func testNativeCOFFImportDirectoryComponent(t *testing.T, omit []bool, layout directoryLayout, component *componentDirectoryShape) {
	t.Helper()
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
	if len(layout.timestamps) != 0 {
		checkBoundProviders(t, dir, layout)
	}
	target := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH] + "-w64-windows-gnu"
	imports, _ := coffDirectoryAssembly(t, target, importDirectoryAssemblyOptions(target, directoryGroups(), []int{1, 0}, omit, layout))
	parts := [][]byte{imports}
	if component != nil {
		parts, _ = componentImportParts(t, target, *component, "", false)
	}
	paths := writeComponentObjects(t, dir, parts)
	var crossPaths []string
	if component != nil && component.crossArchive {
		crossPaths = crossImportArchives(t, dir, parts)
	}
	obj := paths[0]
	archive := filepath.Join(dir, "directory.lib")
	archiveParts := append([][]byte{}, parts...)
	if component != nil {
		poison := *component
		poison.namespace = "missing_"
		unused, _ := componentImportParts(t, target, poison, "", false)
		archiveParts = append(archiveParts, unused...)
	}
	if err := os.WriteFile(archive, longImportArchive(archiveParts), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "consumer.c")
	if err := os.WriteFile(source, []byte(directoryConsumer), 0600); err != nil {
		t.Fatal(err)
	}
	for _, opt := range []string{"-O0", "-O2"} {
		consumer := compile(t, source, source+opt+".obj", "--target="+target, opt)
		control := filepath.Join(dir, "control"+opt+".dll")
		if len(crossPaths) != 0 {
			linkDirectoryInputs(t, target, control, []string{"/wholearchive:" + crossPaths[0], "/wholearchive:" + crossPaths[1]}, consumer)
			checkBoundDirectoryControl(t, target, control, layout, []int{1, 0})
		} else if component != nil {
			linkDirectoryInputs(t, target, control, paths, consumer)
			checkBoundDirectoryControl(t, target, control, layout, []int{1, 0})
		} else if len(layout.timestamps) != 0 {
			linkDirectoryControl(t, target, control, obj, consumer)
			checkBoundDirectoryControl(t, target, control, layout, []int{1, 0})
		} else if len(omit) == 0 {
			directoryControl(t, target, control, obj, consumer)
		} else {
			linkDirectoryControl(t, target, control, obj, consumer)
			checkDirectoryLookupRVAs(t, control, []bool{omit[1], omit[0]})
		}
		kinds := []string{"object", "archive", "system_library"}
		if len(crossPaths) != 0 {
			kinds = []string{"archives", "mixed_inputs", "retry", "system_library"}
		}
		for _, kind := range kinds {
			t.Run(opt+"/"+kind, func(t *testing.T) {
				opts := Options{}
				if len(crossPaths) != 0 {
					opts.LibraryPaths = []string{dir}
				}
				s := New(opts)
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
				if len(crossPaths) != 0 {
					// Reload independent snapshots for every case; retry drops a
					// backing archive before the remaining provider is staged.
					inputs := crossImportArchives(t, t.TempDir(), parts)
					if kind == "mixed_inputs" {
						load(t, s, paths[0], inputs[1], consumer)
						// The other members of the first archive remain explicit
						// raw objects in this mixed ownership case.
						for i := 2; i < len(paths); i += 2 {
							load(t, s, paths[i])
						}
					} else {
						load(t, s, inputs[1], consumer)
						if kind == "retry" {
							if err := s.Link("first"); err == nil || s.image != nil || len(s.libs) != 0 {
								t.Fatal("incomplete cross-archive component published", err)
							}
							if err := os.Remove(inputs[1]); err != nil {
								t.Fatal(err)
							}
						}
						load(t, s, inputs[0])
					}
				} else if component != nil && kind == "object" {
					stage := filepath.Join(t.TempDir(), "staged.obj")
					if err := os.WriteFile(stage, parts[0], 0600); err != nil {
						t.Fatal(err)
					}
					load(t, s, stage)
					if err := s.Link("first"); err == nil || s.image != nil || len(s.libs) != 0 {
						t.Fatal("incomplete component published or opened DLLs", err)
					}
					if err := os.Remove(stage); err != nil {
						t.Fatal(err)
					}
					load(t, s, append(paths[1:], consumer)...)
				} else {
					load(t, s, input, consumer)
				}
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
