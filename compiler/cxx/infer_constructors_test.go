package cxx_test

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func TestNativeInferredConstructorsAcrossTargets(t *testing.T) {
	for _, target := range freeTargets {
		t.Run(target, func(t *testing.T) {
			out, err := cxxexample.InferSelectedConstructors(context.Background(), "testdata/inferred_constructors.hpp", cxx.DiscoveryOptions{Compiler: cppTool(t), Target: target, Flags: []string{"-ffreestanding"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Constructors) != 23 || len(out.Facade.Functions) != 43 {
				t.Fatal(len(out.Constructors), len(out.Facade.Functions))
			}
			long := abi.I64
			if strings.Contains(target, "windows") || strings.HasPrefix(target, "i686-") {
				long = abi.I32
			}
			args := [][]abi.Type{nil, {abi.I32}, {long}, {abi.I8}, {abi.U64}, {abi.U16}, {abi.Bool}, {abi.F32}, {abi.F64}, {abi.Pointer}, {abi.Pointer}, {abi.Pointer}, {abi.Pointer, abi.I32}, nil, {abi.I32}, {abi.I32, abi.I32}, {abi.I32}, {abi.F64}, {abi.I32}, {abi.I32}, {abi.I32}, nil, nil}
			for i, c := range out.Constructors {
				if c.Transport.Result != abi.Pointer || !slices.Equal(c.Transport.Args, args[i]) {
					t.Fatal(i, c, args[i])
				}
			}
		})
	}
}

func TestInferredConstructorInputs(t *testing.T) {
	for _, mode := range []string{"empty", "too_many", "class", "class_budget", "symbol", "symbol_budget", "duplicate", "argument", "void", "argument_budget", "too_many_arguments", "nil_context", "cancelled", "missing_compiler"} {
		t.Run(mode, func(t *testing.T) {
			input := []cxx.ConstructorSelection{{Class: "R", Symbol: "selected", ArgumentTypes: []string{"int"}}}
			ctx := context.Background()
			switch mode {
			case "empty":
				input = nil
			case "too_many":
				input = make([]cxx.ConstructorSelection, 257)
			case "class":
				input[0].Class = "R;injected"
			case "class_budget":
				input[0].Class = strings.Repeat("R", 257)
			case "symbol":
				input[0].Symbol = "bad;"
			case "symbol_budget":
				input[0].Symbol = strings.Repeat("s", 257)
			case "duplicate":
				input = append(input, input[0])
			case "argument":
				input[0].ArgumentTypes[0] = "int*"
			case "void":
				input[0].ArgumentTypes[0] = "void"
			case "argument_budget":
				input[0].ArgumentTypes[0] = strings.Repeat("T", 257)
			case "too_many_arguments":
				input[0].ArgumentTypes = make([]string, 33)
			case "nil_context":
				ctx = nil
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			compiler := filepath.Join(t.TempDir(), "missing")
			out, err := cxx.InferConstructors(ctx, "testdata/inferred_constructors.hpp", input, cxx.DiscoveryOptions{Compiler: compiler})
			if err == nil || out != nil {
				t.Fatal(out, err)
			}
			var lookup *exec.Error
			if errors.As(err, &lookup) != (mode == "missing_compiler") || lookup != nil && lookup.Name != compiler {
				t.Fatal("invalid selections/context must fail before compiler lookup", err)
			}
		})
	}
}

func TestNativeInferredConstructorRejections(t *testing.T) {
	for _, mode := range []string{"Ref", "Extended", "Nothing", "Pair", "Array", "Function", "Member", "Deleted", "Hidden", "Abstract", "Ambiguous", "missing", "arity"} {
		t.Run(mode, func(t *testing.T) {
			bad := cxx.ConstructorSelection{Class: "inferred_constructors::Generic", Symbol: "bad", ArgumentTypes: []string{"inferred_constructors::" + mode}}
			switch mode {
			case "Deleted", "Hidden", "Abstract", "Ambiguous", "missing":
				bad.Class = "inferred_constructors::" + mode
				bad.ArgumentTypes = []string{"int"}
			case "arity":
				bad.Class = "inferred_constructors::Receiver"
				bad.ArgumentTypes = []string{"int", "int", "int"}
			}
			input := []cxx.ConstructorSelection{{Class: "inferred_constructors::Receiver", Symbol: "good"}, bad}
			out, err := cxx.InferConstructors(context.Background(), "testdata/inferred_constructors.hpp", input, cxx.DiscoveryOptions{Compiler: cppTool(t)})
			if err == nil || out != nil {
				t.Fatal("unsupported allocation published partial output", out, err)
			}
		})
	}
}

func TestNativeInferredConstructorProducerGuards(t *testing.T) {
	for _, mode := range []string{"argument", "pointer_width"} {
		t.Run(mode, func(t *testing.T) {
			header := "testdata/inferred_constructors.hpp"
			out, err := cxx.InferConstructors(context.Background(), header, []cxx.ConstructorSelection{{Class: "inferred_constructors::Generic", Symbol: "guarded", ArgumentTypes: []string{"inferred_constructors::Changing"}}}, cxx.DiscoveryOptions{Compiler: cppTool(t), Target: "x86_64-unknown-linux-gnu", Flags: []string{"-ffreestanding"}})
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "guard.cpp")
			write(t, source, out.Facade.CXXSource)
			args := []string{"--target=x86_64-unknown-linux-gnu", "-ffreestanding", "-std=c++17", "-fsyntax-only", source}
			if data, err := exec.Command(cppTool(t), args...).CombinedOutput(); err != nil {
				t.Fatalf("matching producer: %v\n%s", err, data)
			}
			want := "inferred argument changed"
			if mode == "argument" {
				args = append(args, "-DINFERRED_CONSTRUCTOR_WIDE")
			} else {
				args[0] = "--target=i686-unknown-linux-gnu"
				want = "inferred pointer width changed"
			}
			if data, err := exec.Command(cppTool(t), args...).CombinedOutput(); err == nil || !strings.Contains(string(data), want) {
				t.Fatalf("producer guard: %v\n%s", err, data)
			}
		})
	}
}

func TestNativeInferredConstructorSnapshotsAndUnguardedHeader(t *testing.T) {
	header := filepath.Join(t.TempDir(), "once.hpp")
	write(t, header, []byte("struct Once {}; struct R {R(int){}};"))
	input := []cxx.ConstructorSelection{{Class: "R", Symbol: "selected", ArgumentTypes: []string{"int"}}}
	out, err := cxx.InferConstructors(context.Background(), header, input, cxx.DiscoveryOptions{Compiler: cppTool(t), Flags: []string{"-Werror"}})
	if err != nil {
		t.Fatal(err)
	}
	input[0].Class, input[0].ArgumentTypes[0] = "Other", "double"
	if out.Constructors[0].Selection.Class != "R" || out.Constructors[0].Selection.ArgumentTypes[0] != "int" {
		t.Fatal("selection aliases caller storage", out)
	}
}

func TestNativeInferredConstructorSourceBudget(t *testing.T) {
	input := make([]cxx.ConstructorSelection, 256)
	for i := range input {
		input[i] = cxx.ConstructorSelection{Class: "R", Symbol: "selected_" + strconv.Itoa(i), ArgumentTypes: make([]string, 32)}
		for j := range input[i].ArgumentTypes {
			input[i].ArgumentTypes[j] = strings.Repeat("T", 256)
		}
	}
	out, err := cxx.InferConstructors(context.Background(), "testdata/inferred_constructors.hpp", input, cxx.DiscoveryOptions{Compiler: cppTool(t)})
	if out != nil || err == nil || !strings.Contains(err.Error(), "source budget") {
		t.Fatal(out, err)
	}
}
