package dylib

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/internal/native"
)

func shortImport(machine uint16, kind, policy uint16, public, dll, exported string, ordinal uint16) []byte {
	data := []byte(public + "\x00" + dll + "\x00")
	if policy == 4 {
		data = append(data, []byte(exported+"\x00")...)
	}
	b := make([]byte, 20)
	le.PutUint16(b[2:], 0xffff)
	le.PutUint16(b[6:], machine)
	le.PutUint32(b[12:], uint32(len(data)))
	le.PutUint16(b[16:], ordinal)
	le.PutUint16(b[18:], kind|policy<<2)
	return append(b, data...)
}

func TestCOFFShortImportPolicies(t *testing.T) {
	for _, machine := range []uint16{0x8664, 0xaa64, 0x14c} {
		for kind := uint16(0); kind <= 2; kind++ {
			for _, tc := range []struct {
				policy         uint16
				public, export string
			}{
				{0, "_func@8", ""}, {1, "_func@8", "_func@8"},
				{2, "_func@8", "func@8"}, {3, "_func@8", "func"},
				{4, "_func@8", "renamed"},
			} {
				t.Run(fmt.Sprintf("%x/%d/%d", machine, kind, tc.policy), func(t *testing.T) {
					data := shortImport(machine, kind, tc.policy, tc.public, "provider.dll", "renamed", 7)
					f, err := parse("short", data)
					if err != nil {
						t.Fatal(err)
					}
					want := tc.public
					if machine == 0x14c {
						want = strings.TrimPrefix(want, "_")
					}
					entry := f.obj.symbols[0].imported.entry
					if len(f.info.Imports) != 1 || f.info.Imports[0].DLL != entry.dll || f.info.Imports[0].Name != entry.name || f.info.Imports[0].Symbol != want || f.info.Imports[0].Ordinal != entry.ordinal {
						t.Fatalf("inspection imports: %+v", f.info.Imports)
					}
					if entry.public != want || entry.name != tc.export || f.obj.symbols[0].name != "__imp_"+want {
						t.Fatalf("symbol/export mapping: %+v", entry)
					}
					if (entry.ordinal == 7) != (tc.policy == 0) {
						t.Fatalf("ordinal/hint mapping: %+v", entry)
					}
					count := 2
					if kind == 1 {
						count = 1
					}
					if len(f.info.Symbols) != count || !f.info.Symbols[0].Defined || len(f.obj.sections) != 0 || !f.obj.symbols[0].imported.indirect {
						t.Fatalf("import symbols: %+v", f.info)
					}
					if kind != 1 && f.obj.symbols[1].imported.indirect != (kind == 2) {
						t.Fatal("incorrect callable/constant alias")
					}
					archive := append([]byte("!<arch>\n"), archiveMemberBytes("provider.dll/", data)...)
					if _, err := parse("imports.lib", archive); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestMalformedCOFFImports(t *testing.T) {
	base := shortImport(0x8664, 0, 1, "add", "provider.dll", "", 1)
	for _, patch := range []func([]byte) []byte{
		func(b []byte) []byte { return b[:19] },
		func(b []byte) []byte { le.PutUint32(b[12:], 0xffffffff); return b },
		func(b []byte) []byte { b[len(b)-1] = 'x'; return b },
		func(b []byte) []byte { le.PutUint16(b[18:], 0x20); return b },
		func(b []byte) []byte { le.PutUint16(b[18:], 3|1<<2); return b },
		func(b []byte) []byte { le.PutUint16(b[18:], 5<<2); return b },
		func(b []byte) []byte { return shortImport(0x8664, 0, 0, "add", "provider.dll", "", 0) },
		func(b []byte) []byte { return shortImport(0x8664, 0, 1, "", "provider.dll", "", 1) },
		func(b []byte) []byte { return shortImport(0x8664, 0, 1, "add", "", "", 1) },
		func(b []byte) []byte { return shortImport(0x8664, 0, 4, "add", "provider.dll", "", 1) },
		func(b []byte) []byte { return shortImport(0x8664, 0, 3, "_@8", "provider.dll", "", 1) },
		func(b []byte) []byte { return shortImport(0x8664, 0, 1, "add", "../provider.dll", "", 1) },
	} {
		if _, err := parse("bad-import", patch(append([]byte(nil), base...))); err == nil {
			t.Fatal("accepted malformed short import")
		}
	}
}

func TestRecognizeLongCOFFImportTables(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		_, o := coffAssembly(t, target, ".section .idata$5,\"dr\"\n.long 0\n")
		if len(o.info.Unsupported) != 1 || !strings.Contains(o.info.Unsupported[0], "long import tables") {
			t.Fatalf("unresolved native import table accepted: %+v", o.info)
		}
	}
}

func TestCOFFImportDefinitionPriority(t *testing.T) {
	f, err := parse("import", shortImport(0x8664, 0, 1, "add", "provider.dll", "", 1))
	if err != nil {
		t.Fatal(err)
	}
	strong := &object{symbols: []symbol{{name: "add", global: true, section: 1}}}
	for _, objects := range [][]*object{{f.obj, strong}, {strong, f.obj}} {
		defs, err := definitions(objects)
		if err != nil || defs["add"].o != strong || defs["__imp_add"].o != f.obj {
			t.Fatalf("strong/import priority: %+v, %v", defs, err)
		}
	}
	if _, err := definitions([]*object{f.obj, f.obj}); err != nil {
		t.Fatal(err)
	}
	g, err := parse("other", shortImport(0x8664, 0, 1, "add", "other.dll", "", 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := definitions([]*object{f.obj, g.obj}); err == nil {
		t.Fatal("conflicting explicit imports accepted")
	}
}

func TestImportDLLPathsAndOptions(t *testing.T) {
	paths, origin := t.TempDir(), t.TempDir()
	for _, dir := range []string{paths, origin} {
		if err := os.WriteFile(filepath.Join(dir, "plugin.dll"), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		paths []string
		want  string
	}{
		{[]string{paths}, filepath.Join(paths, "plugin.dll")},
		{nil, filepath.Join(origin, "plugin.dll")},
	} {
		got, err := importDLLPath("plugin.dll", origin, tc.paths)
		if err != nil || got != tc.want {
			t.Fatalf("DLL path: %q %v", got, err)
		}
	}
	if _, err := importDLLPath("plugin.dll", origin, []string{"bad\x00path"}); err == nil {
		t.Fatal("NUL path accepted")
	}
	if got, err := importDLLPath("system.dll", origin, nil); err != nil || got != "system.dll" {
		t.Fatalf("system fallback: %q %v", got, err)
	}
	opts := Options{LibraryPaths: []string{paths}}
	s := New(opts)
	opts.LibraryPaths[0] = "changed"
	if s.opts.LibraryPaths[0] != paths {
		t.Fatal("Options retains caller slice")
	}
}

func TestCOFFImportIATLayout(t *testing.T) {
	for _, kind := range []uint16{0, 1, 2} {
		f, err := parse("iat", shortImport(0x8664, kind, 1, "entry", "provider.dll", "", 0))
		if err != nil {
			t.Fatal(err)
		}
		defs, err := definitions([]*object{f.obj})
		if err != nil {
			t.Fatal(err)
		}
		entry := f.obj.symbols[0].imported.entry
		im := &image{base: 0x10000000, defs: defs, imports: map[*coffImport]uintptr{entry: 0x12345678}, got: map[uintptr]uintptr{}, mem: make([]byte, 64), pointerSize: 8, stubStart: 64}
		for i, v := range f.obj.symbols {
			got, err := im.symbol(f.obj, i, false)
			want := uintptr(0x12345678)
			if v.imported.indirect {
				want = im.base
			}
			if err != nil || got != want || le.Uint64(im.mem) != 0x12345678 {
				t.Fatalf("IAT kind %d: %#x %v", kind, got, err)
			}
		}
		if im.gotNext != 8 { // CONST aliases share one slot, too.
			t.Fatalf("duplicate IAT slots: %d", im.gotNext)
		}
	}
}

func TestCOFFImportedRuntimeHelper(t *testing.T) {
	f, err := parse("atexit import", shortImport(0x8664, 0, 1, "atexit", "not-opened-crt.dll", "", 0))
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{})
	addresses, err := s.prepareImports([]*object{f.obj})
	if err != nil || len(addresses) != 0 || len(s.libs) != 0 {
		t.Fatalf("session helper delegated to DLL: %v", err)
	}
	defs, err := definitions([]*object{f.obj})
	if err != nil {
		t.Fatal(err)
	}
	im := &image{base: 0x10000000, defs: defs, hooks: map[string]uintptr{"atexit": 0x12345678}, got: map[uintptr]uintptr{}, mem: make([]byte, 64), pointerSize: 8, stubStart: 64}
	for i, v := range f.obj.symbols {
		got, err := im.symbol(f.obj, i, false)
		want := uintptr(0x12345678)
		if v.imported.indirect {
			want = im.base
		}
		if err != nil || got != want {
			t.Fatalf("session helper import: %#x %v", got, err)
		}
	}
	// An ordinary object definition still overrides an imported helper.
	strong := &object{symbols: []symbol{{name: "atexit", section: -1, value: 0x12345678, global: true}}}
	defs, err = definitions([]*object{f.obj, strong})
	if err != nil {
		t.Fatal(err)
	}
	im = &image{base: 0x10000000, defs: defs, got: map[uintptr]uintptr{}, mem: make([]byte, 64), pointerSize: 8, stubStart: 64}
	if _, err := im.symbol(f.obj, 0, false); err != nil || le.Uint64(im.mem) != 0x12345678 {
		t.Fatalf("object-defined helper import: %v", err)
	}
}

func TestNativeCOFFImportHelperPreparation(t *testing.T) {
	needNative(t)
	f, err := parse("atexit import", shortImport(0x8664, 0, 1, "atexit", "not-opened-crt.dll", "", 0))
	if err != nil {
		t.Fatal(err)
	}
	// Exercise native helper preparation on every host; no foreign code is
	// executed, and the generated helper thunk uses this host's ABI.
	f.obj.info.Arch, f.obj.info.OS = runtime.GOARCH, runtime.GOOS
	if runtime.GOARCH == "386" {
		f.obj.info.Bits = 32
	}
	defs, err := definitions([]*object{f.obj})
	if err != nil {
		t.Fatal(err)
	}
	im, err := newImage([]*object{f.obj}, defs, func(string) uintptr { return 0 }, nil, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer im.close()
	if im.lifecycle == nil || im.hooks["atexit"] == 0 {
		t.Fatal("import-only root did not prepare the session helper")
	}
}

func TestNativeCOFFImportedExitRegistration(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DLL imports require Windows")
	}
	needNative(t)
	dir := t.TempDir()
	host, _, read := lifecycleObserver(t, dir)
	src := filepath.Join(dir, "imported-exit.c")
	if err := os.WriteFile(src, []byte("extern void record_event(int);\n__declspec(dllimport) int atexit(void (*)(void));\nstatic void done(void){record_event(9);}\nint register_imported(int a,int b){return atexit(done)==0?a+b:-1;}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	obj := compile(t, src, filepath.Join(dir, "imported-exit.obj"))
	machine := map[string]uint16{"amd64": 0x8664, "arm64": 0xaa64, "386": 0x14c}[runtime.GOARCH]
	path := filepath.Join(dir, "atexit.obj")
	if err := os.WriteFile(path, shortImport(machine, 0, 1, "atexit", "not-opened-crt.dll", "", 0), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(Options{})
	defer s.Close()
	load(t, s, host, obj, path)
	call(t, s, "register_imported", 20, 22, 42)
	expectEvents(t, read)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	expectEvents(t, read, 9)
}

func nativeImportFixture(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "provider.c")
	if err := os.WriteFile(src, []byte("int add(int a,int b){return a+b;}\nint value=42;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	obj := compile(t, src, filepath.Join(dir, "provider.obj"))
	def := filepath.Join(dir, "provider.def")
	if err := os.WriteFile(def, []byte("LIBRARY provider.dll\nEXPORTS\nadd @7\nvalue DATA\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dll, lib := filepath.Join(dir, "provider.dll"), filepath.Join(dir, "provider.lib")
	machine := map[string]string{"amd64": "X64", "arm64": "ARM64", "386": "X86"}[runtime.GOARCH]
	command(t, "lld-link", "/dll", "/noentry", "/nodefaultlib", "/machine:"+machine, "/def:"+def, "/out:"+dll, "/implib:"+lib, obj)
	consumer := filepath.Join(dir, "consumer.c")
	if err := os.WriteFile(consumer, []byte("__declspec(dllimport) int add(int,int);\n__declspec(dllimport) extern int value;\nint imported(int a,int b){return add(a,b)+value-42;}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return dll, lib, compile(t, consumer, filepath.Join(dir, "consumer.obj"))
}

func TestNativeCOFFImportLibraries(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DLL imports require Windows")
	}
	needNative(t)
	dll, lib, consumer := nativeImportFixture(t)
	for _, explicit := range []bool{false, true} {
		s := New(Options{})
		if explicit {
			load(t, s, dll)
		}
		load(t, s, lib, consumer)
		call(t, s, "imported", 20, 22, 42)
		call(t, s, "add", 20, 22, 42)
		if len(s.libs) != 1 {
			t.Fatalf("DLL reference not reused: %d", len(s.libs))
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// A function root extracts its import without any raw caller object.
	s := New(Options{})
	load(t, s, lib)
	call(t, s, "add", 20, 22, 42)
	if _, err := s.Lookup("__imp_add"); err != nil { // IAT must already be read-only.
		t.Fatal(err)
	}
	s.Close()

	// Configured paths find a dependency when the .lib has been moved.
	moved := filepath.Join(t.TempDir(), "moved.lib")
	b, err := os.ReadFile(lib)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moved, b, 0600); err != nil {
		t.Fatal(err)
	}
	s = New(Options{LibraryPaths: []string{filepath.Dir(dll)}})
	load(t, s, moved, consumer)
	call(t, s, "imported", 20, 22, 42)
	s.Close()

	// An unused archive import must not open its missing DLL.
	machine := map[string]uint16{"amd64": 0x8664, "arm64": 0xaa64, "386": 0x14c}[runtime.GOARCH]
	unused := shortImport(machine, 0, 1, "unused", "nonexistent-unused.dll", "", 0)
	lazy := filepath.Join(filepath.Dir(dll), "lazy.lib")
	if err := os.WriteFile(lazy, append(b, archiveMemberBytes("unused.obj/", unused)...), 0600); err != nil {
		t.Fatal(err)
	}
	s = New(Options{})
	load(t, s, lazy)
	call(t, s, "add", 20, 22, 42)
	if len(s.libs) != 1 {
		t.Fatalf("unused DLL loaded: %d", len(s.libs))
	}
	s.Close()
}

func TestNativeCOFFImportNamesAndOrdinals(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DLL imports require Windows")
	}
	needNative(t)
	dll, _, _ := nativeImportFixture(t)
	machine := map[string]uint16{"amd64": 0x8664, "arm64": 0xaa64, "386": 0x14c}[runtime.GOARCH]
	for _, tc := range []struct {
		policy uint16
		public string
	}{
		{0, "ordinal_add"}, {1, "add"}, {2, "_add"}, {3, "_add@8"}, {4, "renamed_add"},
	} {
		path := filepath.Join(filepath.Dir(dll), fmt.Sprintf("name-%d.obj", tc.policy))
		data := shortImport(machine, 0, tc.policy, tc.public, "provider.dll", "add", 7)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		s := New(Options{})
		load(t, s, path)
		name := tc.public
		if runtime.GOARCH == "386" {
			name = strings.TrimPrefix(name, "_")
		}
		// Name decorations do not infer a calling convention: the actual
		// DLL entry is cdecl, and this test's adapter explicitly uses cdecl.
		call(t, s, name, 20, 22, 42)
		s.Close()
	}
	// CONST exposes the public name and __imp_ name as the same data slot.
	path := filepath.Join(filepath.Dir(dll), "constant.obj")
	if err := os.WriteFile(path, shortImport(machine, 2, 4, "constant", "provider.dll", "value", 0), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(Options{})
	load(t, s, path)
	a, err := s.Lookup("constant")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Lookup("__imp_constant")
	if err != nil || a != b {
		t.Fatalf("CONST aliases: %#x %#x %v", a, b, err)
	}
	s.Close()
}

func TestNativeCOFFImportFailureAndRetry(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DLL imports require Windows")
	}
	needNative(t)
	dll, _, _ := nativeImportFixture(t)
	machine := map[string]uint16{"amd64": 0x8664, "arm64": 0xaa64, "386": 0x14c}[runtime.GOARCH]
	path := filepath.Join(filepath.Dir(dll), "bad-ordinal.obj")
	if err := os.WriteFile(path, shortImport(machine, 0, 0, "retry", "provider.dll", "", 60000), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(Options{})
	load(t, s, path)
	if err := s.Link("retry"); err == nil || s.image != nil || len(s.libs) != 1 {
		t.Fatalf("invalid ordinal link: %v", err)
	}
	address := native.Lookup(s.libs[0], "add")
	if err := s.Define("retry", address); err != nil {
		t.Fatal(err)
	}
	call(t, s, "retry", 20, 22, 42)
	s.Close()

	// A missing DLL leaves no raw image; supplying it permits a retry.
	dir := t.TempDir()
	path = filepath.Join(dir, "missing.obj")
	if err := os.WriteFile(path, shortImport(machine, 0, 1, "add", "retry-missing.dll", "", 0), 0600); err != nil {
		t.Fatal(err)
	}
	s = New(Options{})
	load(t, s, path)
	if err := s.Link("add"); err == nil || s.image != nil || len(s.libs) != 0 {
		t.Fatalf("missing DLL link: %v", err)
	}
	data, err := os.ReadFile(dll)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "retry-missing.dll"), data, 0600); err != nil {
		t.Fatal(err)
	}
	call(t, s, "add", 20, 22, 42)
	s.Close()
}
