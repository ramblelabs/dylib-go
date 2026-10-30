package dylib

import (
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	examplecall "github.com/ramblelabs/dylib-go/examples/call"
	"github.com/ramblelabs/dylib-go/internal/native"
)

func command(t testing.TB, name string, args ...string) {
	t.Helper()
	if _, e := exec.LookPath(name); e != nil {
		if os.Getenv("DYLIB_TEST_REQUIRE_TOOLS") == "1" {
			t.Fatalf("required test tool %s unavailable: %v", name, e)
		}
		t.Skipf("%s unavailable", name)
	}
	c := exec.Command(name, args...)
	if name == compiler() && runtime.GOARCH == "386" {
		foreign := false
		for _, arg := range args {
			foreign = foreign || strings.HasPrefix(arg, "--target=")
		}
		if !foreign {
			flags := []string{"-m32"}
			if runtime.GOOS == "windows" {
				flags = []string{"--target=i686-w64-windows-gnu"}
				if root := os.Getenv("DYLIB_NATIVE_SYSROOT"); root != "" {
					flags = append(flags, "--sysroot="+root, "--rtlib=libgcc")
				}
			}
			c = exec.Command(name, append(flags, args...)...)
		}
	}
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, e, b)
	}
}
func compiler() string {
	if p := os.Getenv("CLANG"); p != "" {
		return p
	}
	return "clang"
}
func compile(t testing.TB, src, out string, flags ...string) string {
	t.Helper()
	return compileWith(t, compiler(), src, out, flags...)
}
func compileWith(t testing.TB, cc, src, out string, flags ...string) string {
	t.Helper()
	args := []string{"-O0", "-fno-stack-protector", "-c", src, "-o", out}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" {
		args = append(args, "--target=x86_64-apple-macosx11")
	}
	if runtime.GOOS == "windows" {
		// Select the MSVC object ABI even when clang comes from MSYS2. MinGW
		// can introduce COMDAT import-pointer helpers, which are unsupported.
		// These fixtures need only clang's freestanding stdint.h, not an SDK.
		triple := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH]
		args = append(args, "--target="+triple+"-pc-windows-msvc", "-ffreestanding")
	}
	windows := runtime.GOOS == "windows"
	explicitTarget := false
	for _, flag := range flags {
		if strings.HasPrefix(flag, "--target=") {
			windows = strings.Contains(flag, "windows")
			explicitTarget = true
		}
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == "386" && !explicitTarget {
		args = append(args, "-m32")
	}
	if !windows {
		args = append(args, "-fPIC")
	}
	args = append(args, flags...)
	command(t, cc, args...)
	return out
}
func needNative(t testing.TB) {
	t.Helper()
	if !native.Available() || (runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64" && runtime.GOARCH != "386") {
		if os.Getenv("DYLIB_TEST_REQUIRE_NATIVE") == "1" {
			t.Fatal("required native object execution unavailable")
		}
		t.Skip("native calls unavailable")
	}
}
func load(t testing.TB, s *Session, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if e := s.Load(p); e != nil {
			t.Fatal(e)
		}
	}
}
func call(t *testing.T, s *Session, n string, a, b, want int32) {
	t.Helper()
	v, e := callInt32(s, n, a, b)
	if e != nil || v != want {
		t.Fatalf("%s(%d,%d) = %d, %v; want %d", n, a, b, v, e, want)
	}
}

// Only the test adapter knows this fixed signature; the loader resolves a
// generic symbol and keeps its address alive during the adapter invocation.
func callInt32(s *Session, name string, a, b int32) (int32, error) {
	symbol, err := s.Resolve(name)
	if err != nil {
		return 0, err
	}
	return callSymbolInt32(symbol, a, b)
}

func callSymbolInt32(symbol *Symbol, a, b int32) (int32, error) {
	var result int32
	err := symbol.WithAddress(func(address uintptr) error {
		var err error
		result, err = examplecall.Int32(address, a, b)
		return err
	})
	return result, err
}

func TestNativeObjects(t *testing.T) {
	needNative(t)
	dir := t.TempDir()
	for _, tc := range []struct {
		file, name string
		a, b, want int32
		flags      []string
	}{
		{"add.c", "add", 20, 22, 42, nil}, {"state.c", "state", 20, 22, 49, nil}, {"pointers.c", "pointers", 20, 22, 49, nil}, {"common.c", "common", 20, 22, 42, []string{"-fcommon"}}, {"exception_free.cpp", "cpp_add", 20, 22, 42, []string{"-fno-exceptions", "-fno-rtti"}},
		{"pcbias.c", "pcbias", 20, 22, 49, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := compile(t, "testdata/"+tc.file, filepath.Join(dir, tc.name+".o"), tc.flags...)
			s := New(Options{})
			defer s.Close()
			load(t, s, p)
			call(t, s, tc.name, tc.a, tc.b, tc.want)
		})
	}
}

func TestArchiveExtractionAndCrossObjectCall(t *testing.T) {
	needNative(t)
	dir := t.TempDir()
	add := compile(t, "testdata/add.c", filepath.Join(dir, "a_very_long_member_name_for_archive.o"))
	unused := compile(t, "testdata/missing.c", filepath.Join(dir, "unused.o"))
	caller := compile(t, "testdata/caller.c", filepath.Join(dir, "caller.o"))
	ar := filepath.Join(dir, "plugins.a")
	command(t, "ar", "rcs", ar, add, unused)
	for _, root := range []string{"add", "caller"} {
		s := New(Options{})
		load(t, s, ar)
		if root == "caller" {
			load(t, s, caller)
		}
		want := int32(42)
		if root == "caller" {
			want++
		}
		call(t, s, root, 20, 22, want)
		if e := s.Close(); e != nil {
			t.Fatal(e)
		}
	}
	s := New(Options{})
	defer s.Close()
	load(t, s, ar)
	if _, e := s.Lookup("unused"); e == nil || !strings.Contains(e.Error(), "missing_dependency") {
		t.Fatalf("missing dependency: %v", e)
	}
}

func TestRetryAndLifecycle(t *testing.T) {
	needNative(t)
	dir := t.TempDir()
	caller := compile(t, "testdata/caller.c", filepath.Join(dir, "caller.o"))
	add := compile(t, "testdata/add.c", filepath.Join(dir, "add.o"))
	s := New(Options{})
	load(t, s, caller)
	if _, e := s.Lookup("caller"); e == nil {
		t.Fatal("missing dependency accepted")
	}
	load(t, s, add)
	call(t, s, "caller", 20, 22, 43)
	if e := s.Load(add); !errors.Is(e, ErrLinked) {
		t.Fatalf("load after link: %v", e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Lookup("caller"); !errors.Is(e, ErrClosed) {
		t.Fatalf("lookup after close: %v", e)
	}
}

func compileSharedAdd(t *testing.T, dir string) string {
	t.Helper()
	lib := filepath.Join(dir, "plugin.so")
	flags := []string{"-shared", "-fPIC", "testdata/add.c", "-o", lib}
	if runtime.GOOS == "darwin" {
		lib = filepath.Join(dir, "plugin.dylib")
		flags = []string{"-dynamiclib", "testdata/add.c", "-o", lib}
		if runtime.GOARCH == "amd64" {
			flags = append(flags, "-arch", "x86_64")
		}
	} else if runtime.GOOS == "windows" {
		lib = filepath.Join(dir, "plugin.dll")
		src := filepath.Join(dir, "plugin.c")
		if e := os.WriteFile(src, []byte("__declspec(dllexport) int add(int a, int b) { return a+b; }"), 0600); e != nil {
			t.Fatal(e)
		}
		flags = []string{"-shared", src, "-o", lib}
	}
	command(t, compiler(), flags...)
	return lib
}

// Library execution is checked independently of raw object execution.
func needShared(t *testing.T) {
	t.Helper()
	if !native.Available() || (runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64" && runtime.GOARCH != "386") {
		if os.Getenv("DYLIB_TEST_REQUIRE_SHARED") == "1" {
			t.Fatal("required shared-library native bridge unavailable")
		}
		t.Skip("shared-library native bridge unavailable")
	}
}

func TestSystemSharedLibrary(t *testing.T) {
	needShared(t)
	lib := compileSharedAdd(t, t.TempDir())
	i, e := Inspect(lib)
	if e != nil || i.Kind != "shared" || i.Arch != runtime.GOARCH {
		t.Fatalf("native shared-library metadata: %+v, %v", i, e)
	}
	s := New(Options{})
	defer s.Close()
	load(t, s, lib)
	call(t, s, "add", 20, 22, 42)
}

func TestSharedAndHostSymbols(t *testing.T) {
	needNative(t)
	dir := t.TempDir()
	lib := compileSharedAdd(t, dir)
	caller := compile(t, "testdata/caller.c", filepath.Join(dir, "caller.o"))
	s := New(Options{})
	defer s.Close()
	load(t, s, lib, caller)
	call(t, s, "caller", 20, 22, 43)
	call(t, s, "add", 20, 22, 42)
	if runtime.GOOS != "windows" {
		h := New(Options{ProcessSymbols: true})
		defer h.Close()
		host := compile(t, "testdata/host.c", filepath.Join(dir, "host.o"), "-fno-builtin")
		load(t, h, host)
		call(t, h, "host_abs", -20, 22, 42)
	}
	// Explicit host definitions are sufficient even without process lookup.
	h := New(Options{})
	defer h.Close()
	p, e := s.Lookup("add")
	if e != nil {
		t.Fatal(e)
	}
	if e = h.Define("add", p); e != nil {
		t.Fatal(e)
	}
	load(t, h, caller)
	call(t, h, "caller", 20, 22, 43)
}

func TestConcurrentCallsAndClose(t *testing.T) {
	needNative(t)
	dir := t.TempDir()
	p := compile(t, "testdata/add.c", filepath.Join(dir, "add.o"))
	s := New(Options{})
	load(t, s, p)
	if _, e := s.Lookup("add"); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				v, e := callInt32(s, "add", 20, 22)
				if e != nil && !errors.Is(e, ErrClosed) {
					t.Error(e)
				}
				if e == nil && v != 42 {
					t.Errorf("got %d", v)
				}
			}
		}()
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	wg.Wait()
}

func TestInspectForeignTargets(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.c")
	os.WriteFile(src, []byte("int add(int a,int b){return a+b;}"), 0600)
	for _, tc := range []struct{ triple, format, arch string }{
		{"x86_64-linux-gnu", "ELF", "amd64"}, {"aarch64-linux-gnu", "ELF", "arm64"}, {"x86_64-apple-macosx11", "Mach-O", "amd64"}, {"arm64-apple-macosx11", "Mach-O", "arm64"}, {"x86_64-windows-msvc", "COFF", "amd64"}, {"aarch64-windows-msvc", "COFF", "arm64"},
	} {
		t.Run(tc.triple, func(t *testing.T) {
			p := compile(t, src, filepath.Join(dir, tc.triple+".o"), "--target="+tc.triple)
			i, e := Inspect(p)
			if e != nil {
				t.Fatal(e)
			}
			if i.Format != tc.format || i.Arch != tc.arch {
				t.Fatalf("info: %+v", i)
			}
			if checkHost(i) != nil {
				s := New(Options{})
				defer s.Close()
				if e := s.Load(p); e == nil {
					t.Fatal("foreign object accepted for execution")
				}
			}
		})
	}
}

func TestRejectTLS(t *testing.T) {
	needNative(t)
	dir := t.TempDir()
	for _, src := range []string{"_Thread_local int x; int f(int a,int b){return x+a+b;}"} {
		p := filepath.Join(dir, "unsupported.c")
		os.WriteFile(p, []byte(src), 0600)
		obj := compile(t, p, filepath.Join(dir, "unsupported.o"))
		i, e := Inspect(obj)
		if e != nil {
			t.Fatalf("metadata: %+v %v", i, e)
		}
		s := New(Options{})
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows" {
			if len(i.Unsupported) != 0 {
				t.Fatal("native TLS cannot be staged:", i.Unsupported)
			}
			load(t, s, obj)
			if e = s.Link("f"); e == nil || !strings.Contains(e.Error(), "Options.NativeTLS") {
				t.Fatal("native TLS executed without opt-in:", e)
			}
		} else if len(i.Unsupported) == 0 {
			t.Fatal("unsupported native TLS model lost its diagnostic")
		} else if e = s.Load(obj); e == nil {
			t.Fatal("unsupported runtime requirement silently accepted")
		}
		s.Close()
	}
}

func TestForeignPointerRelocations(t *testing.T) {
	needNative(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "p.c")
	os.WriteFile(src, []byte("int value=7; int *pointer=&value;"), 0600)
	for _, triple := range []string{"x86_64-linux-gnu", "aarch64-linux-gnu", "x86_64-apple-macosx11", "arm64-apple-macosx11", "x86_64-windows-msvc", "aarch64-windows-msvc"} {
		t.Run(triple, func(t *testing.T) {
			p := compile(t, src, filepath.Join(dir, triple+".o"), "--target="+triple)
			b, e := readFile(p)
			if e != nil {
				t.Fatal(e)
			}
			f, e := parse(p, b)
			if e != nil {
				t.Fatal(e)
			}
			defs, e := definitions([]*object{f.obj})
			if e != nil {
				t.Fatal(e)
			}
			im, e := newImage([]*object{f.obj}, defs, func(string) uintptr { return 0 }, nil, false, false, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer im.close()
			ptr, e := im.lookup("pointer")
			if e != nil {
				t.Fatal(e)
			}
			value, e := im.lookup("value")
			if e != nil {
				t.Fatal(e)
			}
			got := binary.LittleEndian.Uint64(im.mem[ptr-im.base:])
			if got != uint64(value) {
				t.Fatalf("pointer=%#x; want %#x", got, value)
			}
		})
	}
}
