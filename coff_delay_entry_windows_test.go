//go:build windows

package dylib

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/ramblelabs/dylib-go/internal/native"
)

type lazyEntryProbe struct {
	mu             sync.Mutex
	target, module uintptr
	missing, name  string
	ordinal        uint16
	failure        uint32
	calls          int
	threads        map[uintptr]bool
	problem        error
}

func (p *lazyEntryProbe) snapshot() (int, map[uintptr]bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	threads := make(map[uintptr]bool, len(p.threads))
	for id := range p.threads {
		threads[id] = true
	}
	return p.calls, threads, p.problem
}

var lazyProbeRegistry struct {
	sync.Mutex
	once     sync.Once
	callback uintptr
	next     uintptr
	entries  map[uintptr]*lazyEntryProbe
}

func lazyProbeDispatch(address uintptr) uintptr {
	request, ok := native.ReadCOFFDelayRequest(address)
	if !ok {
		return 0
	}
	lazyProbeRegistry.Lock()
	probe := lazyProbeRegistry.entries[request.Owner]
	lazyProbeRegistry.Unlock()
	if probe == nil {
		native.SetCOFFDelayResult(address, 0, 0, 87, 87)
		return 0
	}
	probe.mu.Lock()
	probe.calls++
	probe.threads[request.Thread] = true
	if request.Thread == 0 || request.Slot != 0 {
		probe.problem = fmt.Errorf("invalid native request identity: %+v", request)
	}
	probe.mu.Unlock()
	// Exercise actual managed allocation/collection on both ordinary and
	// C-created threads, using only the public Windows callback boundary.
	storage := make([]byte, 64<<10)
	storage[0] = 42
	runtime.GC()
	runtime.KeepAlive(storage)
	target, module, failure := probe.target, probe.module, probe.failure
	if request.Recovering {
		module = request.Module
		if request.Address != 0 {
			target, failure = request.Address, 0
		} else if module != 0 {
			if probe.ordinal != 0 {
				target = native.LookupOrdinal(module, probe.ordinal)
			} else {
				target = native.Lookup(module, probe.name)
			}
			failure = 0
			if target == 0 {
				failure = 127
			}
		} else {
			target = 0
		}
	} else if failure == 126 {
		opened, err := native.Open(probe.missing)
		if opened != 0 {
			native.Close(opened)
		}
		if err == nil {
			probe.mu.Lock()
			probe.problem = fmt.Errorf("missing native module unexpectedly opened")
			probe.mu.Unlock()
		}
		target, module = 0, 0
	} else if failure == 127 {
		if probe.ordinal != 0 {
			target = native.LookupOrdinal(module, probe.ordinal)
		} else {
			target = native.Lookup(module, probe.name)
		}
		if target != 0 {
			probe.mu.Lock()
			probe.problem = fmt.Errorf("missing native procedure unexpectedly resolved")
			probe.mu.Unlock()
		}
	} else if failure != 0 {
		target = 0
	}
	native.SetCOFFDelayResult(address, target, module, failure, failure)
	return target
}

func TestNativeCOFFLazyEntryTerminal(t *testing.T) {
	needNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		t.Run(opt, func(t *testing.T) {
			s := nativeInputSession(t, "testdata/cofflazy/probe.c", "library", opt)
			var library string
			for path := range s.paths {
				if filepath.Ext(path) == ".dll" {
					library = path
				}
			}
			if library == "" {
				t.Fatal("missing native terminal probe library")
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			for _, failure := range []uint32{126, 127, 87} {
				t.Run(strconv.FormatUint(uint64(failure), 10), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
					defer cancel()
					command := exec.CommandContext(ctx, executable, "-test.run=^TestNativeCOFFLazyEntryTerminalChild$", "-test.v")
					command.Env = append(os.Environ(), "DYLIB_LAZY_TERMINAL_LIBRARY="+library, "DYLIB_LAZY_TERMINAL_CODE="+strconv.FormatUint(uint64(failure), 10))
					output, err := command.CombinedOutput()
					exit, ok := err.(*exec.ExitError)
					code := uint32(0xc06d0000) | failure
					// The Go executable's last continue handler reports the native
					// exception and exits with 2; other hosts retain the NT status.
					// Require the exact native report before accepting that exit.
					goFatal := ok && exit.ExitCode() == 2 &&
						bytes.Contains(output, []byte(fmt.Sprintf("Exception %#x ", code))) &&
						bytes.Contains(output, []byte("signal arrived during external code execution"))
					if ctx.Err() != nil || !ok || uint32(exit.ExitCode()) != code && !goFatal {
						t.Fatalf("native delay exception: %v, context=%v\n%s", err, ctx.Err(), output)
					}
				})
			}
		})
	}
}

func TestNativeCOFFLazyEntryTerminalChild(t *testing.T) {
	code := os.Getenv("DYLIB_LAZY_TERMINAL_CODE")
	if code == "" {
		t.Skip("native failure subprocess only")
	}
	needNative(t)
	failure, err := strconv.ParseUint(code, 10, 32)
	if err != nil || failure != 126 && failure != 127 && failure != 87 {
		t.Fatal("invalid native failure subprocess case", code, err)
	}
	s := New(Options{})
	defer s.Close()
	load(t, s, os.Getenv("DYLIB_LAZY_TERMINAL_LIBRARY"))
	target, err := s.Lookup("scalar")
	if err != nil {
		t.Fatal(err)
	}
	probe := &lazyEntryProbe{target: target, module: s.libs[0], name: "entry", failure: uint32(failure), missing: filepath.Join(t.TempDir(), "prototype.dll"), threads: make(map[uintptr]bool)}
	newLazyProbeEntry(t, s, probe)
	call(t, s, "configure_handler", 0, 0, 0)
	call(t, s, "probe", 0, 20, 42)
	t.Fatal("unhandled native delay failure returned to Go")
}

func probeNativePointer(address uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&address))
}

func probeNativeWord(t *testing.T, s *Session, name string) *uintptr {
	t.Helper()
	address, err := s.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return (*uintptr)(probeNativePointer(address))
}

func newLazyProbeEntry(t *testing.T, s *Session, probe *lazyEntryProbe) {
	t.Helper()
	slot := probeNativeWord(t, s, "factory_entry")
	cellSlot := probeNativeWord(t, s, "factory_cell")
	lazyProbeRegistry.once.Do(func() {
		lazyProbeRegistry.entries = make(map[uintptr]*lazyEntryProbe)
		lazyProbeRegistry.callback = syscall.NewCallback(lazyProbeDispatch)
	})
	lazyProbeRegistry.Lock()
	lazyProbeRegistry.next++
	id := lazyProbeRegistry.next
	lazyProbeRegistry.entries[id] = probe
	lazyProbeRegistry.Unlock()
	page := os.Getpagesize()
	mem, err := native.Alloc(2 * page)
	if err != nil {
		lazyProbeRegistry.Lock()
		delete(lazyProbeRegistry.entries, id)
		lazyProbeRegistry.Unlock()
		t.Fatal(err)
	}
	var u *runtimeFunctions
	t.Cleanup(func() {
		if err := u.close(); err != nil {
			t.Error(err) // Retain the code, registry and context on unregister failure.
			return
		}
		*slot = 0
		*cellSlot = 0
		if err := native.Free(mem); err != nil {
			t.Error(err)
			return
		}
		lazyProbeRegistry.Lock()
		delete(lazyProbeRegistry.entries, id)
		lazyProbeRegistry.Unlock()
	})
	base := uintptr(unsafe.Pointer(&mem[0]))
	width := uint64(unsafe.Sizeof(uintptr(0)))
	copy(mem[320:], "prototype.dll\x00")
	copy(mem[384:], probe.name+"\x00")
	le.PutUint32(mem[256:], 1)
	for offset, value := range map[int]uint32{4: 320, 8: 448, 12: 128, 16: 456} {
		le.PutUint32(mem[256+offset:], value)
	}
	name := base + 384
	if probe.ordinal != 0 {
		name = 0
		value := uint64(1)<<(width*8-1) | uint64(probe.ordinal)
		if width == 8 {
			le.PutUint64(mem[456:], value)
		} else {
			le.PutUint32(mem[456:], uint32(value))
		}
	} else {
		copy(mem[514:], probe.name+"\x00")
		le.PutUint32(mem[456:], 512)
	}
	if native.COFFDelayEntrySize() > 128 || !native.ConfigureCOFFDelayEntry(base, lazyProbeRegistry.callback, id, 0, base+256, base+128, base+320, name, probe.ordinal) {
		t.Fatal("configure native lazy entry")
	}
	entry, err := makeCOFFLazyEntry(runtime.GOARCH, base+128, base, native.COFFDelayHelper())
	if err != nil {
		t.Fatal(err)
	}
	code := uint64(page)
	copy(mem[code:], entry.code)
	xdata := code + alignUp(uint64(len(entry.code)), 4)
	copy(mem[xdata:], entry.unwind)
	native.ClearCache(mem[code:])
	if err := native.Protect(mem[code:], true, false); err != nil {
		t.Fatal(err)
	}
	u = &runtimeFunctions{}
	if runtime.GOARCH != "386" {
		size := 12
		if runtime.GOARCH == "arm64" {
			size = 8
		}
		u.mem, err = native.Alloc(size)
		if err != nil {
			t.Fatal(err)
		}
		le.PutUint32(u.mem, uint32(code)+entry.slow)
		if size == 12 {
			le.PutUint32(u.mem[4:], uint32(code)+uint32(len(entry.code)))
		}
		le.PutUint32(u.mem[size-4:], uint32(xdata))
		if err := native.Protect(u.mem, false, false); err != nil {
			t.Fatal(err)
		}
		if err := native.AddFunctionTable(uintptr(unsafe.Pointer(&u.mem[0])), 1, base); err != nil {
			t.Fatal(err)
		}
		u.registered = true
	}
	*slot = base + uintptr(code)
	*cellSlot = base + 128
}

func TestNativeCOFFLazyEntryBoundary(t *testing.T) {
	needNative(t)
	selected := os.Getenv("DYLIB_LAZY_BOUNDARY_OPT")
	if selected == "" {
		// A broken native exception/unwind boundary can terminate the whole
		// process without a Go traceback. Preserve its actual exit status and
		// isolate each optimization, including native DLL/TLS cleanup.
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(opt, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				pattern := "^TestNativeCOFFLazyEntryBoundary$/^" + regexp.QuoteMeta(opt) + "$"
				command := exec.CommandContext(ctx, executable, "-test.run="+pattern, "-test.v")
				command.Env = append(os.Environ(), "DYLIB_LAZY_BOUNDARY_OPT="+opt)
				output, err := command.CombinedOutput()
				if err != nil || ctx.Err() != nil {
					var status uint32
					if exit, ok := err.(*exec.ExitError); ok {
						status = uint32(exit.ExitCode())
					}
					t.Fatalf("native boundary exited with %#x: %v; context=%v\n%s", status, err, ctx.Err(), output)
				}
				if !bytes.Contains(output, []byte("--- PASS: TestNativeCOFFLazyEntryBoundary ")) {
					t.Fatalf("missing child boundary execution\n%s", output)
				}
				// Keep individual native PASS records without duplicating the
				// parent's root/optimization records or test protocol markers.
				for _, line := range strings.Split(string(output), "\n") {
					if strings.Contains(line, "--- PASS: "+t.Name()+"/") {
						t.Log(strings.TrimSpace(line))
					}
				}
			})
		}
		return
	}
	if selected != "-O0" && selected != "-O2" {
		t.Fatal("invalid native boundary optimization", selected)
	}
	runCOFFLazyEntryBoundary(t, selected)
}

// Also qualify the host's existing runtime and prior native-library state.
// Isolated exception diagnostics must not replace calls in that same process.
func TestNativeCOFFLazyEntryBoundaryInProcess(t *testing.T) {
	needNative(t)
	runCOFFLazyEntryBoundary(t, "")
}

func runCOFFLazyEntryBoundary(t *testing.T, selected string) {
	if native.COFFDelayHelper() == 0 {
		t.Fatal("native Windows delay boundary unavailable")
	}
	for _, opt := range []string{"-O0", "-O2"} {
		if selected != "" && opt != selected {
			continue
		}
		t.Run(opt, func(t *testing.T) {
			s := nativeInputSession(t, "testdata/cofflazy/probe.c", "library", opt)
			targetAddress, err := s.Lookup("targets")
			if err != nil {
				t.Fatal(err)
			}
			names := []string{"scalar", "mixed_spills", "pair", "large_sret", "hfa", "variadic", "pointers"}
			if runtime.GOARCH == "amd64" || runtime.GOARCH == "386" {
				names = append(names, "vectorcall")
			}
			if runtime.GOARCH == "386" {
				names = append(names, "stdcall", "fastcall", "thiscall")
			}
			targets := unsafe.Slice((*uintptr)(probeNativePointer(targetAddress)), len(names))
			for kind, name := range names {
				t.Run(name, func(t *testing.T) {
					probe := &lazyEntryProbe{target: targets[kind], module: s.libs[0], name: "entry", threads: make(map[uintptr]bool)}
					newLazyProbeEntry(t, s, probe)
					call(t, s, "probe", int32(kind), 20, 42)
					call(t, s, "probe", int32(kind), 20, 42)
					if calls, _, problem := probe.snapshot(); problem != nil || calls != 1 {
						t.Fatal("resolver or cached native entry", calls, problem)
					}
				})
			}
			if runtime.GOARCH != "386" {
				t.Run("virtual_unwind", func(t *testing.T) {
					probe := &lazyEntryProbe{target: targets[0], module: s.libs[0], name: "entry", threads: make(map[uintptr]bool)}
					newLazyProbeEntry(t, s, probe)
					call(t, s, "check_unwind", 0, 0, 42)
					if calls, _, problem := probe.snapshot(); calls != 0 || problem != nil {
						t.Fatal("unwind inspection invoked the resolver", calls, problem)
					}
				})
			}
			if runtime.GOARCH == "arm64" {
				t.Run("nonvolatile_fp", func(t *testing.T) {
					probe := &lazyEntryProbe{target: targets[0], module: s.libs[0], name: "entry", threads: make(map[uintptr]bool)}
					newLazyProbeEntry(t, s, probe)
					call(t, s, "check_nonvolatile_fp", 20, 0, 42)
					call(t, s, "check_nonvolatile_fp", 20, 0, 42)
					if calls, _, problem := probe.snapshot(); calls != 1 || problem != nil {
						t.Fatal("FP register preservation or cached entry", calls, problem)
					}
				})
			}
			t.Run("foreign_threads", func(t *testing.T) {
				probe := &lazyEntryProbe{target: targets[0], module: s.libs[0], name: "entry", threads: make(map[uintptr]bool)}
				newLazyProbeEntry(t, s, probe)
				call(t, s, "threads", 20, 0, 42)
				address, err := s.Lookup("probe_thread")
				if err != nil {
					t.Fatal(err)
				}
				parent := uintptr(*(*uint32)(probeNativePointer(address)))
				calls, threads, problem := probe.snapshot()
				if calls < 1 || calls > 8 || len(threads) == 0 || threads[parent] || problem != nil {
					t.Fatal("foreign thread callback identity/GC", calls, threads, parent, problem)
				}
				call(t, s, "threads", 20, 0, 42)
				if after, _, _ := probe.snapshot(); after != calls {
					t.Fatal("published native entry resolved again")
				}
			})
			for _, tc := range []struct {
				name       string
				failure    uint32
				handler    int32
				procedure  string
				ordinal    uint16
				exceptions int32
			}{
				{"module_recovery", 126, 1, "scalar", 0, 1},
				{"procedure_recovery", 127, 2, "entry", 0, 1},
				{"ordinal_recovery", 127, 2, "entry", 65535, 1},
				{"module_then_procedure", 126, 3, "entry", 0, 2},
			} {
				t.Run(tc.name, func(t *testing.T) {
					probe := &lazyEntryProbe{target: targets[0], module: s.libs[0], name: tc.procedure, ordinal: tc.ordinal, failure: tc.failure, missing: filepath.Join(t.TempDir(), "prototype.dll"), threads: make(map[uintptr]bool)}
					newLazyProbeEntry(t, s, probe)
					*probeNativeWord(t, s, "recovery_target") = probe.target
					*probeNativeWord(t, s, "recovery_module") = probe.module
					call(t, s, "configure_handler", tc.handler, 0, 0)
					t.Cleanup(func() { call(t, s, "configure_handler", 0, 0, 0) })
					call(t, s, "probe", 0, 20, 42)
					call(t, s, "probe", 0, 20, 42)
					address, err := s.Lookup("native_exceptions")
					if err != nil {
						t.Fatal(err)
					}
					count := *(*int32)(probeNativePointer(address))
					if calls, _, problem := probe.snapshot(); count != tc.exceptions || calls != int(count)+1 || problem != nil {
						t.Fatal("native exception/recovery path", count, calls, problem)
					}
				})
			}
		})
	}
}
