package dylib

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type longImportFixture struct {
	public, export string
	ordinal        uint16
	data           bool
}

func longImportAssembly(target string, entry longImportFixture, cachedWord ...string) string {
	public := entry.public
	if strings.HasPrefix(target, "i686") {
		public = "_" + public
	}
	iat := "__imp_" + public
	text := ""
	if !entry.data {
		text = ".text\n.global " + public + "\n" + public + ":\n"
		switch {
		case strings.HasPrefix(target, "aarch64"):
			text += "adrp x16," + iat + "\nadd x16,x16,:lo12:" + iat + "\nldr x16,[x16]\nbr x16\n"
		case strings.HasPrefix(target, "i686"):
			text += "jmp *" + iat + "\n.byte 0x90,0x90\n"
		default:
			text += "jmp *" + iat + "(%rip)\n.byte 0x90,0x90\n"
		}
	}
	word := ".rva hint\n"
	if entry.ordinal != 0 {
		if strings.HasPrefix(target, "i686") {
			word = fmt.Sprintf(".long 0x80000000+%d\n", entry.ordinal)
		} else {
			word = fmt.Sprintf(".quad 0x8000000000000000+%d\n", entry.ordinal)
		}
	} else if !strings.HasPrefix(target, "i686") {
		word += ".long 0\n"
	}
	text += ".section .idata$4,\"dr\"\n" + word
	if len(cachedWord) != 0 {
		word = cachedWord[0]
	}
	text += ".section .idata$5,\"dr\"\n.global " + iat + "\n" + iat + ":\n" + word
	text += ".section .idata$6,\"dr\"\n"
	if entry.ordinal == 0 {
		text += "hint:\n.short 0\n.asciz \"" + entry.export + "\"\n.balign 2\n"
	}
	return text + ".section .idata$7,\"dr\"\n.rva dll_head\n"
}

func longImportParts(t *testing.T, target, dll string, entries ...longImportFixture) [][]byte {
	t.Helper()
	head, _ := coffAssembly(t, target, ".section .idata$2,\"dr\"\n.global dll_head\ndll_head:\n.long 0,0,0\n.rva dll_name\n.long 0\n")
	tail, _ := coffAssembly(t, target, ".section .idata$7,\"dr\"\n.global dll_name\ndll_name:\n.asciz \""+dll+"\"\n")
	parts := [][]byte{head, tail}
	for _, entry := range entries {
		data, _ := coffAssembly(t, target, longImportAssembly(target, entry))
		parts = append(parts, data)
	}
	return parts
}

func longImportArchive(parts [][]byte) []byte {
	archive := []byte("!<arch>\n")
	for i, part := range parts {
		archive = append(archive, archiveMemberBytes(fmt.Sprintf("import%d.obj/", i), part)...)
	}
	return archive
}

func TestCOFFLongImportArchives(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			entries := []longImportFixture{{"add", "add", 0, false}, {"renamed", "different_export", 0, false}, {"numbered", "", 7, false}, {"value", "value", 0, true}}
			parts := longImportParts(t, target, "provider.dll", entries...)
			f, err := parse("long.lib", longImportArchive(parts))
			if err != nil {
				t.Fatal(err)
			}
			for i, entry := range entries {
				o := f.members[i+2].obj
				if len(o.info.Unsupported) != 0 || len(o.info.Imports) != 1 || len(o.sections) != 0 || len(o.relocs) != 0 {
					t.Fatalf("import not normalized: %+v", o.info)
				}
				imported := o.info.Imports[0]
				if imported.DLL != "provider.dll" || imported.Symbol != entry.public || imported.Name != entry.export || imported.Ordinal != entry.ordinal || (imported.Kind == "data") != entry.data {
					t.Fatalf("import metadata: %+v", imported)
				}
				for _, v := range o.symbols {
					if v.section != -4 || v.imported == nil {
						t.Fatal("unconverted import symbol")
					}
				}
				// The thunk on its own cannot identify the DLL.
				standalone, err := parse("isolated.obj", parts[i+2])
				if err != nil || len(standalone.info.Unsupported) == 0 {
					t.Fatalf("isolated import accepted: %v", err)
				}
			}
		})
	}
}

func TestCOFFLongImportTemplateValidation(t *testing.T) {
	parts := longImportParts(t, "x86_64-pc-windows-msvc", "provider.dll", longImportFixture{"add", "add", 0, false})
	for _, mutation := range []func([]*object){
		func(objects []*object) { objects[0].symbols = nil },
		func(objects []*object) {
			objects[0].sections = append(objects[0].sections, &section{name: ".text", size: 1, data: []byte{0xc3}, exec: true})
		},
		func(objects []*object) {
			objects[1].sections = append(objects[1].sections, &section{name: ".CRT$XCU", size: 8, data: make([]byte, 8), lifecycle: lifecycleInit})
		},
		func(objects []*object) {
			for _, s := range objects[1].sections {
				if s != nil && s.name == ".idata$7" {
					s.data = []byte("unterminated")
				}
			}
		},
		func(objects []*object) {
			for _, s := range objects[2].sections {
				if s != nil && s.name == ".text" {
					s.data[0] = 0xc3 // Arbitrary code must not be erased.
				}
			}
		},
		func(objects []*object) {
			for _, s := range objects[2].sections {
				if s != nil && s.name == ".idata$4" {
					s.data[4] = 1 // ILT and IAT must agree.
				}
			}
		},
		func(objects []*object) {
			objects[2].symbols = append(objects[2].symbols, symbol{name: "extra", global: true, section: 1})
		},
		func(objects []*object) { objects[2].relocs = append(objects[2].relocs, objects[2].relocs[0]) },
	} {
		objects := make([]*object, len(parts))
		for i, part := range parts {
			// Some mutations alter section bytes; keep source fixtures intact.
			f, err := parse("member", append([]byte(nil), part...))
			if err != nil {
				t.Fatal(err)
			}
			objects[i] = f.obj
		}
		mutation(objects)
		if _, ok := newCOFFImportGraph(objects).convert(objects[2]); ok {
			t.Fatal("malformed or incomplete import template accepted")
		}
	}
	// Ambiguous descriptor definitions cannot pick a DLL arbitrarily.
	f, err := parse("member", parts[0])
	if err != nil {
		t.Fatal(err)
	}
	g, err := parse("thunk", parts[2])
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := newCOFFImportGraph([]*object{f.obj, f.obj, g.obj}).convert(g.obj); ok {
		t.Fatal("ambiguous descriptor accepted")
	}
}

func TestNativeCOFFLongImportLibraries(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DLL imports require Windows")
	}
	needNative(t)
	dll, _, _ := nativeImportFixture(t)
	target := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH] + "-w64-windows-gnu"
	parts := longImportParts(t, target, "provider.dll", longImportFixture{"add", "add", 0, false}, longImportFixture{"value", "value", 0, true}, longImportFixture{"numbered", "", 7, false})
	lib := filepath.Join(filepath.Dir(dll), "long.lib")
	if err := os.WriteFile(lib, longImportArchive(parts), 0600); err != nil {
		t.Fatal(err)
	}
	// Use a MinGW-targeted consumer, with explicit DLL function/data imports.
	src := filepath.Join(filepath.Dir(dll), "mingw-consumer.c")
	if err := os.WriteFile(src, []byte("__declspec(dllimport) int add(int,int);\n__declspec(dllimport) extern int value;\nint imported(int a,int b){return add(a,b)+value-42;}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	obj := compile(t, src, src+".obj", "--target="+target)
	s := New(Options{})
	defer s.Close()
	load(t, s, lib, obj)
	if err := s.Link("imported", "numbered"); err != nil {
		t.Fatal(err)
	}
	call(t, s, "imported", 20, 22, 42)
	call(t, s, "numbered", 20, 22, 42)
	if len(s.libs) != 1 || len(s.image.objects) != 4 { // Consumer plus three imports.
		t.Fatalf("descriptor helpers were extracted: DLLs=%d objects=%d", len(s.libs), len(s.image.objects))
	}
}

func TestGNUImportLibraryFixtures(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "386"} {
		info, err := Inspect(filepath.Join("testdata", "coffimports", "windows_"+arch+".a"))
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, member := range info.Members {
			count += len(member.Imports)
			if len(member.Imports) != 0 && (member.Arch != arch || len(member.Unsupported) != 0) {
				t.Fatalf("GNU import not normalized: %+v", member)
			}
		}
		if count != 3 {
			t.Fatalf("GNU dlltool imports not decoded: %+v", info)
		}
	}
}

func TestNativeGNUImportLibraries(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DLL imports require Windows")
	}
	needNative(t)
	dll, _, consumer := nativeImportFixture(t)
	lib := filepath.Join("testdata", "coffimports", "windows_"+runtime.GOARCH+".a")
	s := New(Options{LibraryPaths: []string{filepath.Dir(dll)}})
	defer s.Close()
	load(t, s, lib, consumer)
	if err := s.Link("imported", "numbered"); err != nil {
		t.Fatal(err)
	}
	call(t, s, "imported", 20, 22, 42)
	call(t, s, "numbered", 20, 22, 42)
	if len(s.libs) != 1 || len(s.image.objects) != 4 {
		t.Fatalf("GNU descriptor helpers extracted: DLLs=%d objects=%d", len(s.libs), len(s.image.objects))
	}
}
