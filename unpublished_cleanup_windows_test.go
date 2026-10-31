//go:build windows

package dylib

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestNativeUnpublishedCOFFTLSCleanupRetry(t *testing.T) {
	needCOFFNativeTLS(t)
	s := New(Options{NativeTLS: true})
	defer s.Close()
	input := compile(t, "testdata/coff_tls_values.c", filepath.Join(t.TempDir(), "values.o"), "-fno-emulated-tls")
	load(t, s, input)
	objects, err := s.selectObjects([]string{"tls_values"})
	if err != nil {
		t.Fatal(err)
	}
	defs, err := definitions(objects)
	if err != nil {
		t.Fatal(err)
	}
	im, err := newImage(objects, defs, s.external, nil, false, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if im.mem != nil && s.failedImage != im {
			if err := im.close(); err != nil {
				t.Error(err)
			}
		}
	}()
	owner, directory := im.coffTLS, im.coffTLS.directory
	filename, err := syscall.UTF16PtrFromString(filepath.Join(directory, filepath.Base(directory)+".dll"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(filename, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if h != syscall.InvalidHandle {
			syscall.CloseHandle(h)
		}
	}()
	_, cause := im.lookup("absent_root")
	if cause == nil {
		t.Fatal("expected a missing root")
	}
	s.mu.Lock()
	err = s.discardUnpublishedImage(im, cause)
	s.mu.Unlock()
	if !errors.Is(err, cause) || err == cause || s.failedImage != im || s.image != nil || im.mem == nil || owner.handle != 0 || owner.directory != directory {
		t.Fatal("unpublished carrier deletion lost ownership", err)
	}
	if err := s.Close(); err == nil || s.failedImage != im || im.mem == nil || owner.directory != directory {
		t.Fatal("locked carrier retry lost ownership", err)
	}
	if err := syscall.CloseHandle(h); err != nil {
		t.Fatal(err)
	}
	h = syscall.InvalidHandle
	if err := s.Close(); err != nil || s.failedImage != nil || im.mem != nil || owner.directory != "" {
		t.Fatal("unpublished carrier cleanup did not finish", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("unpublished carrier directory retained", err)
	}
}
