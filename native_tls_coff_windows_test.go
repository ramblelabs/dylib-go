//go:build windows

package dylib

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestNativeCOFFTLSCleanupRetry(t *testing.T) {
	needCOFFNativeTLS(t)
	input := compile(t, "testdata/coff_tls_values.c", filepath.Join(t.TempDir(), "values.o"), "-fno-emulated-tls")
	s := New(Options{NativeTLS: true})
	defer s.Close()
	load(t, s, input)
	if _, err := s.Resolve("tls_values"); err != nil {
		t.Fatal(err)
	}
	im := s.image
	owner := im.coffTLS
	directory := owner.directory
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
	if err := s.Close(); err == nil || s.failedImage != im || owner.handle != 0 || owner.directory != directory || im.mem == nil {
		t.Fatal("failed deletion lost its owner", err)
	}
	if err := syscall.CloseHandle(h); err != nil {
		t.Fatal(err)
	}
	h = syscall.InvalidHandle
	if err := s.Close(); err != nil || s.failedImage != nil || im.mem != nil || owner.directory != "" {
		t.Fatal("carrier cleanup retry", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("carrier directory retained", err)
	}
}

func TestNativeCOFFTLSFailedInitialization(t *testing.T) {
	needCOFFNativeTLS(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "failure.c")
	if err := os.WriteFile(source, []byte("static _Thread_local int value=7;\nstatic int fail(void){return value++==7?17:23;}\n__attribute__((used,section(\".CRT$XIC\"))) static int (*const init)(void)=fail;\nint tls_failed(void){return value;}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input := compile(t, source, filepath.Join(dir, "failure.o"), "-fno-emulated-tls")
	s := New(Options{NativeTLS: true})
	defer s.Close()
	load(t, s, input)
	if err := s.Link("tls_failed"); !errors.Is(err, ErrInitialization) || s.image != nil || s.failedImage == nil {
		t.Fatal("failed native TLS initialization", err)
	}
	owner := s.failedImage.coffTLS
	path := owner.directory
	if owner.handle == 0 || path == "" {
		t.Fatal("failed initializer lost its carrier")
	}
	if err := s.Close(); err != nil || owner.handle != 0 || owner.directory != "" {
		t.Fatal("failed initialization cleanup", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed initializer carrier retained", err)
	}
}
