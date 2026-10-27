//go:build cgo && darwin && (amd64 || arm64)

package abibridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	dylib "github.com/ramblelabs/dylib-go"
	builder "github.com/ramblelabs/dylib-go/compiler/abibridge"
	examplecall "github.com/ramblelabs/dylib-go/examples/call"
)

func run(t *testing.T, program string, args ...string) string {
	t.Helper()
	output, err := exec.Command(program, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", program, args, err, output)
	}
	return string(output)
}

func fixture(t *testing.T, optimization string) string {
	t.Helper()
	dir := t.TempDir()
	sdk := strings.TrimSpace(run(t, "xcrun", "--sdk", "macosx", "--show-sdk-path"))
	arch := map[string]string{"arm64": "arm64", "amd64": "x86_64"}[runtime.GOARCH]
	triple := arch + "-apple-macosx15.4"
	gate := filepath.Join(dir, "gate.o")
	run(t, "xcrun", "clang", "-isysroot", sdk, "-target", triple, "-c", "testdata/gate.c", "-o", gate)
	managed := filepath.Join(dir, "managed.o")
	run(t, "xcrun", "clang", "-isysroot", sdk, "-target", triple, "-c", "testdata/managed.c", "-o", managed)
	library := filepath.Join(dir, "native.dylib")
	identity := sha256.Sum256([]byte(dir))
	args := []string{"swiftc", "-sdk", sdk, "-target", triple, optimization, "-enable-library-evolution", "-module-name", fmt.Sprintf("DylibSwiftFixture_%x", identity[:8]), "-import-objc-header", "testdata/gate.h"}
	run(t, "xcrun", append(append([]string(nil), args...), "-emit-library", "testdata/native.swift", gate, managed, "-o", library)...)
	ir := filepath.Join(dir, "native.ll")
	run(t, "xcrun", append(args, "-S", "-emit-ir", "testdata/native.swift", "-o", ir)...)
	data, err := os.ReadFile(ir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"fixture_i32", "fixture_mixed", "fixture_indirect", "fixture_large", "fixture_spill", "fixture_managed_initialize", "fixture_managed_read", "fixture_empty_initialize"} {
		found := false
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "define ") && strings.Contains(line, "swiftcc ") && strings.Contains(line, "@"+name+"(") {
				found = true
				if name == "fixture_indirect" && (!strings.Contains(line, "sret(") || !strings.Contains(line, "ptr")) {
					t.Fatal("resilient value lost its formally indirect ABI", line)
				}
			}
		}
		if !found {
			t.Fatal("fixture is not a native Swift entry", name)
		}
	}
	sil := filepath.Join(dir, "native.sil")
	run(t, "xcrun", append(args, "-emit-sil", "testdata/native.swift", "-o", sil)...)
	ownership, err := os.ReadFile(sil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		mode, swiftType string
		indirect        bool
	}{
		{"direct", "DirectManaged", false}, {"mixed", "MixedManaged", false}, {"large", "LargeManaged", false},
		{"indirect", "ManagedRecord", true}, {"empty", "EmptyValue", true},
	} {
		name := "fixture_owned_" + entry.mode + "_echo"
		parameter, result := "@guaranteed ", "@owned "
		if entry.indirect {
			parameter, result = "@in_guaranteed ", "@out "
		}
		found := false
		for _, line := range strings.Split(string(ownership), "\n") {
			if strings.HasPrefix(line, "sil @"+name+" : ") {
				found = strings.Contains(line, parameter+entry.swiftType) && strings.Contains(line, "-> "+result+entry.swiftType)
			}
		}
		if !found {
			t.Fatal("managed fixture lost guaranteed input / owned result SIL contract", name)
		}
		found = false
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "define ") && strings.Contains(line, "swiftcc ") && strings.Contains(line, "@"+name+"(") {
				found = true
				if strings.Contains(line, "sret(") != (entry.indirect || entry.mode == "large") {
					t.Fatal("managed result lowering differs from its declared shape", line)
				}
			}
		}
		if !found {
			t.Fatal("missing managed swiftcc entry", name)
		}
	}
	t.Log("verified guaranteed managed SIL inputs, owned results and direct/indirect swiftcc lowering")
	t.Log("verified compiler-emitted swiftcc entries and formally indirect resilient storage")
	return library
}

func open(t *testing.T, path string) *Runtime {
	t.Helper()
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}

func load(t *testing.T, path string) *dylib.Session {
	t.Helper()
	// Swift runtime metadata can outlive any individual invocation or session.
	s := dylib.New(dylib.Options{KeepLibraries: true})
	if err := s.Load(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func prepare(t *testing.T, r *Runtime, signature Signature) *Call {
	t.Helper()
	c, err := r.Prepare(signature)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}

func resolve(t *testing.T, s *dylib.Session, name string) *dylib.Symbol {
	t.Helper()
	f, err := s.Resolve(name)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func control(t *testing.T, s *dylib.Session, name string) func(int32, int32) (int32, error) {
	t.Helper()
	function := resolve(t, s, name)
	return func(a, b int32) (result int32, err error) {
		err = function.WithAddress(func(address uintptr) error {
			result, err = examplecall.Int32(address, a, b)
			return err
		})
		return result, err
	}
}

func words(values ...uint64) []byte {
	output := make([]byte, 8*len(values))
	for i, value := range values {
		binary.LittleEndian.PutUint64(output[8*i:], value)
	}
	return output
}

func int32Bytes(value int32) []byte {
	output := make([]byte, 4)
	binary.LittleEndian.PutUint32(output, uint32(value))
	return output
}

func TestNativeSwift(t *testing.T) {
	source, ffi := os.Getenv("DYLIB_ABIBRIDGE_SOURCE"), os.Getenv("DYLIB_ABIBRIDGE_LIBFFI_SOURCE")
	if source == "" || ffi == "" {
		if os.Getenv("DYLIB_TEST_REQUIRE_ABIBRIDGE") == "1" {
			t.Fatal("required pinned ABIBridge sources missing")
		}
		t.Skip("set DYLIB_ABIBRIDGE_SOURCE and DYLIB_ABIBRIDGE_LIBFFI_SOURCE for optional Swift calls")
	}
	facade := os.Getenv("DYLIB_ABIBRIDGE_FACADE")
	if facade == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		library, err := builder.Build(ctx, builder.Options{Source: source, LibffiSource: ffi})
		if err != nil {
			t.Fatal(err)
		}
		defer library.Close()
		facade = library.Path
	}
	t.Logf("ABIBridge %s; ZDLibffi %s; direct C transport without Go libffi", builder.SourceCommit, builder.LibffiCommit)
	t.Run("wire_validation", func(t *testing.T) { testWire(t, facade) })
	for _, optimization := range []struct{ name, flag string }{{"O0", "-Onone"}, {"O2", "-O"}} {
		t.Run(optimization.name, func(t *testing.T) {
			native := fixture(t, optimization.flag)
			t.Run("calls", func(t *testing.T) { testCalls(t, facade, native) })
			t.Run("pointers", func(t *testing.T) { testPointers(t, facade, native) })
			t.Run("managed", func(t *testing.T) { testManagedValues(t, facade, native) })
			t.Run("managed_calls", func(t *testing.T) { testManagedCalls(t, facade, native) })
			t.Run("retirement", func(t *testing.T) { testRetirement(t, facade, native) })
		})
	}
}

func testWire(t *testing.T, facade string) {
	r := open(t, facade)
	_, good, err := (Signature{Result: Type{Kind: Int32}, Args: []Type{{Kind: Int32}}}).encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{nil, {1}, good[:len(good)-1], append(append([]byte(nil), good...), 0), {99, 0, 0, 0, 0, 0, 0, 0}} {
		if plan, err := nativePrepare(r.prepare, data); err == nil || plan != 0 {
			if plan != 0 {
				nativeRelease(r.release, plan)
			}
			t.Fatal("malformed native signature accepted", len(data), err)
		}
	}
	bad := append([]byte(nil), good...)
	binary.LittleEndian.PutUint32(bad[12:], 8) // Result Int32 falsely reports eight bytes.
	if plan, err := nativePrepare(r.prepare, bad); err == nil || plan != 0 {
		if plan != 0 {
			nativeRelease(r.release, plan)
		}
		t.Fatal("native scalar layout mismatch accepted", err)
	}
}

func testCalls(t *testing.T, facade, native string) {
	r := open(t, facade)
	s := load(t, native)
	layout := control(t, s, "fixture_layout")
	for _, entry := range []struct{ which, index, want int32 }{{0, 0, 16}, {0, 1, 8}, {0, 2, 8}, {1, 0, 40}, {1, 1, 8}, {2, 0, 8}, {2, 1, 4}, {3, 0, 12}, {3, 1, 8}} {
		if got, err := layout(entry.which, entry.index); err != nil || got != entry.want {
			t.Fatal("Swift storage layout", entry, got, err)
		}
	}
	mixed := Type{Kind: Storage, Size: 16, Alignment: 8, Components: []Component{{Int64, 0}, {Float64, 8}}}
	padded := Type{Kind: Storage, Size: 16, Alignment: 8, Components: []Component{{Uint8, 0}, {Uint64, 8}}}
	tail := Type{Kind: Storage, Size: 12, Alignment: 8, Components: []Component{{Float64, 0}, {Int32, 8}}}
	large := Type{Kind: Storage, Size: 40, Alignment: 8}
	for i := uint32(0); i < 5; i++ {
		large.Components = append(large.Components, Component{Int64, 8 * i})
	}
	indirect := Type{Kind: Indirect, Size: 8, Alignment: 4}
	cases := []struct {
		name      string
		signature Signature
		args      [][]byte
		want      []byte
	}{
		{"i32", Signature{Type{Kind: Int32}, []Type{{Kind: Int32}, {Kind: Int32}}}, [][]byte{int32Bytes(20), int32Bytes(22)}, int32Bytes(42)},
		{"i8", Signature{Type{Kind: Int64}, []Type{{Kind: Int8}, {Kind: Uint8}}}, [][]byte{{0x80}, {0xff}}, words(127)},
		{"i8_result", Signature{Type{Kind: Int8}, []Type{{Kind: Int32}, {Kind: Int32}}}, [][]byte{int32Bytes(120), int32Bytes(8)}, []byte{0x80}},
		{"u8_result", Signature{Type{Kind: Uint8}, []Type{{Kind: Int32}, {Kind: Int32}}}, [][]byte{int32Bytes(250), int32Bytes(5)}, []byte{0xff}},
		{"i16", Signature{Type{Kind: Int16}, []Type{{Kind: Int16}, {Kind: Uint16}}}, [][]byte{{0x00, 0x80}, {0xff, 0xff}}, []byte{0xff, 0x7f}},
		{"u16", Signature{Type{Kind: Uint16}, []Type{{Kind: Uint16}}}, [][]byte{{0xfe, 0xff}}, []byte{0xff, 0xff}},
		{"u32", Signature{Type{Kind: Uint32}, []Type{{Kind: Uint32}, {Kind: Uint32}}}, [][]byte{int32Bytes(-2147483648), int32Bytes(42)}, int32Bytes(-2147483606)},
		{"u64", Signature{Type{Kind: Uint64}, []Type{{Kind: Uint64}, {Kind: Uint64}}}, [][]byte{words(1 << 63), words(42)}, words(1<<63 | 42)},
		{"f32", Signature{Type{Kind: Float32}, []Type{{Kind: Float32}, {Kind: Float32}}}, [][]byte{int32Bytes(int32(math.Float32bits(20.5))), int32Bytes(int32(math.Float32bits(21.5)))}, int32Bytes(int32(math.Float32bits(42)))},
		{"f64", Signature{Type{Kind: Float64}, []Type{{Kind: Float64}, {Kind: Float64}}}, [][]byte{words(math.Float64bits(20.5)), words(math.Float64bits(21.5))}, words(math.Float64bits(42))},
		{"mixed", Signature{mixed, []Type{mixed, {Kind: Int64}, {Kind: Float64}}}, [][]byte{words(20, math.Float64bits(20.5)), words(22), words(math.Float64bits(21.5))}, words(42, math.Float64bits(42))},
		{"padded", Signature{padded, []Type{padded}}, [][]byte{words(0xeeeeeeeeeeeeee29, 42)}, words(42, 43)},
		{"tail", Signature{tail, []Type{tail}}, [][]byte{append(words(math.Float64bits(41)), int32Bytes(41)...)}, append(words(math.Float64bits(42)), int32Bytes(42)...)},
		{"large", Signature{large, []Type{large}}, [][]byte{words(1, 2, 3, 4, 5)}, words(6, 6, 6, 6, 6)},
		{"indirect", Signature{indirect, []Type{indirect}}, [][]byte{append(int32Bytes(20), int32Bytes(22)...)}, append(int32Bytes(42), int32Bytes(-2)...)},
	}
	spill := Signature{Result: Type{Kind: Float64}}
	var spillArgs [][]byte
	for i := 1; i <= 10; i++ {
		spill.Args = append(spill.Args, Type{Kind: Int64})
		spillArgs = append(spillArgs, words(uint64(i)))
	}
	for i := 1; i <= 10; i++ {
		spill.Args = append(spill.Args, Type{Kind: Float64})
		spillArgs = append(spillArgs, words(math.Float64bits(float64(i))))
	}
	cases = append(cases, struct {
		name      string
		signature Signature
		args      [][]byte
		want      []byte
	}{"spill", spill, spillArgs, words(math.Float64bits(110))})
	for _, entry := range cases {
		t.Run(entry.name, func(t *testing.T) {
			call := prepare(t, r, entry.signature)
			function := resolve(t, s, "fixture_"+entry.name)
			for iteration := 0; iteration < 4; iteration++ {
				runtime.GC()
				got, err := call.Invoke(function, entry.args...)
				if err != nil || !bytes.Equal(got, entry.want) {
					t.Fatalf("got %x, %v; want %x", got, err, entry.want)
				}
			}
			var wg sync.WaitGroup
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					got, err := call.Invoke(function, entry.args...)
					if err != nil || !bytes.Equal(got, entry.want) {
						t.Errorf("concurrent Swift call: %x, %v", got, err)
					}
				}()
			}
			wg.Wait()
			if _, err := call.Invoke(function, []byte{0}); err == nil {
				t.Fatal("invalid buffers accepted")
			}
			if err := call.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := call.Invoke(function, entry.args...); !errors.Is(err, ErrClosed) {
				t.Fatal(err)
			}
		})
	}
}

func testPointers(t *testing.T, facade, native string) {
	r := open(t, facade)
	s := load(t, native)
	allocate := prepare(t, r, Signature{Result: Type{Kind: Pointer}})
	free := prepare(t, r, Signature{Args: []Type{{Kind: Pointer}}})
	modify := prepare(t, r, Signature{Result: Type{Kind: Int32}, Args: []Type{{Kind: Pointer}, {Kind: Int32}}})
	pointer, err := allocate.Invoke(resolve(t, s, "fixture_allocate"))
	if err != nil || len(pointer) != 8 || binary.LittleEndian.Uint64(pointer) == 0 {
		t.Fatal(pointer, err)
	}
	defer func() {
		if _, err := free.Invoke(resolve(t, s, "fixture_free"), pointer); err != nil {
			t.Error(err)
		}
	}()
	got, err := modify.Invoke(resolve(t, s, "fixture_pointer"), pointer, int32Bytes(22))
	if err != nil || !bytes.Equal(got, int32Bytes(42)) {
		t.Fatal(got, err)
	}
}

func testRetirement(t *testing.T, facade, native string) {
	r := open(t, facade)
	s := load(t, native)
	controller := load(t, native)
	gate := control(t, controller, "fixture_gate")
	if _, err := gate(2, 0); err != nil {
		t.Fatal(err)
	}
	defer gate(1, 0)
	call := prepare(t, r, Signature{Result: Type{Kind: Int32}})
	function := resolve(t, s, "fixture_block")
	done := make(chan error, 1)
	go func() {
		got, err := call.Invoke(function)
		if err == nil && !bytes.Equal(got, int32Bytes(42)) {
			err = errors.New("wrong blocked Swift result")
		}
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ready, err := gate(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if ready == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Swift call did not enter native gate")
		}
		time.Sleep(time.Millisecond)
	}
	runtimeClosed := make(chan error, 1)
	imageClosed := make(chan error, 1)
	go func() { runtimeClosed <- r.Close() }()
	go func() { imageClosed <- s.Close() }()
	for _, closed := range []chan error{runtimeClosed, imageClosed} {
		select {
		case err := <-closed:
			t.Fatal("retired active native Swift code", err)
		case <-time.After(20 * time.Millisecond):
		}
	}
	if _, err := gate(1, 0); err != nil {
		t.Fatal(err)
	}
	for _, closed := range []chan error{done, runtimeClosed, imageClosed} {
		select {
		case err := <-closed:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("native Swift retirement deadlocked")
		}
	}
	if _, err := call.Invoke(function); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}
