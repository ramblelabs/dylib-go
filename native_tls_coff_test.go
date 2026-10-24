package dylib

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ramblelabs/dylib-go/internal/native"
)

func TestCOFFNativeTLSAcrossTargets(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(target+"/"+opt, func(t *testing.T) {
				path := compile(t, "testdata/coff_tls_values.c", filepath.Join(t.TempDir(), "values.o"), "--target="+target, "-ffreestanding", "-fno-emulated-tls", opt)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				f, err := parse(path, data)
				if err != nil || len(f.info.Unsupported) != 0 {
					t.Fatal(f, err)
				}
				if needed, err := checkCOFFTLS([]*object{f.obj}, true); err != nil || !needed {
					t.Fatal(needed, err)
				}
				if _, err := checkCOFFTLS([]*object{f.obj}, false); err == nil {
					t.Fatal("native TLS without opt-in")
				}
				im := &image{objects: []*object{f.obj}, pointerSize: uint64(f.info.Bits / 8), page: 4096}
				if _, err := im.layoutCOFFTLS(4096); err != nil || im.coffTLS.size < 8192+24 || im.coffTLS.align != 64 {
					t.Fatal(im.coffTLS, err)
				}
			})
		}
	}
}

func TestCOFFNativeTLSValidation(t *testing.T) {
	for _, name := range []string{"readonly", "execute", "alignment", "kind", "lifecycle", "defined_index", "weak_index", "import_index", "alias_index", "array_arch", "disabled", "empty"} {
		t.Run(name, func(t *testing.T) {
			sec := &section{name: ".tls$", size: 4, align: 4, write: true, nativeTLS: coffTLSSection}
			o := &object{info: Info{Format: "COFF", OS: "windows", Arch: "amd64", Bits: 64}, sections: []*section{nil, sec}, symbols: []symbol{{name: coffTLSIndex, global: true}}}
			enabled := true
			switch name {
			case "readonly":
				sec.write = false
			case "execute":
				sec.exec = true
			case "alignment":
				sec.align = 3
			case "kind":
				sec.nativeTLS = 3
			case "lifecycle":
				sec.lifecycle = lifecycleInit
			case "defined_index":
				o.symbols[0].section = 1
			case "weak_index":
				o.symbols[0].weak = true
			case "import_index":
				o.symbols[0].name = "__imp_" + coffTLSIndex
				o.symbols[0].imported = &coffImportSymbol{}
			case "alias_index":
				o.symbols[0].alias = &weakAlias{}
			case "array_arch":
				o.symbols[0].name = coffTLSArray
			case "disabled":
				enabled = false
			case "empty":
				sec.size = 0
			}
			needed, err := checkCOFFTLS([]*object{o}, enabled)
			if name == "empty" {
				im := &image{objects: []*object{o}, page: 4096}
				if _, err := im.layoutCOFFTLS(0); err == nil {
					t.Fatal("empty module")
				}
				return
			}
			if err == nil {
				t.Fatal("invalid TLS accepted", needed)
			}
		})
	}
}

func TestCOFFNativeTLSLayoutAndOffsets(t *testing.T) {
	a := &section{name: ".tls$Z", size: 4, align: 4, write: true, nativeTLS: coffTLSSection}
	b := &section{name: ".tls$A", size: 8192, align: 64, write: true, nativeTLS: coffTLSSection}
	o := &object{info: Info{Format: "COFF", Arch: "arm64", Bits: 64}, sections: []*section{nil, a, b}, symbols: []symbol{{section: 1, value: 0}, {section: 2, value: 8}}}
	im := &image{objects: []*object{o}, defs: map[string]definition{}, page: 4096}
	if _, err := im.layoutCOFFTLS(0); err != nil {
		t.Fatal(err)
	}
	if im.coffTLS.sections[b] != 0 || im.coffTLS.sections[a] != 8192 {
		t.Fatal("suffix order", im.coffTLS.sections)
	}
	for _, tc := range []struct {
		name           string
		typ, ins, want uint32
	}{{"word", 8, 0, 8192}, {"high", 10, 0x91400000, 0x91400800}, {"low_add", 9, 0x91000000, 0x91000000}, {"low_load", 11, 0xb9400000, 0xb9400000}} {
		t.Run(tc.name, func(t *testing.T) {
			buf := make([]byte, 4)
			le.PutUint32(buf, tc.ins)
			if done, err := im.relocateCOFFTLS(o, relocation{typ: tc.typ, symbol: 0}, buf); err != nil || !done || le.Uint32(buf) != tc.want {
				t.Fatalf("packed offset: %x %v", buf, err)
			}
		})
	}
	for _, typ := range []uint32{1, 2, 3, 13, 14} {
		t.Run(fmt.Sprint("reject", typ), func(t *testing.T) {
			if _, err := im.relocateCOFFTLS(o, relocation{typ: typ, symbol: 0}, make([]byte, 8)); err == nil {
				t.Fatal("template exposed as process address")
			}
		})
	}
}

func TestCOFFNativeTLSCleanupRetry(t *testing.T) {
	dir := t.TempDir()
	carrier := &coffTLSLayout{handle: 123, directory: filepath.Join(dir, "owned")}
	if err := os.Mkdir(carrier.directory, 0700); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("native unload unavailable")
	if err := carrier.close(func(uintptr) error { return failure }); !errors.Is(err, failure) || carrier.handle != 123 || carrier.directory == "" {
		t.Fatal("failed handle lost", err)
	}
	count := 0
	if err := carrier.close(func(h uintptr) error {
		count++
		if h != 123 {
			t.Fatal(h)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := carrier.close(func(uintptr) error { t.Fatal("unloaded twice"); return nil }); err != nil || count != 1 || carrier.handle != 0 || carrier.directory != "" {
		t.Fatal("cleanup state", err)
	}
}

func needCOFFNativeTLS(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("native COFF TLS requires Windows")
	}
	needNative(t)
}

func coffTLSFlags(dialect, opt string) []string {
	target := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH]
	if dialect == "msvc" {
		target += "-pc-windows-msvc"
	} else {
		target += "-w64-windows-gnu"
	}
	return []string{"--target=" + target, opt, "-ffreestanding", "-fno-emulated-tls"}
}

func TestNativeCOFFTLS(t *testing.T) {
	needCOFFNativeTLS(t)
	for _, dialect := range []string{"msvc", "gnu"} {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(dialect+"/"+opt, func(t *testing.T) {
				dir := t.TempDir()
				flags := coffTLSFlags(dialect, opt)
				consumer := compile(t, "testdata/emutls_consumer.c", filepath.Join(dir, "consumer.o"), flags...)
				provider := compile(t, "testdata/emutls_provider.c", filepath.Join(dir, "provider.o"), flags...)
				archive := filepath.Join(dir, "module.a")
				command(t, "ar", "rcs", archive, consumer, provider)
				for _, kind := range []string{"objects", "archive"} {
					t.Run(kind, func(t *testing.T) {
						runtime.LockOSThread()
						defer runtime.UnlockOSThread()
						s := New(Options{NativeTLS: true})
						defer s.Close()
						if kind == "archive" {
							load(t, s, archive)
						} else {
							load(t, s, consumer)
							if err := s.Link("tls_step"); err == nil || s.image != nil {
								t.Fatal("missing TLS provider accepted", err)
							}
							load(t, s, provider)
						}
						for i := int32(0); i < 100; i++ {
							call(t, s, "tls_step", 20, 22, 49+i)
						}
						if _, err := s.Resolve("tls_shared"); err == nil || !strings.Contains(err.Error(), "native TLS") {
							t.Fatal("global TLS address", err)
						}
						owner := s.image.coffTLS
						path := owner.directory
						if owner.handle == 0 || path == "" {
							t.Fatal("missing OS TLS owner")
						}
						if err := s.Close(); err != nil {
							t.Fatal(err)
						}
						if _, err := os.Stat(path); !os.IsNotExist(err) || owner.handle != 0 {
							t.Fatal("carrier retained", err)
						}
					})
				}
			})
		}
	}
}

func TestNativeCOFFTLSValuesAndThreads(t *testing.T) {
	needCOFFNativeTLS(t)
	host := nativeThreadLibrary(t, "testdata/emutls_threads.c")
	runner, err := host.Resolve("tls_threads")
	if err != nil {
		t.Fatal(err)
	}
	for _, dialect := range []string{"msvc", "gnu"} {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(dialect+"/"+opt, func(t *testing.T) {
				input := compile(t, "testdata/coff_tls_values.c", filepath.Join(t.TempDir(), "values.o"), coffTLSFlags(dialect, opt)...)
				for generation := 0; generation < 2; generation++ {
					s := New(Options{NativeTLS: true})
					defer s.Close()
					load(t, s, input)
					step, err := s.Resolve("tls_values")
					if err != nil {
						t.Fatal(err)
					}
					if err := runner.WithAddress(func(run uintptr) error {
						return step.WithAddress(func(fn uintptr) error { native.CallPointer(run, fn); return nil })
					}); err != nil {
						t.Fatal(err)
					}
					call(t, host, "tls_thread_result", 0, 0, 0)
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestNativeCOFFTLSRetirement(t *testing.T) {
	needCOFFNativeTLS(t)
	host := nativeThreadLibrary(t, "testdata/emutls_threads.c")
	start, err := host.Resolve("tls_threads_pause")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var inputs []string
	for _, part := range []string{"consumer", "provider"} {
		inputs = append(inputs, compile(t, "testdata/emutls_"+part+".c", filepath.Join(dir, part+".o"), "-O2", "-fno-emulated-tls"))
	}
	for _, mode := range []string{"owner_before_join", "concurrent_exit"} {
		t.Run(mode, func(t *testing.T) {
			s := New(Options{NativeTLS: true})
			defer s.Close()
			load(t, s, inputs...)
			step, err := s.Resolve("tls_step")
			if err != nil {
				t.Fatal(err)
			}
			owner := s.image.coffTLS
			lease, err := step.Acquire()
			if err != nil {
				t.Fatal(err)
			}
			joined := false
			defer func() {
				defer lease.Close()
				if !joined {
					call(t, host, "tls_threads_release", 0, 0, 0)
				}
			}()
			address, err := lease.Address()
			if err != nil {
				t.Fatal(err)
			}
			if err := start.WithAddress(func(run uintptr) error { native.CallPointer(run, address); return nil }); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if n, err := callInt32(host, "tls_threads_ready", 0, 0); err == nil && n == 4 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("workers did not reach retirement barrier")
				}
				time.Sleep(time.Millisecond)
			}
			closed := make(chan error, 1)
			go func() { closed <- s.Close() }()
			waitSessionRetired(t, s)
			select {
			case <-closed:
				t.Fatal("carrier ignored code lease")
			default:
			}
			lease.Close()
			if mode == "concurrent_exit" {
				call(t, host, "tls_threads_release", 0, 0, 0)
				joined = true
			}
			if err := <-closed; err != nil || owner.handle != 0 || owner.directory != "" {
				t.Fatal("retirement cleanup", err)
			}
			if !joined {
				call(t, host, "tls_threads_release", 0, 0, 0)
				joined = true
			}
		})
	}
}

func TestNativeCOFFTLSExistingThreads(t *testing.T) {
	needCOFFNativeTLS(t)
	host := nativeThreadLibrary(t, "testdata/coff_tls_threads.c")
	start, err := host.Resolve("tls_existing_start")
	if err != nil {
		t.Fatal(err)
	}
	run, err := host.Resolve("tls_existing_run")
	if err != nil {
		t.Fatal(err)
	}
	for _, dialect := range []string{"msvc", "gnu"} {
		t.Run(dialect, func(t *testing.T) {
			input := compile(t, "testdata/coff_tls_values.c", filepath.Join(t.TempDir(), "values.o"), coffTLSFlags(dialect, "-O2")...)
			if err := start.WithAddress(func(fn uintptr) error { native.CallVoid(fn); return nil }); err != nil {
				t.Fatal(err)
			}
			joined := false
			defer func() {
				if !joined {
					run.WithAddress(func(fn uintptr) error { native.CallPointer(fn, 0); return nil })
				}
			}()
			s := New(Options{NativeTLS: true})
			defer s.Close()
			load(t, s, input)
			step, err := s.Resolve("tls_values")
			if err != nil {
				t.Fatal(err)
			}
			if err := run.WithAddress(func(fn uintptr) error {
				return step.WithAddress(func(entry uintptr) error { native.CallPointer(fn, entry); return nil })
			}); err != nil {
				t.Fatal(err)
			}
			joined = true
			call(t, host, "tls_existing_result", 0, 0, 0)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeCOFFTLSIsolation(t *testing.T) {
	needCOFFNativeTLS(t)
	for _, dialect := range []string{"msvc", "gnu"} {
		t.Run(dialect, func(t *testing.T) {
			input := compile(t, "testdata/coff_tls_values.c", filepath.Join(t.TempDir(), "values.o"), coffTLSFlags(dialect, "-O2")...)
			ready := make(chan struct{}, 2)
			begin, release := make(chan struct{}), make(chan struct{})
			done := make(chan error, 2)
			var workers sync.WaitGroup
			var steps [2]*Symbol
			for i := 0; i < 2; i++ {
				workers.Add(1)
				go func() {
					defer workers.Done()
					runtime.LockOSThread()
					defer runtime.UnlockOSThread()
					ready <- struct{}{}
					<-begin
					if steps[0] == nil || steps[1] == nil {
						done <- fmt.Errorf("TLS setup failed")
						<-release
						return
					}
					var failure error
					for n := int32(0); n < 100; n++ {
						for _, step := range steps {
							got, err := callSymbolInt32(step, 20, 22)
							if err != nil || got != 49+n {
								failure = fmt.Errorf("independent native TLS module: %d %v", got, err)
								break
							}
						}
						if failure != nil {
							break
						}
					}
					done <- failure
					<-release
				}()
			}
			began := false
			defer func() {
				if !began {
					close(begin)
				}
				close(release)
				workers.Wait()
			}()
			<-ready
			<-ready
			var sessions [2]*Session
			for i := range sessions {
				s := New(Options{NativeTLS: true})
				sessions[i] = s
				defer s.Close()
				load(t, s, input)
				step, err := s.Resolve("tls_values")
				if err != nil {
					close(begin)
					began = true
					t.Fatal(err)
				}
				steps[i] = step
			}
			if le.Uint32(sessions[0].image.mem[sessions[0].image.coffTLS.index:]) == le.Uint32(sessions[1].image.mem[sessions[1].image.coffTLS.index:]) {
				close(begin)
				began = true
				t.Fatal("TLS indices shared across images")
			}
			close(begin)
			began = true
			for i := 0; i < 2; i++ {
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}
			for _, s := range sessions {
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestNativeCOFFTLSLifecycle(t *testing.T) { needCOFFNativeTLS(t); testNativeTLSLifecycle(t) }

func TestNativeCOFFTLSOptIn(t *testing.T) {
	needCOFFNativeTLS(t)
	input := compile(t, "testdata/coff_tls_values.c", filepath.Join(t.TempDir(), "values.o"), "-fno-emulated-tls")
	s := New(Options{})
	defer s.Close()
	load(t, s, input)
	if err := s.Link("tls_values"); err == nil || !strings.Contains(err.Error(), "Options.NativeTLS") || s.image != nil {
		t.Fatal("missing opt-in", err)
	}
	for _, name := range []string{coffTLSIndex, coffTLSArray, "__imp_" + coffTLSIndex} {
		t.Run(name, func(t *testing.T) {
			if err := s.Define(name, 1); err == nil {
				t.Fatal("runtime index override", name)
			}
		})
	}
}
