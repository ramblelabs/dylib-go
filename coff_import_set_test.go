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

var explicitDirectoryShapes = []componentDirectoryShape{
	{name: "detached_null", multi: true, detachedNull: true},
	{name: "detached_bound", multi: true, detachedNull: true, timestamps: []uint32{1, 0x12345678}},
}

func TestCOFFExplicitImportDirectoryTargets(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		for _, shape := range explicitDirectoryShapes {
			t.Run(target+"/"+shape.name, func(t *testing.T) {
				parts, original := componentImportParts(t, target, shape, directoryMixedAssembly(target, true), false)
				before := make([]*object, len(original))
				for i, o := range original {
					before[i] = copyDirectoryObject(o)
				}
				// Ordinary Load cannot infer this terminator from proximity.
				ordinary, _ := prepareCOFFImportInputs(original, nil)
				imports := 0
				for _, o := range ordinary {
					imports += len(o.info.Imports)
				}
				if imports != 0 {
					t.Fatal("unconnected terminator was implicitly consumed", imports)
				}
				for _, reverse := range []bool{false, true} {
					for _, big := range []bool{false, true} {
						t.Run(fmt.Sprintf("reverse_%t/bigobj_%t", reverse, big), func(t *testing.T) {
							data := append([][]byte(nil), parts...)
							if reverse {
								for i, j := 0, len(data)-1; i < j; i, j = i+1, j-1 {
									data[i], data[j] = data[j], data[i]
								}
							}
							if big {
								for i, b := range data {
									data[i] = promoteBigCOFF(t, b)
								}
							}
							paths := writeComponentObjects(t, t.TempDir(), data)
							files, absolute, err := readCOFFImportDirectory(paths)
							if err != nil {
								t.Fatal(err)
							}
							var selected []*object
							var infos []ImportInfo
							groups := 0
							for i, f := range files {
								o := f.obj
								if len(o.info.Unsupported) != 0 || o.directory != filepath.Dir(absolute[i]) || o.info.Name != absolute[i] {
									t.Fatal("lost source identity or unsupported projection", o.info, o.directory)
								}
								infos = append(infos, o.info.Imports...)
								groups += len(o.groups)
								selected = append(selected, o)
							}
							if len(infos) != 5 || groups != 4 {
								t.Fatal("imports or ordinary COMDAT/lifecycle groups lost", infos, groups)
							}
							firstDLL := "first.dll"
							if reverse {
								firstDLL = "second.dll"
							}
							if infos[0].DLL != firstDLL {
								t.Fatal("descriptor order differs from explicit input order", infos)
							}
							_, staged := prepareCOFFImportInputs(selected, nil)
							if len(staged) != 0 {
								t.Fatal("explicit objects became archive candidates")
							}
							for i, path := range paths {
								b, err := os.ReadFile(path)
								if err != nil || !reflect.DeepEqual(b, data[i]) {
									t.Fatal("composition changed source bytes", err)
								}
							}
							assertImportDefinitions(t, selected)
						})
					}
				}
				for i, o := range original {
					if !reflect.DeepEqual(o, before[i]) {
						t.Fatal("failed implicit composition mutated input", i)
					}
				}
			})
		}
	}
}

func assertImportDefinitions(t *testing.T, objects []*object) {
	t.Helper()
	defs, err := definitions(objects)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "__imp_first", "second", "__imp_second", "numbered", "__imp_first_value", "__imp_second_value", "mixed", "mixed_read", "mixed_dependency"} {
		d, ok := defs[name]
		if !ok || d.sym().name != name {
			t.Fatal("lost original import/ordinary definition", name)
		}
	}
	for name := range defs {
		if strings.HasPrefix(name, "directory") || strings.HasPrefix(name, "hint") || strings.HasPrefix(name, "ilt") || strings.HasPrefix(name, "dll") {
			t.Fatal("metadata helper exposed", name)
		}
	}
}

func TestCOFFExplicitImportDirectoryTransaction(t *testing.T) {
	parts, _ := componentImportParts(t, "x86_64-pc-windows-msvc", explicitDirectoryShapes[0], "", false)
	paths := writeComponentObjects(t, t.TempDir(), parts)
	dir := t.TempDir()
	archive := filepath.Join(dir, "archive.lib")
	text := filepath.Join(dir, "text.obj")
	if err := os.WriteFile(archive, longImportArchive(parts), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(text, []byte("not an object"), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		paths []string
	}{
		{"empty", nil},
		{"nul", []string{"\x00"}},
		{"duplicate", append(append([]string{}, paths...), paths[0])},
		{"missing", append(append([]string{}, paths...), filepath.Join(dir, "absent.obj"))},
		{"archive", []string{archive}},
		{"bad_object", append(append([]string{}, paths...), text)},
		{"incomplete", paths[:len(paths)-1]},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New(Options{})
			defer s.Close()
			if err := s.LoadCOFFImportDirectory(c.paths...); err == nil {
				t.Fatal("invalid bounded directory accepted")
			}
			if len(s.files) != 0 || len(s.paths) != 0 || len(s.libs) != 0 || s.image != nil {
				t.Fatal("failed transaction changed session")
			}
		})
	}
	t.Run("closed", func(t *testing.T) {
		s := New(Options{})
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := s.LoadCOFFImportDirectory(paths...); err != ErrClosed {
			t.Fatal(err)
		}
	})
}

func TestCOFFExplicitImportDirectoryCompleteObject(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			b, control := coffDirectoryAssembly(t, target, importDirectoryAssembly(target, directoryGroups()))
			paths := writeComponentObjects(t, t.TempDir(), [][]byte{b})
			files, _, err := readCOFFImportDirectory(paths)
			if err != nil || len(files) != 1 {
				t.Fatal("complete standalone directory rejected", err)
			}
			if !reflect.DeepEqual(files[0].obj.info.Imports, control.info.Imports) {
				t.Fatal("standalone directory changed identity", files[0].obj.info.Imports, control.info.Imports)
			}
			s := New(Options{})
			defer s.Close()
			err = s.LoadCOFFImportDirectory(paths...)
			matching := runtime.GOOS == "windows" && runtime.GOARCH == control.info.Arch
			if matching {
				if err != nil || len(s.files) != 1 || len(s.paths) != 1 || len(s.libs) != 0 || s.image != nil {
					t.Fatal("complete directory was not staged without DLL loading", err)
				}
			} else if err == nil || len(s.files) != 0 || len(s.paths) != 0 || len(s.libs) != 0 {
				t.Fatal("foreign target changed session", err)
			}
			// Adding even a valid disconnected null must not disappear through
			// per-file normalization of this already complete directory.
			null, _ := coffDirectoryAssembly(t, target, ".section .idata$3,\"dw\"\n.p2align 2\n.zero 20\n")
			paths = writeComponentObjects(t, t.TempDir(), [][]byte{b, null})
			if _, _, err := readCOFFImportDirectory(paths); err == nil {
				t.Fatal("duplicate complete-directory terminator accepted")
			}
		})
	}
}

func TestCOFFExplicitImportDirectoryValidation(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			parts, _ := componentImportParts(t, target, explicitDirectoryShapes[0], "", false)
			for _, shape := range []struct{ name, assembly string }{
				{"nonzero", ".section .idata$3,\"dw\"\n.p2align 2\n.zero 19\n.byte 1\n"},
				{"oversized", ".section .idata$3,\"dw\"\n.p2align 2\n.zero 40\n"},
				{"overaligned", ".section .idata$3,\"dw\"\n.p2align 4\n.zero 20\n"},
				{"executable", ".section .idata$3,\"xr\"\n.p2align 2\n.zero 20\n"},
				{"live_reference", ".section .idata$3,\"dw\"\n.p2align 2\n.global null_boundary\nnull_boundary:\n.zero 20\n.section .data$ref,\"dw\"\n.rva null_boundary\n"},
			} {
				t.Run(shape.name, func(t *testing.T) {
					b, _ := coffDirectoryAssembly(t, target, shape.assembly)
					bad := append([][]byte{}, parts...)
					bad[5] = b // The detached .idata$3 owner, before the second descriptor.
					paths := writeComponentObjects(t, t.TempDir(), bad)
					if _, _, err := readCOFFImportDirectory(paths); err == nil {
						t.Fatal("invalid bounded directory accepted")
					}
					for i, path := range paths {
						after, err := os.ReadFile(path)
						if err != nil || !reflect.DeepEqual(after, bad[i]) {
							t.Fatal("failed composition changed input", err)
						}
					}
				})
			}
		})
	}
}

func TestCOFFExplicitImportDirectorySystemControl(t *testing.T) {
	if _, err := exec.LookPath("lld-link"); err != nil {
		t.Skip("optional cross-target PE control; Windows native tests require lld-link")
	}
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse_%t", target, reverse), func(t *testing.T) {
				shape := explicitDirectoryShapes[1]
				parts, _ := componentImportParts(t, target, shape, "", reverse)
				dir := t.TempDir()
				paths := writeComponentObjects(t, dir, parts)
				source := filepath.Join(dir, "consumer.c")
				if err := os.WriteFile(source, []byte(directoryConsumer), 0600); err != nil {
					t.Fatal(err)
				}
				consumer := compile(t, source, source+".obj", "--target="+target, "-O2")
				output := filepath.Join(dir, "control.dll")
				linkDirectoryInputs(t, target, output, paths, consumer)
				order := []int{0, 1}
				if reverse {
					order = []int{1, 0}
				}
				checkBoundDirectoryControl(t, target, output, shape.layout(), order)
			})
		}
	}
}

func TestNativeCOFFExplicitImportDirectory(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native DLL imports require Windows")
	}
	for _, shape := range explicitDirectoryShapes {
		t.Run(shape.name, func(t *testing.T) {
			testNativeCOFFImportDirectoryComponent(t, shape.omit, shape.layout(), &shape)
		})
	}
}

func TestNativeCOFFExplicitImportDirectoryMixed(t *testing.T) {
	shape := explicitDirectoryShapes[1]
	testNativeCOFFImportDirectoryMixedComponent(t, shape.omit, shape.layout(), &shape)
}
