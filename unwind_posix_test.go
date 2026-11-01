//go:build linux || darwin

package dylib

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNativePOSIXFrameRegistration(t *testing.T) {
	testNativePOSIXFrameRegistration(t)
}

func testNativePOSIXFrameRegistration(t *testing.T, extra ...string) {
	testNativePOSIXFrameInputs(t, nil, extra...)
}

func testNativePOSIXFrameInputs(t *testing.T, edit func(*testing.T, string), extra ...string) {
	needNative(t)
	dir := t.TempDir()
	name, flag := "unwind_host.so", "-shared"
	if runtime.GOOS == "darwin" {
		name, flag = "unwind_host.dylib", "-dynamiclib"
	}
	host := filepath.Join(dir, name)
	args := []string{flag, "-fPIC", "-O0", "-funwind-tables", "testdata/unwind_host_posix.c", "-o", host}
	if runtime.GOOS == "linux" {
		args = append(args, "-lgcc_s", "-ldl")
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" {
		args = append(args, "--target=x86_64-apple-macosx11")
	}
	if runtime.GOARCH == "386" {
		args = append(args, "-m32")
	}
	args = append(args, extra...)
	command(t, compiler(), args...)
	obj := compile(t, "testdata/unwind_frames.c", filepath.Join(dir, "frames.o"), append([]string{"-funwind-tables"}, extra...)...)
	if edit != nil {
		edit(t, obj)
	}
	observer := New(Options{})
	defer observer.Close()
	load(t, observer, host)
	for _, archive := range []bool{false, true} {
		input := obj
		if archive {
			input = filepath.Join(dir, "frames.a")
			command(t, "ar", "rcs", input, obj)
		}
		s := New(Options{RegisterUnwind: true})
		defer s.Close()
		load(t, s, host, input)
		call(t, s, "unwind_root", 20, 22, 42)
		call(t, observer, "has_saved_frame", 0, 0, 1)
		owned := s.image.unwind
		if owned == nil || !owned.registered || len(owned.frames) == 0 && owned.dynamic == 0 || owned.provider == 0 {
			t.Fatal("unwind registration has no native owner")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		call(t, observer, "has_saved_frame", 0, 0, 0)
		if owned.registered || len(owned.frames) != 0 || owned.dynamic != 0 || owned.provider != 0 {
			t.Fatal("registration survived Close")
		}
	}
	if runtime.GOOS == "linux" {
		// Fill the last real FDE's CFI with no-ops up to a complete host page.
		// A following nonzero data section must not become libgcc's terminator.
		padded := New(Options{RegisterUnwind: true})
		defer padded.Close()
		load(t, padded, host, obj)
		o := padded.files[0].obj
		for _, frame := range o.sections {
			if frame == nil || frame.name != ".eh_frame" {
				continue
			}
			last := 0
			for offset := 0; offset < len(frame.data); {
				size := int(binary.LittleEndian.Uint32(frame.data[offset:]))
				if size == 0 {
					break
				}
				last, offset = offset, offset+4+size
			}
			if binary.LittleEndian.Uint32(frame.data[last+4:]) == 0 {
				t.Fatal("last producer record is not an FDE")
			}
			page := os.Getpagesize()
			frame.data = append(frame.data, make([]byte, page-len(frame.data))...)
			frame.size = uint64(page)
			binary.LittleEndian.PutUint32(frame.data[last:], uint32(page-last-4))
		}
		o.sections = append(o.sections, &section{name: ".tail_probe", size: 4, data: []byte{255, 255, 255, 255}})
		call(t, padded, "unwind_root", 20, 22, 42)
		if err := padded.Close(); err != nil {
			t.Fatal(err)
		}
		call(t, observer, "has_saved_frame", 0, 0, 0)
	}
	defaultSession := New(Options{})
	defer defaultSession.Close()
	load(t, defaultSession, host, obj)
	if runtime.GOOS == "linux" {
		call(t, defaultSession, "unwind_root", 20, 22, -2)
	} else {
		result, err := callInt32(defaultSession, "unwind_root", 20, 22)
		if err != nil || result != 42 && result != -1 && result != -2 {
			t.Fatalf("default frame result: %d, %v", result, err)
		}
	}
	call(t, observer, "has_saved_frame", 0, 0, 0)
	// A bad table fails before publication and allows correcting the staged
	// bytes; registration is never reused across a validation retry.
	validation := New(Options{RegisterUnwind: true})
	defer validation.Close()
	load(t, validation, host, obj)
	var metadata *section
	for _, section := range validation.files[0].obj.sections {
		if section != nil && (section.name == ".eh_frame" || section.name == "__eh_frame") {
			metadata = section
			break
		}
	}
	if metadata == nil {
		t.Fatal("producer .eh_frame missing")
	}
	original := metadata.data[8]
	metadata.data[8] = 99
	if err := validation.Link("unwind_root"); err == nil || validation.image != nil || validation.initErr != nil {
		t.Fatalf("invalid frame published or became permanent: %v", err)
	}
	metadata.data[8] = original
	call(t, validation, "unwind_root", 20, 22, 42)
	if err := validation.Close(); err != nil {
		t.Fatal(err)
	}
	call(t, observer, "has_saved_frame", 0, 0, 0)

	lifecycleHost, _, read := lifecycleObserver(t, dir)
	failed := compile(t, "testdata/lifecycle_cinit.c", filepath.Join(dir, "failed.o"), "-DFAIL_CODE=7", "-funwind-tables", "-DFORCE_DWARF")
	s := New(Options{RegisterUnwind: true})
	defer s.Close()
	load(t, s, lifecycleHost)
	loadCInitializerFixture(t, s, failed)
	if err := s.Link("cinit_root"); !errors.Is(err, ErrInitialization) || s.failedImage == nil || s.failedImage.unwind == nil || !s.failedImage.unwind.registered {
		t.Fatalf("failed initializer unwind ownership: %v", err)
	}
	owned := s.failedImage.unwind
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if owned.registered || len(owned.frames) != 0 || owned.dynamic != 0 || owned.provider != 0 {
		t.Fatal("failed image retained registration")
	}
	expectEvents(t, read, 1, 2, 7, 8)
}
