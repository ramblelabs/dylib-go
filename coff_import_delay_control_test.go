package dylib

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func delayProvider(t *testing.T, target, opt string) (dir, dll string) {
	t.Helper()
	dir = t.TempDir()
	provider := compile(t, "testdata/coffdelay/provider.c", filepath.Join(dir, "provider.obj"), "--target="+target, opt)
	def := filepath.Join(dir, "provider.def")
	if err := os.WriteFile(def, []byte("EXPORTS\nadd @7\nmix\npair\nindirect\nvarmix\nprovider_calls\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dll = filepath.Join(dir, "probe.dll")
	command(t, "lld-link", "/dll", "/noentry", "/nodefaultlib", "/def:"+def, "/out:"+dll, provider)
	return
}

// Link the exact raw delay stubs with an independent, executable C helper.
// Eager raw binding never selects this helper; the system control calls it.
func linkDelayControl(t *testing.T, target, opt, dir, consumer, dependency string, parts [][]byte) string {
	t.Helper()
	machine, width := uint16(0x8664), 8
	if target[:7] == "aarch64" {
		machine = 0xaa64
	} else if target[:4] == "i686" {
		machine, width = 0x14c, 4
	}
	var entries [][]byte
	for _, v := range []struct {
		name  string
		bytes int
	}{{"LoadLibraryW", 4}, {"GetModuleFileNameW", 12}, {"GetProcAddress", 8}, {"FreeLibrary", 4}} {
		public, policy := v.name, uint16(1)
		if width == 4 {
			public, policy = fmt.Sprintf("_%s@%d", v.name, v.bytes), 3
		}
		entries = append(entries, shortImport(machine, 0, policy, public, "kernel32.dll", "", 0))
	}
	imports := filepath.Join(dir, "kernel.lib")
	command(t, "llvm-ar", append([]string{"rcs", imports}, writeComponentObjects(t, t.TempDir(), entries)...)...)
	helper := compile(t, "testdata/coffdelay/control.c", filepath.Join(dir, "helper.obj"), "--target="+target, opt)
	output := filepath.Join(dir, "control.dll")
	args := []string{"/dll", "/noentry", "/nodefaultlib", "/out:" + output, "/export:delay_call", "/export:observed", "/export:release_provider", "/section:.rdata,rw", consumer, dependency, helper, imports}
	args = append(args, writeComponentObjects(t, t.TempDir(), parts)...)
	command(t, "lld-link", args...)
	return output
}

func TestCOFFDelaySystemControl(t *testing.T) {
	if _, err := exec.LookPath("lld-link"); err != nil {
		t.Skip("optional cross-target PE control; native Windows requires lld-link")
	}
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			consumer := compile(t, "testdata/coffdelay/consumer.c", filepath.Join(dir, "consumer.obj"), "--target="+target, "-O2")
			source := filepath.Join(dir, "dependency.c")
			if err := os.WriteFile(source, []byte("int dependency(int a,int b){return a+b+1;}"), 0600); err != nil {
				t.Fatal(err)
			}
			dependency := compile(t, source, source+".obj", "--target="+target, "-O2")
			parts, _ := delayFixtureParts(t, target, nativeDelayEntries())
			linkDelayControl(t, target, "-O2", dir, consumer, dependency, parts)
		})
	}
}

func TestNativeCOFFGNUDelayImports(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH == "arm64" {
		t.Skip("GNU delay archive fixtures cover Windows amd64/386")
	}
	needNative(t)
	target := map[string]string{"amd64": "x86_64", "386": "i686"}[runtime.GOARCH] + "-pc-windows-msvc"
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			dir, _ := delayProvider(t, target, opt)
			source := filepath.Join(dir, "consumer.c")
			if err := os.WriteFile(source, []byte("__declspec(dllimport) int add(int,int),numbered(int,int); int invoke(int a,int b){return add(a,b)+numbered(a,b);}"), 0600); err != nil {
				t.Fatal(err)
			}
			consumer := compile(t, source, source+".obj", "--target="+target, opt)
			s := New(Options{DelayImports: DelayImportsEager, LibraryPaths: []string{dir}})
			defer s.Close()
			load(t, s, consumer, filepath.Join("testdata", "coffdelay", "windows_"+runtime.GOARCH+".a"))
			if err := s.Link("invoke"); err != nil {
				t.Fatal(err)
			}
			call(t, s, "provider_calls", 0, 0, 0)
			call(t, s, "invoke", 20, 22, 84)
			call(t, s, "invoke", 20, 22, 84)
			call(t, s, "provider_calls", 0, 0, 4)
		})
	}
}
