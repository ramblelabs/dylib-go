package dylib

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Every object has the same member name. Opaque unselected payloads and an
// index/name table make index-based selection observable without ranlib lookup.
func importSourceArchive(parts [][]byte, format string) ([]byte, []int) {
	name := "duplicated_long_import_directory_member.obj"
	index := "/"
	indexData := make([]byte, 4)
	if format == "bsd" {
		index, indexData = "__.SYMDEF", make([]byte, 8)
	}
	b := append([]byte("!<arch>\n"), archiveMemberBytes(index, indexData)...)
	if format == "coff" {
		// The second linker member distinguishes COFF's NUL name table
		// from GNU's newline-terminated table for independent readers.
		b = append(b, archiveMemberBytes("/", make([]byte, 8))...)
	}
	if format != "bsd" {
		end := "/\n"
		if format == "coff" {
			end = "\x00"
		}
		b = append(b, archiveMemberBytes("//", []byte(name+end))...)
	}
	add := func(data []byte) {
		if format == "bsd" {
			b = append(b, archiveMemberBytes(fmt.Sprintf("#1/%d", len(name)), append([]byte(name), data...))...)
		} else {
			b = append(b, archiveMemberBytes("/0", data)...)
		}
	}
	add([]byte("opaque unselected payload"))
	indices := make([]int, len(parts))
	position := 1
	for i := len(parts) - 1; i >= 0; i-- {
		indices[i] = position
		add(parts[i])
		position++
	}
	add([]byte("another unselected payload"))
	return b, indices
}

// providerDir is deliberately distinct from the descriptor/name source dir.
// Thin members and proxies retain the external IAT owner's DLL-search origin.
func importDirectorySources(t *testing.T, providerDir string, parts [][]byte, kind string) ([]COFFImportSource, []string) {
	t.Helper()
	aux := t.TempDir()
	var cleanup []string
	write := func(path string, b []byte) string {
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		cleanup = append(cleanup, path)
		return path
	}
	archive := func(dir, name, format string, data [][]byte) COFFImportSource {
		b, indices := importSourceArchive(data, format)
		return COFFImportSource{Path: write(filepath.Join(dir, name), b), Members: indices}
	}
	switch kind {
	case "gnu", "coff", "bsd":
		source := archive(providerDir, "selected-"+kind+".a", kind, parts)
		return []COFFImportSource{source}, cleanup
	case "cross":
		iat := [][]byte{parts[3], parts[4]}
		metadata := [][]byte{parts[0], parts[1], parts[2], parts[5], parts[6]}
		a := archive(providerDir, "selected-iat.a", "gnu", iat)
		b := archive(aux, "selected-names.a", "bsd", metadata)
		return []COFFImportSource{a, b}, cleanup
	case "mixed":
		raw := COFFImportSource{Path: write(filepath.Join(aux, "selected-raw.obj"), parts[0])}
		a := archive(providerDir, "selected-rest.a", "coff", parts[1:])
		return []COFFImportSource{raw, a}, cleanup
	case "thin", "proxy":
		var members []thinTestMember
		members = append(members, thinTestMember{name: "unselected-missing.obj", size: 1})
		indices := make([]int, len(parts))
		var proxy []archiveEntry
		var proxyIndices []int
		var proxyPath string
		if kind == "proxy" {
			b, indices := importSourceArchive(parts, "gnu")
			proxyPath = write(filepath.Join(providerDir, "selected-proxy-source.a"), b)
			var err error
			proxy, err = archiveEntries(b, false)
			if err != nil {
				t.Fatal(err)
			}
			proxyIndices = indices
		}
		for i := len(parts) - 1; i >= 0; i-- {
			path := proxyPath
			var origin uint64
			if kind == "thin" {
				path = write(filepath.Join(providerDir, fmt.Sprintf("selected-thin-%d.obj", i)), parts[i])
			} else {
				origin = proxy[proxyIndices[i]].Offset
			}
			name, err := filepath.Rel(aux, path)
			if err != nil {
				t.Fatal(err)
			}
			indices[i] = len(members)
			members = append(members, thinTestMember{name: name, size: uint64(len(parts[i])), origin: origin})
		}
		path := write(filepath.Join(aux, "selected-thin.a"), thinBytes(members...))
		return []COFFImportSource{{Path: path, Members: indices}}, cleanup
	}
	t.Fatal("unknown archive source kind", kind)
	return nil, nil
}

func reverseImportSources(sources []COFFImportSource) []COFFImportSource {
	out := append([]COFFImportSource(nil), sources...)
	for i := range out {
		if out[i].Members != nil {
			out[i].Members = append([]int{}, out[i].Members...)
			for a, b := 0, len(out[i].Members)-1; a < b; a, b = a+1, b-1 {
				out[i].Members[a], out[i].Members[b] = out[i].Members[b], out[i].Members[a]
			}
		}
	}
	for a, b := 0, len(out)-1; a < b; a, b = a+1, b-1 {
		out[a], out[b] = out[b], out[a]
	}
	return out
}

func TestCOFFImportDirectorySourceTargets(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		t.Run(target, func(t *testing.T) {
			parts, _ := componentImportParts(t, target, explicitDirectoryShapes[1], directoryMixedAssembly(target, true), false)
			for _, kind := range []string{"gnu", "coff", "bsd", "cross", "mixed", "thin", "proxy"} {
				for _, big := range []bool{false, true} {
					data := append([][]byte(nil), parts...)
					if big {
						for i, b := range data {
							data[i] = promoteBigCOFF(t, b)
						}
					}
					provider := t.TempDir()
					sources, cleanup := importDirectorySources(t, provider, data, kind)
					for _, reverse := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/bigobj_%t/reverse_%t", kind, big, reverse), func(t *testing.T) {
							inputs := sources
							if reverse {
								inputs = reverseImportSources(inputs)
							}
							files, absolute, err := readCOFFImportDirectorySources(inputs)
							if err != nil {
								t.Fatal(err)
							}
							if len(files) != len(parts) || len(absolute) != len(inputs) {
								t.Fatal("selected source/member cardinality lost", len(files), absolute)
							}
							position := 0
							for _, input := range inputs {
								if input.Members == nil {
									if files[position].obj.info.Name != input.Path {
										t.Fatal("raw source order changed")
									}
									position++
									continue
								}
								for _, index := range input.Members {
									if !strings.HasPrefix(files[position].obj.info.Name, fmt.Sprintf("%s[%d](", input.Path, index)) {
										t.Fatal("archive member selection order changed", files[position].obj.info.Name, index)
									}
									position++
								}
							}
							var objects []*object
							imports, groups := 0, 0
							for _, f := range files {
								o := f.obj
								if len(o.info.Unsupported) != 0 {
									t.Fatal(o.info)
								}
								if len(o.info.Imports) != 0 && o.directory != provider {
									t.Fatal("IAT owner origin changed", o.info.Name, o.directory, provider)
								}
								imports += len(o.info.Imports)
								groups += len(o.groups)
								objects = append(objects, o)
							}
							if imports != 5 || groups != 4 {
								t.Fatal("selected imports/ordinary groups lost", imports, groups)
							}
							assertImportDefinitions(t, objects)
							s := New(Options{})
							defer s.Close()
							err = s.LoadCOFFImportDirectoryFrom(inputs...)
							if runtime.GOOS == "windows" && runtime.GOARCH == objects[0].info.Arch {
								if err != nil || len(s.files) != len(parts) || len(s.paths) != len(inputs) || len(s.libs) != 0 || s.image != nil {
									t.Fatal("selected source staging opened DLLs or lost members", err)
								}
							} else if err == nil || len(s.files) != 0 || len(s.paths) != 0 || len(s.libs) != 0 {
								t.Fatal("foreign selected source changed session", err)
							}
						})
					}
					for _, path := range cleanup {
						if err := os.Remove(path); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
		})
	}
}

func TestCOFFImportDirectorySourceValidation(t *testing.T) {
	parts, _ := componentImportParts(t, "x86_64-pc-windows-msvc", explicitDirectoryShapes[0], "", false)
	sources, _ := importDirectorySources(t, t.TempDir(), parts, "gnu")
	archive := sources[0]
	cases := []struct {
		name   string
		inputs []COFFImportSource
	}{
		{"empty", nil},
		{"duplicate_source", []COFFImportSource{archive, archive}},
		{"no_selection", []COFFImportSource{{Path: archive.Path}}},
		{"empty_selection", []COFFImportSource{{Path: archive.Path, Members: []int{}}}},
		{"negative", []COFFImportSource{{Path: archive.Path, Members: []int{-1}}}},
		{"outside", []COFFImportSource{{Path: archive.Path, Members: []int{100}}}},
		{"duplicate_member", []COFFImportSource{{Path: archive.Path, Members: []int{1, 1}}}},
		{"opaque_member", []COFFImportSource{{Path: archive.Path, Members: []int{0}}}},
		{"incomplete", []COFFImportSource{{Path: archive.Path, Members: archive.Members[:len(archive.Members)-1]}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(Options{})
			defer s.Close()
			if err := s.LoadCOFFImportDirectoryFrom(tc.inputs...); err == nil {
				t.Fatal("invalid selection accepted")
			}
			if len(s.files) != 0 || len(s.paths) != 0 || len(s.libs) != 0 || s.image != nil {
				t.Fatal("failed selected source changed session")
			}
		})
	}
}

func TestCOFFImportDirectorySourceSystemControl(t *testing.T) {
	if _, err := exec.LookPath("lld-link"); err != nil {
		t.Skip("optional cross-target PE control; Windows native tests require lld-link")
	}
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			shape := explicitDirectoryShapes[1]
			parts, _ := componentImportParts(t, target, shape, "", false)
			for _, kind := range []string{"gnu", "coff", "bsd"} {
				t.Run(kind, func(t *testing.T) {
					dir := t.TempDir()
					sources, _ := importDirectorySources(t, dir, parts, kind)
					listing, err := exec.Command("llvm-ar", "t", sources[0].Path).CombinedOutput()
					names := strings.Fields(string(listing))
					if kind == "bsd" && len(names) != 0 && names[0] == "__.SYMDEF" {
						// llvm-ar t exposes this BSD index; it is not an object.
						names = names[1:]
					}
					if err != nil || len(names) != len(parts)+2 {
						t.Fatalf("independent archive member listing: %v\n%s", err, listing)
					}
					for _, name := range names {
						if name != "duplicated_long_import_directory_member.obj" {
							t.Fatal("independent archive member identity changed", name)
						}
					}
					files, _, err := readCOFFImportDirectorySources(sources)
					if err != nil || len(files) != len(parts) {
						t.Fatal(err)
					}
					source := filepath.Join(dir, "consumer.c")
					if err := os.WriteFile(source, []byte(directoryConsumer), 0600); err != nil {
						t.Fatal(err)
					}
					consumer := compile(t, source, source+".obj", "--target="+target, "-O2")
					for _, reverse := range []bool{false, true} {
						t.Run(fmt.Sprintf("reverse_%t", reverse), func(t *testing.T) {
							inputs, materialized, order := sources, append([][]byte(nil), parts...), []int{0, 1}
							if reverse {
								inputs = reverseImportSources(inputs)
								for a, b := 0, len(materialized)-1; a < b; a, b = a+1, b-1 {
									materialized[a], materialized[b] = materialized[b], materialized[a]
								}
								order = []int{1, 0}
							}
							if _, _, err := readCOFFImportDirectorySources(inputs); err != nil {
								t.Fatal(err)
							}
							output := filepath.Join(dir, fmt.Sprintf("control-%t.dll", reverse))
							// LLD sorts .idata chunks by filename. Number freshly
							// materialized objects in the requested selection order.
							objects := writeComponentObjects(t, t.TempDir(), materialized)
							linkDirectoryInputs(t, target, output, objects, consumer)
							checkBoundDirectoryControl(t, target, output, shape.layout(), order)
						})
					}
				})
			}
		})
	}
}

func TestCOFFImportDirectorySourceSnapshots(t *testing.T) {
	parts, _ := componentImportParts(t, "x86_64-pc-windows-msvc", explicitDirectoryShapes[1], "", false)
	for _, kind := range []string{"gnu", "cross", "mixed", "thin", "proxy"} {
		t.Run(kind, func(t *testing.T) {
			sources, paths := importDirectorySources(t, t.TempDir(), parts, kind)
			before := make([][]byte, len(paths))
			for i, path := range paths {
				var err error
				before[i], err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			files, _, err := readCOFFImportDirectorySources(sources)
			if err != nil {
				t.Fatal(err)
			}
			for i, path := range paths {
				after, err := os.ReadFile(path)
				if err != nil || !reflect.DeepEqual(after, before[i]) {
					t.Fatal("selection changed source", err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			imports := 0
			for _, f := range files {
				imports += len(f.obj.info.Imports)
			}
			if imports != 5 {
				t.Fatal("deleted source snapshot lost imports", imports)
			}
		})
	}
}

func TestCOFFImportDirectorySourceExternalBudget(t *testing.T) {
	parts, _ := componentImportParts(t, "x86_64-pc-windows-msvc", explicitDirectoryShapes[0], "", false)
	dir := t.TempDir()
	raw := filepath.Join(dir, "first.obj")
	if err := os.WriteFile(raw, parts[0], 0600); err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(dir, "large.obj")
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(maxFile)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	thin := filepath.Join(dir, "selected.a")
	writeThin(t, thin, thinTestMember{name: "large.obj", size: 1})
	s := New(Options{})
	defer s.Close()
	err = s.LoadCOFFImportDirectoryFrom(COFFImportSource{Path: raw}, COFFImportSource{Path: thin, Members: []int{0}})
	if err == nil || !strings.Contains(err.Error(), "at most") || len(s.files) != 0 || len(s.paths) != 0 || len(s.libs) != 0 {
		t.Fatal("aggregate input budget did not reject external read before parsing", err)
	}
}

func TestNativeCOFFImportDirectorySources(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native import directories require Windows")
	}
	for _, kind := range []string{"gnu", "cross", "mixed", "thin", "proxy"} {
		t.Run(kind, func(t *testing.T) {
			shape := explicitDirectoryShapes[1]
			shape.sourceKind = kind
			testNativeCOFFImportDirectoryComponent(t, shape.omit, shape.layout(), &shape)
		})
	}
}

func TestNativeCOFFImportDirectorySourcesMixed(t *testing.T) {
	shape := explicitDirectoryShapes[1]
	shape.sourceKind = "cross"
	testNativeCOFFImportDirectoryMixedComponent(t, shape.omit, shape.layout(), &shape)
}
