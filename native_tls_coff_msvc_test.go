package dylib

import (
	"bytes"
	"debug/pe"
	"os"
	"path/filepath"
	"testing"
)

func TestCOFFNativeTLSGuardEntry(t *testing.T) {
	for _, tc := range []struct{ arch, target string }{{"amd64", "x86_64-pc-windows-msvc"}, {"arm64", "aarch64-pc-windows-msvc"}, {"386", "i686-pc-windows-msvc"}} {
		t.Run(tc.arch, func(t *testing.T) {
			path := compile(t, "testdata/coff_tls_guard_entry.S", filepath.Join(t.TempDir(), "entry.o"), "--target="+tc.target)
			f, err := pe.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			want, err := f.Section(".text").Data()
			if err != nil {
				t.Fatal(err)
			}
			got, err := coffTLSGuardEntry(tc.arch, 0x12345678, 0x124321)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("independent assembly mismatch: %v\ngot %x\nwant%x", err, got, want)
			}
		})
	}
}

func TestMSVCNativeTLSGuardValidation(t *testing.T) {
	for _, name := range []string{"defined", "common", "weak", "import", "alias", "forward", "disabled"} {
		t.Run(name, func(t *testing.T) {
			o := &object{info: Info{Format: "COFF", OS: "windows", Arch: "amd64", Bits: 64}, symbols: []symbol{{name: coffTLSGuard, global: true}}}
			enabled := true
			s := &o.symbols[0]
			switch name {
			case "defined":
				s.section = 1
			case "common":
				s.section = -2
			case "weak":
				s.weak = true
			case "import":
				s.name = "__imp_" + coffTLSGuard
			case "alias":
				s.alias = &weakAlias{}
			case "forward":
				s.forward = &symbolForward{}
			case "disabled":
				enabled = false
			}
			if _, err := checkCOFFTLS([]*object{o}, enabled); err == nil {
				t.Fatal("foreign native runtime guard accepted")
			}
		})
	}
	for _, tc := range []struct {
		name, arch string
		address    uintptr
		offset     uint32
	}{
		{"architecture", "riscv64", 1, 0}, {"index", "amd64", 0, 0}, {"offset", "amd64", 1, uint32(maxImage)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := coffTLSGuardEntry(tc.arch, tc.address, tc.offset); err == nil {
				t.Fatal("invalid native getter accepted")
			}
		})
	}
}

func TestMSVCNativeTLSFormats(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(target+"/"+opt, func(t *testing.T) {
				path := compile(t, "testdata/emutls_msvc_objects.cpp", filepath.Join(t.TempDir(), "native.o"), "--target="+target, "-ffreestanding", "-fno-emulated-tls", "-std=c++17", "-fno-exceptions", "-fno-rtti", "-DDYLIB_TLS_DYNAMIC_INIT", opt)
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				f, err := parse(path, b)
				if err != nil || len(f.info.Unsupported) != 0 {
					t.Fatal(f, err)
				}
				objects := []*object{f.obj}
				if needed, err := checkTLSRuntime(objects, Options{NativeTLS: true}); err != nil || !needed {
					t.Fatal(needed, err)
				}
				if _, err := checkTLSRuntime(objects, Options{}); err == nil {
					t.Fatal("native thread runtime accepted without option")
				}
				if needed, err := checkCOFFTLS(objects, true); err != nil || !needed {
					t.Fatal(needed, err)
				}
				defs, err := definitions(objects)
				if err != nil {
					t.Fatal(err)
				}
				owned, private := coffTLSGuardObject(objects, defs)
				if len(owned) != 2 || len(objects) != 1 {
					t.Fatal("private guard changed input list")
				}
				if _, ok := defs[coffTLSGuard]; ok {
					t.Fatal("private guard changed caller definitions")
				}
				d := private[coffTLSGuard]
				sec := d.o.sections[d.sym().section]
				if sec.nativeTLS != coffTLSSection || sec.size != 1 || !sec.write {
					t.Fatal("guard is not native TLS")
				}
				im := &image{objects: owned, defs: private, page: 4096}
				if _, err := im.layoutCOFFTLS(0); err != nil {
					t.Fatal(err)
				}
				if _, ok := im.coffTLS.sections[sec]; !ok {
					t.Fatal("guard missing from native template")
				}
				if usesMSVCEmulatedTLS(objects) {
					t.Fatal("native guard interpreted as an emulated descriptor")
				}
			})
		}
	}
}

func TestNativeMSVCTLS(t *testing.T) {
	needMSVCTLS(t)
	for _, tc := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"destructors", TestNativeCXXTLSThreadDestructors},
		{"retirement", TestNativeCXXTLSThreadDestructorRetirement},
		{"isolation", TestNativeCXXTLSThreadDestructorIsolation},
		{"late_registration", TestNativeCXXTLSThreadDestructorLateRegistration},
		{"manual", TestNativeCXXTLSThreadDestructorManual},
		{"initialization", TestNativeCXXTLSThreadDestructorInitialization},
		{"failed_initialization", TestNativeCXXTLSThreadDestructorInitializerFailure},
		{"active_initialization", TestNativeCXXTLSActiveInitialization},
		{"snapshot", func(t *testing.T) { testMSVCTLSSnapshot(t, true) }},
		{"active_owner", func(t *testing.T) { testMSVCTLSActiveInitialization(t, true) }},
		{"existing_threads", TestNativeCOFFCXXTLSExistingThreads},
		{"multiple_objects", func(t *testing.T) { testMSVCTLSMultipleObjects(t, true) }},
		{"mixed_modes", func(t *testing.T) { testCOFFCXXTLSMixedModes(t, "msvc") }},
	} {
		t.Run(tc.name, tc.run)
	}
}
