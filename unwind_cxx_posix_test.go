//go:build linux || darwin

package dylib

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	examplecall "github.com/ramblelabs/dylib-go/examples/call"
)

func testNativeCXXExceptions(t *testing.T) {
	testNativeCXXExceptionsMode(t, false)
}

func testNativeCXXExceptionsMode(t *testing.T, compact bool, extra ...string) {
	needNative(t)
	cpp := os.Getenv("DYLIB_CXX")
	if cpp == "" {
		cpp = "clang++"
	}
	for _, optimization := range []string{"-O0", "-O2"} {
		t.Run(optimization, func(t *testing.T) {
			dir := t.TempDir()
			flags := []string{"-std=c++17", optimization, "-fPIC", "-fexceptions", "-fno-stack-protector"}
			flags = append(flags, extra...)
			libraryName, libraryFlag := "cxx_host.so", "-shared"
			if runtime.GOOS == "linux" {
				flags = append(flags, "-stdlib=libstdc++", "-rtlib=libgcc", "-unwindlib=libgcc")
			} else {
				libraryName, libraryFlag = "cxx_host.dylib", "-dynamiclib"
				flags = append(flags, "-stdlib=libc++")
				if compact {
					flags = append(flags, "-DDYLIB_CXX_COMPACT")
				} else {
					flags = append(flags, "-femit-dwarf-unwind=always")
				}
				if runtime.GOARCH == "amd64" {
					flags = append(flags, "--target=x86_64-apple-macosx11")
				}
			}
			if runtime.GOARCH == "386" {
				flags = append(flags, "-m32")
			}
			host := filepath.Join(dir, libraryName)
			command(t, cpp, append(append([]string(nil), flags...), libraryFlag, "testdata/unwind_cxx_host.cpp", "-o", host)...)
			var objects []string
			for _, kind := range []string{"throw", "catch"} {
				path := filepath.Join(dir, kind+".o")
				command(t, cpp, append(append([]string(nil), flags...), "-c", "testdata/unwind_cxx_"+kind+".cpp", "-o", path)...)
				objects = append(objects, path)
			}
			observer := New(Options{})
			defer observer.Close()
			load(t, observer, host)
			for _, input := range []string{"objects", "reversed_objects", "archive"} {
				t.Run(input, func(t *testing.T) {
					paths := append([]string(nil), objects...)
					if input == "reversed_objects" {
						paths[0], paths[1] = paths[1], paths[0]
					}
					if input == "archive" {
						archive := filepath.Join(dir, "cxx.a")
						command(t, "ar", append([]string{"rcs", archive}, objects...)...)
						paths = []string{archive}
					}
					session := New(Options{RegisterUnwind: true})
					defer session.Close()
					load(t, session, append([]string{host}, paths...)...)
					if err := session.Link("missing_cxx_root"); err == nil {
						t.Fatal("missing root accepted")
					}
					for _, test := range []struct {
						name         string
						a, b, events int32
					}{
						{"integer_rethrow", 20, 22, 111}, {"double", -20, 62, 11}, {"library_throw", 99, 0, 10},
					} {
						t.Run(test.name, func(t *testing.T) {
							call(t, observer, "cxx_events", 1, 0, 0)
							call(t, session, "cxx_catch", test.a, test.b, 42)
							call(t, observer, "cxx_events", 1, 0, test.events)
						})
					}
					call(t, observer, "cxx_has_frame", 0, 0, 1)
					if compact && session.image.compactUsed == 0 {
						t.Fatal("default native C++ input did not use compact unwind")
					}
					if compact && runtime.GOARCH == "amd64" && strings.Contains(strings.Join(extra, " "), "-fno-stack-check") {
						indirect := false
						for offset := uint64(0); offset < session.image.compactUsed; offset += 80 {
							encoding := binary.LittleEndian.Uint32(session.image.mem[session.image.compactStart+offset+68:])
							indirect = indirect || encoding&0x0f000000 == 0x03000000
						}
						if !indirect {
							t.Fatal("large-stack native call did not use indirect compact encoding")
						}
					}
					// Native exceptions and frame registration remain valid while a
					// retained entry finishes work during session retirement.
					symbol, err := session.Resolve("cxx_catch")
					if err != nil {
						t.Fatal(err)
					}
					lease, err := symbol.Acquire()
					if err != nil {
						t.Fatal(err)
					}
					defer lease.Close()
					closed := make(chan error, 1)
					go func() { closed <- session.Close() }()
					waitSessionRetired(t, session)
					if err := lease.WithAddress(func(address uintptr) error {
						result, err := examplecall.Int32(address, 20, 22)
						if result != 42 {
							t.Errorf("retiring C++ call: %d", result)
						}
						return err
					}); err != nil {
						t.Fatal(err)
					}
					call(t, observer, "cxx_has_frame", 0, 0, 1)
					call(t, observer, "cxx_events", 1, 0, 111)
					if err := lease.Close(); err != nil {
						t.Fatal(err)
					}
					if err := <-closed; err != nil {
						t.Fatal(err)
					}
					call(t, observer, "cxx_has_frame", 0, 0, 0)
				})
			}
			t.Run("concurrent_images", func(t *testing.T) {
				owners := make([]*Session, 10) // Exceeds libunwind's finder-slot limit.
				for index := range owners {
					owners[index] = New(Options{RegisterUnwind: true})
					defer owners[index].Close()
					load(t, owners[index], append([]string{host}, objects...)...)
					if err := owners[index].Link("cxx_catch"); err != nil {
						t.Fatal(err)
					}
					if runtime.GOOS == "linux" && runtime.GOARCH != "386" {
						im := owners[index].image
						if im.frameMappingPolicy() == nil || !gccFrameMappingSafe(uint64(im.base), uint64(len(im.mem))) {
							t.Fatal("libgcc image spans a low-32-bit null PC")
						}
					}
					call(t, owners[index], "cxx_catch", 20, 22, 42)
				}
				for _, owner := range owners[:9] {
					if err := owner.Close(); err != nil {
						t.Fatal(err)
					}
				}
				call(t, owners[9], "cxx_catch", 20, 22, 42)
				call(t, observer, "cxx_has_frame", 0, 0, 1)
				if err := owners[9].Close(); err != nil {
					t.Fatal(err)
				}
				call(t, observer, "cxx_has_frame", 0, 0, 0)
				call(t, observer, "cxx_events", 1, 0, 1221)
			})
			if runtime.GOOS == "darwin" {
				t.Run("registration_failure", func(t *testing.T) {
					count, err := callInt32(observer, "cxx_unwind_slots", 1, 0)
					if err != nil || count <= 0 || count > 8 {
						t.Fatalf("fill native finder slots: %d, %v", count, err)
					}
					filled := true
					defer func() {
						if filled {
							call(t, observer, "cxx_unwind_slots", 0, 0, count)
						}
					}()
					session := New(Options{RegisterUnwind: true})
					defer session.Close()
					load(t, session, append([]string{host}, objects...)...)
					if err := session.Link("cxx_catch"); err == nil || !strings.Contains(err.Error(), "dynamic Mach-O unwind registration") {
						t.Fatalf("exhausted lookup registry: %v", err)
					}
					call(t, observer, "cxx_unwind_slots", 0, 0, count)
					filled = false
					call(t, session, "cxx_catch", 20, 22, 42)
					if err := session.Close(); err != nil {
						t.Fatal(err)
					}
					call(t, observer, "cxx_has_frame", 0, 0, 0)
					call(t, observer, "cxx_events", 1, 0, 111)
				})
			}
			plain := New(Options{})
			defer plain.Close()
			load(t, plain, append([]string{host}, objects...)...)
			call(t, plain, "cxx_plain", 20, 22, 42)
			if plain.image.unwind != nil {
				t.Fatal("default loading registered language frames")
			}
			for _, object := range plain.image.objects {
				for _, section := range object.sections {
					if section != nil && (elfUnwindSection(section.name) || section.name == "__eh_frame" || section.name == "__compact_unwind" || lsdaSection(object.info.Format, section.name)) {
						t.Fatal("default loading retained exception data")
					}
				}
			}
		})
	}
}
