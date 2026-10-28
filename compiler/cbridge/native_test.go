//go:build cgo && (linux || darwin || windows)

package cbridge_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cbridge"
	"github.com/ramblelabs/dylib-go/compiler/clang"
	"github.com/ramblelabs/dylib-go/examples/headerbridge"
)

func tool(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		if os.Getenv("DYLIB_TEST_REQUIRE_TOOLS") == "1" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	return path
}

func flags() []string {
	if runtime.GOARCH == "386" {
		if runtime.GOOS == "windows" {
			args := []string{"--target=i686-w64-windows-gnu"}
			if root := os.Getenv("DYLIB_NATIVE_SYSROOT"); root != "" {
				args = append(args, "--sysroot="+root, "--rtlib=libgcc")
			}
			return args
		}
		return []string{"-m32"}
	}
	return nil
}

func compile(t *testing.T, compiler string, args ...string) {
	t.Helper()
	if out, err := exec.Command(compiler, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", compiler, args, err, out)
	}
}

var names = []string{"sum", "native_control", "scalars", "notify", "pointer", "datum", "callback", "float_gap", "empty_lane", "float_tail", "packed_bits", "large", "envelope", "variable", "record_tail", "exhausted", "const_pair", "const_packet", "const_tail", "const_stack", "const_blocks"}

func model(t *testing.T, compiler string, extra ...string) (*clang.Header, *cbridge.Facade) {
	t.Helper()
	path, err := filepath.Abs("testdata/exports.h")
	if err != nil {
		t.Fatal(err)
	}
	parseFlags := append(flags(), extra...)
	h, err := clang.Parse(context.Background(), path, clang.Options{Compiler: compiler, Functions: names, NominalRecords: true, Flags: parseFlags})
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range h.Functions {
		switch d.Name {
		case "variable":
			h.Functions[i], err = d.WithTail(abi.I8, abi.F32)
		case "record_tail", "const_tail":
			name := "FloatGap"
			if d.Name == "const_tail" {
				name = "ConstPair"
			}
			record, lookupErr := h.LookupRecord(name)
			if lookupErr != nil {
				t.Fatal(lookupErr)
			}
			d.Signature.Args = append(d.Signature.Args, abi.Struct)
			d.Signature.ArgTypes = []abi.TypeDesc{{Type: abi.I32}, record.Description}
			h.Functions[i] = d
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	functions := []cbridge.Function{}
	for _, name := range names {
		functions = append(functions, cbridge.Function{Name: name, Symbol: "bridge_" + name})
	}
	f, err := cbridge.Generate(filepath.ToSlash(path), h, functions)
	if err != nil {
		t.Fatal(err)
	}
	return h, f
}

func TestNativeOriginalHeaderBridge(t *testing.T) {
	compiler := os.Getenv("CLANG")
	if compiler == "" {
		compiler = "clang"
	}
	compiler = tool(t, compiler)
	testNativeHeader(t, compiler, compiler)
}

func TestNativeHeaderGlobalPacking(t *testing.T) {
	compiler := clangTool(t)
	testNativeHeader(t, compiler, compiler, "-fpack-struct=1")
}

func testNativeHeader(t *testing.T, parser, compiler string, extra ...string) {
	t.Helper()
	h, f := model(t, parser, extra...)
	testNativeFacade(t, compiler, h, f, "testdata/exports.c", checkCalls, extra...)
}

func testNativeFacade(t *testing.T, compiler string, h *clang.Header, f *cbridge.Facade, producer string, check func(*testing.T, *dylib.Session, *clang.Header, *cbridge.Facade), extra ...string) {
	t.Helper()
	testNativeFacadeOptimizations(t, compiler, h, f, producer, check, []string{"-O0", "-O2"}, nil, extra...)
}

func testNativeFacadeOptimizations(t *testing.T, compiler string, h *clang.Header, f *cbridge.Facade, producer string, check func(*testing.T, *dylib.Session, *clang.Header, *cbridge.Facade), optimizations, runtimeArchives []string, extra ...string) {
	t.Helper()
	for _, optimization := range optimizations {
		t.Run(optimization, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "facade.c")
			if err := os.WriteFile(source, f.CSource, 0600); err != nil {
				t.Fatal(err)
			}
			objects := []string{}
			for i, path := range []string{producer, source} {
				object := filepath.Join(dir, fmt.Sprintf("part%d.o", i))
				args := append([]string{"-std=c17", "-ffreestanding", "-fno-stack-protector", optimization, "-c"}, flags()...)
				args = append(args, extra...)
				if runtime.GOOS != "windows" {
					args = append(args, "-fPIC")
				}
				compile(t, compiler, append(args, path, "-o", object)...)
				objects = append(objects, object)
			}
			archive := filepath.Join(dir, "facade.a")
			archiver := "ar"
			if runtime.GOOS == "windows" {
				archiver = "llvm-ar"
			}
			compile(t, tool(t, archiver), append([]string{"rcs", archive}, objects...)...)
			library := filepath.Join(dir, "facade.so")
			link := []string{"-shared"}
			if runtime.GOOS == "darwin" {
				library = filepath.Join(dir, "facade.dylib")
				link = []string{"-dynamiclib"}
			}
			if runtime.GOOS == "windows" {
				library = filepath.Join(dir, "facade.dll")
				link = append(link, "-Wl,--export-all-symbols")
			}
			link = append(link, flags()...)
			link = append(link, objects...)
			compile(t, compiler, append(link, "-o", library)...)
			for _, input := range []struct {
				name  string
				paths []string
			}{{"object", objects}, {"archive", []string{archive}}, {"library", []string{library}}} {
				t.Run(input.name, func(t *testing.T) {
					s := dylib.New(dylib.Options{ProcessSymbols: runtime.GOOS != "windows"})
					defer s.Close()
					if runtime.GOOS == "windows" {
						directory := "System32"
						if runtime.GOARCH == "386" && (os.Getenv("PROCESSOR_ARCHITEW6432") != "" || os.Getenv("PROCESSOR_ARCHITECTURE") == "AMD64") {
							directory = "SysWOW64"
						}
						if err := s.Load(filepath.Join(os.Getenv("SystemRoot"), directory, "msvcrt.dll")); err != nil {
							t.Fatal(err)
						}
					}
					for _, path := range input.paths {
						if err := s.Load(path); err != nil {
							t.Fatal(err)
						}
					}
					for _, path := range runtimeArchives {
						if err := s.Load(path); err != nil {
							t.Fatal(err)
						}
					}
					roots := []string{}
					for _, b := range f.Functions {
						roots = append(roots, b.NativeSymbol, b.Symbol)
					}
					if err := s.Link(roots...); err != nil {
						t.Fatal(err)
					}
					check(t, s, h, f)
				})
			}
		})
	}
}

func checkCalls(t *testing.T, s *dylib.Session, h *clang.Header, f *cbridge.Facade) {
	t.Helper()
	bindings := map[string]cbridge.Binding{}
	for _, b := range f.Functions {
		bindings[b.Name] = b
	}
	call := func(name string, args ...abi.Value) abi.Value {
		t.Helper()
		v, err := headerbridge.Call(s, bindings[name], args...)
		if err != nil {
			t.Fatal(name, err)
		}
		return v
	}
	check := func(name string, got, want abi.Value) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s got %+v want %+v", name, got, want)
		}
	}
	record := func(name string) abi.TypeDesc {
		t.Helper()
		r, err := h.LookupRecord(name)
		if err != nil {
			t.Fatal(err)
		}
		return r.Description
	}
	value := func(d abi.TypeDesc, fields ...abi.Value) abi.Value {
		t.Helper()
		v, err := abi.StructValue(d, fields...)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	check("sum", call("sum", abi.Int32(20), abi.Int32(22)), abi.Int32(42))
	check("native_control", call("native_control"), abi.Int32(42))
	check("scalars", call("scalars", abi.Int8(-128), abi.Uint8(255), abi.Int16(-32768), abi.Uint16(65535), abi.Int32(-2147483648), abi.Uint32(^uint32(0)), abi.Int64(-9223372036854775808), abi.Uint64(^uint64(0)), abi.Float32(20.5), abi.Float64(22.5), abi.Boolean(true)), abi.Uint64(42))
	ptr := call("datum")
	if ptr.Bits == 0 {
		t.Fatal("null datum")
	}
	check("pointer", call("pointer", ptr), ptr)
	check("notify", call("notify", ptr), abi.Value{Type: abi.Void})
	gap, tail, empty, bits, large, envelope := record("FloatGap"), record("FloatTail"), record("EmptyLane"), record("PackedBits"), record("Large"), record("Envelope")
	check("float_gap", call("float_gap", value(gap, abi.Float32(20)), abi.Int32(22)), value(gap, abi.Float32(42)))
	check("float_tail", call("float_tail", value(tail, abi.Float64(40.5))), value(tail, abi.Float64(42)))
	check("empty_lane", call("empty_lane", value(empty, abi.Int32(40))), value(empty, abi.Int32(42)))
	p := value(bits, abi.Int32(40), abi.Uint32(3), abi.Float64(40.5), ptr)
	q := value(bits, abi.Int32(42), abi.Uint32(4), abi.Float64(42), ptr)
	check("packed_bits", call("packed_bits", p), q)
	check("large", call("large", value(large, abi.Float64(1), abi.Float64(2), abi.Float64(3))), value(large, abi.Float64(2), abi.Float64(4), abi.Float64(6)))
	e := abi.Zero(envelope)
	e.Aggregate.Fields[0].Aggregate.Fields[0] = value(gap, abi.Float32(20))
	e.Aggregate.Fields[0].Aggregate.Fields[1] = value(gap, abi.Float32(21))
	e.Aggregate.Fields[1] = value(tail, abi.Float64(22))
	e.Aggregate.Fields[2] = p
	e.Aggregate.Fields[5] = call("callback")
	got := call("envelope", e)
	if got.Aggregate.Fields[0].Aggregate.Fields[0].Aggregate.Fields[0] != abi.Float32(21) || got.Aggregate.Fields[0].Aggregate.Fields[1].Aggregate.Fields[0] != abi.Float32(23) || got.Aggregate.Fields[1].Aggregate.Fields[0] != abi.Float64(25) || got.Aggregate.Fields[2].Aggregate.Fields[0] != abi.Int32(44) || got.Aggregate.Fields[3].Aggregate.Fields[1].Aggregate.Fields[2] != abi.Int16(4) {
		t.Fatal("nested envelope", got)
	}
	check("variable", call("variable", value(gap, abi.Float32(20)), abi.Int32(2), abi.Int8(20), abi.Float32(2)), value(gap, abi.Float32(42)))
	check("record_tail", call("record_tail", abi.Int32(2), value(gap, abi.Float32(40))), value(gap, abi.Float32(42)))
	args := []abi.Value{}
	for i := 1; i <= 8; i++ {
		args = append(args, abi.Float64(float64(i)))
	}
	args = append(args, value(gap, abi.Float32(2)), value(tail, abi.Float64(4)))
	check("exhausted", call("exhausted", args...), abi.Float64(42))
	t.Run("const_subobjects", func(t *testing.T) {
		call := func(name string, args ...abi.Value) abi.Value {
			t.Helper()
			value, err := headerbridge.Call(s, bindings[name], args...)
			if err != nil {
				t.Fatal(name, err)
			}
			return value
		}
		checkConstCalls(t, call, h, ptr)
	})
}

func checkConstCalls(t *testing.T, call func(string, ...abi.Value) abi.Value, h *clang.Header, pointer abi.Value) {
	t.Helper()
	pair, err := h.LookupRecord("ConstPair")
	if err != nil {
		t.Fatal(err)
	}
	packet, err := h.LookupRecord("ConstPacket")
	if err != nil {
		t.Fatal(err)
	}
	v := abi.Zero(pair.Description)
	v.Aggregate.Fields[0], v.Aggregate.Fields[1] = abi.Int32(20), abi.Int32(22)
	want := abi.Zero(pair.Description)
	want.Aggregate.Fields[0], want.Aggregate.Fields[1] = abi.Int32(21), abi.Int32(24)
	if got := call("const_pair", v); !reflect.DeepEqual(got, want) {
		t.Fatal("const pair", got, want)
	}
	if got := call("const_tail", abi.Int32(0), v); got != abi.Int32(42) {
		t.Fatal("const variadic record", got)
	}
	x := abi.Zero(packet.Description)
	x.Aggregate.Fields[0] = abi.Uint32(3)
	for i := range 2 {
		p := abi.Zero(pair.Description)
		p.Aggregate.Fields[0], p.Aggregate.Fields[1] = abi.Int32(int32(1+i*2)), abi.Int32(int32(2+i*2))
		x.Aggregate.Fields[1].Aggregate.Fields[i] = p
		for j := range 3 {
			x.Aggregate.Fields[2].Aggregate.Fields[i].Aggregate.Fields[j] = abi.Int16(int16(1 + i*3 + j))
		}
	}
	x.Aggregate.Fields[3], x.Aggregate.Fields[4] = call("callback"), pointer
	y := call("const_packet", x)
	if y.Aggregate.Fields[0] != abi.Uint32(4) || y.Aggregate.Fields[1].Aggregate.Fields[1].Aggregate.Fields[1] != abi.Int32(8) || y.Aggregate.Fields[2].Aggregate.Fields[1].Aggregate.Fields[2] != abi.Int16(12) || y.Aggregate.Fields[3] != x.Aggregate.Fields[3] || y.Aggregate.Fields[4] != pointer {
		t.Fatal("const nested values and pointers", y)
	}
	args := []abi.Value{}
	for i := 1; i <= 8; i++ {
		args = append(args, abi.Int32(int32(i)))
	}
	args = append(args, x)
	if got := call("const_stack", args...); got != abi.Int32(50) {
		t.Fatal("const register exhaustion", got)
	}
	blocks, err := h.LookupRecord("ConstBlocks")
	if err != nil {
		t.Fatal(err)
	}
	z := abi.Zero(blocks.Description)
	var sum int32
	for i := range 2 {
		for j := range 65 {
			v := int16(i*100 + j - 50)
			z.Aggregate.Fields[0].Aggregate.Fields[i].Aggregate.Fields[j] = abi.Int16(v)
			sum += int32(v)
		}
	}
	if got := call("const_blocks", z); got != abi.Int32(sum) {
		t.Fatal("nested array block indices/remainder", got, sum)
	}
}
