package dylib

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type delayFixtureEntry struct {
	public, name string
	ordinal      uint16
}

func delayFixtureParts(t *testing.T, target string, entries []delayFixtureEntry) ([][]byte, []*object) {
	return delayFixturePartsExtra(t, target, entries, "")
}

func delayFixturePartsExtra(t *testing.T, target string, entries []delayFixtureEntry, extra string) ([][]byte, []*object) {
	t.Helper()
	prefix, word, feat := "", ".quad", ""
	if strings.HasPrefix(target, "i686") {
		prefix, word, feat = "_", ".long", ".def @feat.00; .scl 3; .type 0; .endef\n.set @feat.00,1\n"
	}
	tail, desc, helper := prefix+"__tailMerge_fixture", "__DELAY_IMPORT_DESCRIPTOR_fixture", "__delayLoadHelper2"
	if prefix != "" {
		helper = "___delayLoadHelper2@8"
	}
	text := ".text\n.global " + tail + "\n" + tail + ":\n"
	switch {
	case prefix != "":
		text += "pushl %ecx\npushl %edx\npushl %eax\npushl $" + desc + "\ncall " + helper + "\npopl %edx\npopl %ecx\njmp *%eax\n"
	case strings.HasPrefix(target, "aarch64"):
		text += "sub sp,sp,#224\nstp x0,x1,[sp,#0]\nstp x2,x3,[sp,#16]\nstp x4,x5,[sp,#32]\nstp x6,x7,[sp,#48]\nstp x8,x30,[sp,#64]\nstp q0,q1,[sp,#80]\nstp q2,q3,[sp,#112]\nstp q4,q5,[sp,#144]\nstp q6,q7,[sp,#176]\nmov x1,x16\nadrp x0," + desc + "\nadd x0,x0,:lo12:" + desc + "\nbl " + helper + "\nmov x16,x0\nldp x0,x1,[sp,#0]\nldp x2,x3,[sp,#16]\nldp x4,x5,[sp,#32]\nldp x6,x7,[sp,#48]\nldp x8,x30,[sp,#64]\nldp q0,q1,[sp,#80]\nldp q2,q3,[sp,#112]\nldp q4,q5,[sp,#144]\nldp q6,q7,[sp,#176]\nadd sp,sp,#224\nbr x16\n"
	default:
		text += "subq $136,%rsp\nmovq %rcx,144(%rsp)\nmovq %rdx,152(%rsp)\nmovq %r8,160(%rsp)\nmovq %r9,168(%rsp)\n"
		for i := 0; i < 6; i++ {
			text += fmt.Sprintf("movaps %%xmm%d,%d(%%rsp)\n", i, 32+i*16)
		}
		text += "movq %rax,%rdx\nleaq " + desc + "(%rip),%rcx\ncall " + helper + "\nmovq 144(%rsp),%rcx\nmovq 152(%rsp),%rdx\nmovq 160(%rsp),%r8\nmovq 168(%rsp),%r9\n"
		for i := 0; i < 6; i++ {
			text += fmt.Sprintf("movaps %d(%%rsp),%%xmm%d\n", 32+i*16, i)
		}
		text += "addq $136,%rsp\njmp *%rax\n"
	}
	text += ".section .text$2,\"dr\"\n.global " + desc + "\n" + desc + ":\n.long 1\n.rva " + prefix + "__fixture_iname\n.rva hmod\n.rva iat_start\n.rva int_start\n.zero 12\n.data\nhmod:\n" + word + " 0\n.section .idata$5,\"dw\"\n" + word + " 0\niat_start:\n.section .idata$4,\"dr\"\n" + word + " 0\nint_start:\n.section .idata$2,\"dr\"\n"
	b, o := coffAssembly(t, target, feat+text)
	parts := [][]byte{b}
	objects := []*object{o}
	for index, e := range entries {
		pub, imp := prefix+e.public, "__imp_"+prefix+e.public
		body := ".text\n.global " + pub + "\n" + pub + ":\n"
		switch {
		case prefix != "":
			body += "jmp *" + imp + "\nfallback:\nmovl $" + imp + ",%eax\njmp " + tail + "\n"
		case strings.HasPrefix(target, "aarch64"):
			body += "adrp x16," + imp + "\nadd x16,x16,:lo12:" + imp + "\nldr x16,[x16]\nbr x16\nfallback:\nadrp x16," + imp + "\nadd x16,x16,:lo12:" + imp + "\nb " + tail + "\n"
		default:
			body += "jmp *" + imp + "(%rip)\nfallback:\nleaq " + imp + "(%rip),%rax\njmp " + tail + "\n"
		}
		body += ".section .idata$5,\"dw\"\n.global " + imp + "\n" + imp + ":\n" + word + " fallback\n.section .idata$4,\"dr\"\n"
		if e.ordinal != 0 {
			flag := uint64(1) << 63
			if prefix != "" {
				flag = 1 << 31
			}
			body += fmt.Sprintf("%s 0x%x\n", word, flag|uint64(e.ordinal))
		} else {
			body += ".rva hint\n"
			if prefix == "" {
				body += ".long 0\n"
			}
			body += ".section .idata$6,\"dr\"\nhint:\n.short 0\n.asciz \"" + e.name + "\"\n"
		}
		body += ".section .idata$7,\"dr\"\n"
		if index == 0 {
			body += extra
		}
		b, o = coffAssembly(t, target, feat+body)
		parts = append(parts, b)
		objects = append(objects, o)
	}
	body := ".section .idata$4,\"dr\"\n" + word + " 0\n.section .idata$5,\"dw\"\n" + word + " 0\n.section .idata$7,\"dr\"\n.global " + prefix + "__fixture_iname\n" + prefix + "__fixture_iname:\n.asciz \"probe.dll\"\n"
	b, o = coffAssembly(t, target, feat+body)
	parts = append(parts, b)
	objects = append(objects, o)
	return parts, objects
}

var delayEntries = []delayFixtureEntry{{"add", "add", 0}, {"numbered", "", 7}}

func nativeDelayEntries() []delayFixtureEntry {
	return append(append([]delayFixtureEntry{}, delayEntries...), delayFixtureEntry{"mix", "mix", 0}, delayFixtureEntry{"pair", "pair", 0}, delayFixtureEntry{"indirect", "indirect", 0}, delayFixtureEntry{"varmix", "varmix", 0})
}

func TestCOFFDelayImportTargets(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		t.Run(target, func(t *testing.T) {
			parts, objects := delayFixtureParts(t, target, delayEntries)
			before := make([]*object, len(objects))
			for i, o := range objects {
				before[i] = copyDirectoryObject(o)
			}
			g := newCOFFImportGraph(objects)
			count := 0
			for i, o := range objects {
				out, ok := g.convert(o)
				if i > 0 && i < len(objects)-1 {
					if !ok || len(out.info.Imports) != 1 || !out.info.Imports[0].Delay || len(out.info.Unsupported) != 0 {
						t.Fatal("delay stub not recognized", i, ok, out)
					}
					count += len(out.info.Imports)
				}
				if !reflect.DeepEqual(o, before[i]) {
					t.Fatal("decoder changed staged input")
				}
			}
			if count != 2 {
				t.Fatal(count)
			}
			f, err := parse("delay.lib", longImportArchive(parts))
			if err != nil {
				t.Fatal(err)
			}
			if !f.members[1].info.Imports[0].Delay {
				t.Fatal("archive lost delay marker")
			}
			t.Run("bigobj", func(t *testing.T) {
				var big [][]byte
				for _, b := range parts {
					big = append(big, promoteBigCOFF(t, b))
				}
				f, err := parse("delay-big.lib", longImportArchive(big))
				if err != nil || f == nil || !f.members[1].info.Imports[0].Delay {
					t.Fatal("bigobj lost delay import", err)
				}
			})
		})
	}
}

func TestCOFFGNUDelayFixtures(t *testing.T) {
	for _, arch := range []string{"amd64", "386"} {
		t.Run(arch, func(t *testing.T) {
			info, err := Inspect(filepath.Join("testdata", "coffdelay", "windows_"+arch+".a"))
			if err != nil {
				t.Fatal(err)
			}
			var imports []ImportInfo
			for _, m := range info.Members {
				imports = append(imports, m.Imports...)
			}
			if len(imports) != 2 {
				t.Fatal("GNU output-delaylib was not decoded", info)
			}
			for _, i := range imports {
				if !i.Delay || i.DLL != "probe.dll" || i.Name != "add" && i.Ordinal != 7 {
					t.Fatal(i)
				}
			}
		})
	}
}

func TestCOFFDelayImportPolicy(t *testing.T) {
	_, objects := delayFixtureParts(t, "x86_64-pc-windows-msvc", delayEntries)
	g := newCOFFImportGraph(objects)
	out, ok := g.convert(objects[1])
	if !ok {
		t.Fatal("fixture")
	}
	// Policy validation is independent of mapping and foreign code execution.
	for _, mode := range []DelayImportMode{DelayImportsDisabled, DelayImportsEager, 42} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			err := checkCOFFDelayImports([]*object{out}, mode)
			if mode == DelayImportsEager {
				if err != nil {
					t.Fatal(err)
				}
				return
			} // Native successful binding is tested on Windows.
			if err == nil || !strings.Contains(err.Error(), "delay-import") && !strings.Contains(err.Error(), "delay imports") {
				t.Fatal("policy opened DLLs or published code", err)
			}
		})
	}
}

func TestNativeCOFFDelayImports(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native delay imports require Windows")
	}
	needNative(t)
	target := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH] + "-pc-windows-msvc"
	entries := nativeDelayEntries()
	for _, opt := range []string{"-O0", "-O2"} {
		dir, dll := delayProvider(t, target, opt)
		consumer := compile(t, "testdata/coffdelay/consumer.c", filepath.Join(dir, "consumer.obj"), "--target="+target, opt)
		depSource := filepath.Join(dir, "dependency.c")
		if err := os.WriteFile(depSource, []byte("int dependency(int a,int b){return a+b+1;}"), 0600); err != nil {
			t.Fatal(err)
		}
		dependency := compile(t, depSource, filepath.Join(dir, "dependency.obj"), "--target="+target, opt)
		parts, _ := delayFixtureParts(t, target, entries)
		control := linkDelayControl(t, target, opt, dir, consumer, dependency, parts)
		t.Run(opt+"/system", func(t *testing.T) {
			s := New(Options{})
			defer s.Close()
			load(t, s, control)
			defer func() { _, _ = callInt32(s, "release_provider", 0, 0) }()
			call(t, s, "observed", 0, 0, 0)
			call(t, s, "delay_call", 20, 22, 252)
			call(t, s, "observed", 0, 0, 6)
			call(t, s, "delay_call", 20, 22, 252)
			call(t, s, "observed", 0, 0, 6)
			call(t, s, "release_provider", 0, 0, 1)
		})
		for _, kind := range []string{"archive", "objects", "cross_archives", "retry_dependency", "retry_provider", "disabled", "unused"} {
			t.Run(opt+"/"+kind, func(t *testing.T) {
				stage := t.TempDir()
				opts := Options{DelayImports: DelayImportsEager, LibraryPaths: []string{dir}}
				if kind == "disabled" || kind == "unused" {
					opts.DelayImports = DelayImportsDisabled
				}
				if kind == "retry_provider" {
					opts.LibraryPaths = nil
				}
				s := New(opts)
				defer s.Close()
				if kind != "unused" {
					load(t, s, consumer)
				}
				if kind == "objects" {
					load(t, s, writeComponentObjects(t, stage, parts)...)
				} else if kind == "cross_archives" {
					for i, subset := range [][][]byte{parts[:1], parts[1:]} {
						path := filepath.Join(stage, fmt.Sprintf("delay%d.lib", i))
						if err := os.WriteFile(path, longImportArchive(subset), 0600); err != nil {
							t.Fatal(err)
						}
						load(t, s, path)
						if err := os.Remove(path); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					path := filepath.Join(stage, "delay.lib")
					if err := os.WriteFile(path, longImportArchive(parts), 0600); err != nil {
						t.Fatal(err)
					}
					load(t, s, path)
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
				if kind != "retry_dependency" {
					load(t, s, dependency)
				}
				if kind == "unused" {
					if err := s.Link("dependency"); err != nil || len(s.libs) != 0 {
						t.Fatal("unused delay archive opened provider", err)
					}
					call(t, s, "dependency", 20, 22, 43)
					return
				}
				if kind == "disabled" {
					err := s.Link("delay_call")
					if err == nil || !strings.Contains(err.Error(), "DelayImportsEager") || s.image != nil || len(s.libs) != 0 {
						t.Fatal("disabled policy opened DLLs", err)
					}
					return
				}
				if kind == "retry_dependency" || kind == "retry_provider" {
					err := s.Link("delay_call")
					if err == nil || s.image != nil {
						t.Fatal("failed link published code", err)
					}
					if kind == "retry_dependency" {
						if !strings.Contains(err.Error(), "dependency") {
							t.Fatal(err)
						}
						load(t, s, dependency)
					} else {
						if len(s.libs) != 0 {
							t.Fatal("missing provider created handle")
						}
						load(t, s, dll)
					}
				}
				if err := s.Link("delay_call"); err != nil {
					t.Fatal(err)
				}
				if len(s.libs) != 1 {
					t.Fatal("provider not owned at Link", len(s.libs))
				}
				call(t, s, "provider_calls", 0, 0, 0)
				call(t, s, "delay_call", 20, 22, 252)
				call(t, s, "provider_calls", 0, 0, 2)
				call(t, s, "delay_call", 20, 22, 252)
				call(t, s, "provider_calls", 0, 0, 4)
			})
		}
	}
}
