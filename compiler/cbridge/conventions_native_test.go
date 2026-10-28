//go:build cgo && (linux || darwin || windows)

package cbridge_test

import (
	"reflect"
	"runtime"
	"strings"
	"testing"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cbridge"
	"github.com/ramblelabs/dylib-go/compiler/clang"
	"github.com/ramblelabs/dylib-go/examples/headerbridge"
)

func TestNativeHeaderCallingConventions(t *testing.T) {
	compiler := clangTool(t)
	testNativeConventions(t, compiler, compiler)
}

func testNativeConventions(t *testing.T, parser, compiler string) {
	t.Helper()
	variants := []string{"host"}
	if runtime.GOOS == "windows" {
		variants = []string{"gnu", "msvc"}
	}
	for _, variant := range variants {
		for _, packing := range []string{"default", "packed"} {
			t.Run(variant+"/"+packing, func(t *testing.T) {
				extra := []string{}
				target := ""
				if variant != "host" {
					triple := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH]
					suffix := "-w64-windows-gnu"
					if variant == "msvc" {
						suffix = "-pc-windows-msvc"
					}
					target = triple + suffix
					extra = append(extra, "--target="+target)
				}
				if packing == "packed" {
					extra = append(extra, "-fpack-struct=1")
				}
				h, f := conventionModel(t, parser, target, append(flags(), extra...)...)
				if target != "" && !strings.HasPrefix(h.Target.Triple, target) {
					t.Fatalf("parser target %s; producer target %s", h.Target.Triple, target)
				}
				checkConventionMetadata(t, h, f)
				testNativeFacade(t, compiler, h, f, "testdata/conventions.c", checkConventionCalls, extra...)
			})
		}
	}
}

func checkConventionCalls(t *testing.T, s *dylib.Session, h *clang.Header, f *cbridge.Facade) {
	t.Helper()
	for _, cc := range conventionCases(h.Target) {
		t.Run(cc.prefix, func(t *testing.T) {
			checkConventionCase(t, s, h, f, cc)
		})
	}
}

func checkConventionCase(t *testing.T, s *dylib.Session, h *clang.Header, f *cbridge.Facade, cc conventionCase) {
	t.Helper()
	bindings := map[string]cbridge.Binding{}
	for _, b := range f.Functions {
		bindings[b.Name] = b
	}
	symbol, err := s.Resolve(bindings["cc_state"].NativeSymbol)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := symbol.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	call := func(name string, args ...abi.Value) abi.Value {
		t.Helper()
		value, err := headerbridge.Call(s, bindings[name], args...)
		if err != nil {
			t.Fatal(name, err)
		}
		return value
	}
	check := func(name string, got, want abi.Value) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %+v; want %+v", name, got, want)
		}
	}
	value := func(name string, fields ...abi.Value) abi.Value {
		t.Helper()
		r, err := h.LookupRecord(name)
		if err != nil {
			t.Fatal(err)
		}
		v, err := abi.StructValue(r.Description, fields...)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	state := call("cc_state")
	if state.Bits == 0 {
		t.Fatal("null native receiver")
	}
	pair := value("CCPair", abi.Float32(20), abi.Int32(22))
	pairResult := value("CCPair", abi.Float32(22), abi.Int32(42))
	largeRecord, err := h.LookupRecord("CCLarge")
	if err != nil {
		t.Fatal(err)
	}
	large, largeResult := abi.Zero(largeRecord.Description), abi.Zero(largeRecord.Description)
	for i := range 5 {
		large.Aggregate.Fields[0].Aggregate.Fields[i] = abi.Float64(float64(i + 1))
		increment := 2
		if i == 0 {
			increment = 20
		}
		largeResult.Aggregate.Fields[0].Aggregate.Fields[i] = abi.Float64(float64(i + 1 + increment))
	}
	large.Aggregate.Fields[1], largeResult.Aggregate.Fields[1] = abi.Int32(6), abi.Int32(7)
	hfa := value("CCHFA", abi.Float32(1), abi.Float32(2), abi.Float32(3), abi.Float32(4))
	hfaResult := value("CCHFA", abi.Float32(3), abi.Float32(4), abi.Float32(5), abi.Float32(6))
	name := cc.prefix
	check(name+" control", call(name+"_control"), abi.Int32(42))
	check(name+" scalar", call(name+"_scalar", state, abi.Int32(20), abi.Float64(2.5)), abi.Int32(42))
	check(name+" pointer", call(name+"_pointer", state, state), state)
	check(name+" null pointer", call(name+"_pointer", state, abi.Ptr(0)), state)
	check(name+" record", call(name+"_pair", state, pair, abi.Int32(2)), pairResult)
	check(name+" large result", call(name+"_large", state, large, abi.Int32(2)), largeResult)
	check(name+" HFA", call(name+"_hfa", state, hfa, abi.Float32(2)), hfaResult)
	entry := call(name + "_factory")
	check(name+" native entry", call(name+"_call", entry, state, pair, abi.Int32(2)), pairResult)
	check(name+" native forward", call(name+"_forward", state, entry, pair, abi.Int32(2)), pairResult)
	check(name+" HFA entry", call(name+"_hfa_call", call(name+"_hfa_factory"), state, hfa, abi.Float32(2)), hfaResult)
	spill := []abi.Value{state}
	for i := 1; i <= 8; i++ {
		spill = append(spill, abi.Int32(int32(i)))
	}
	for i := 1; i <= 10; i++ {
		spill = append(spill, abi.Float64(float64(i)))
	}
	spill = append(spill, pair)
	check(name+" register exhaustion", call(name+"_spill", spill...), abi.Float64(153))
}
