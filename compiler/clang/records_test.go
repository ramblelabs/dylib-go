package clang

import (
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/abi"
)

const recordHeader = `typedef struct { signed char tag; double value; short tail; } Pair;
struct Outer { Pair items[2]; int values[2][3]; struct Outer *next; };
Pair echo(Pair);
struct Outer nest(struct Outer);
Pair *mutate(Pair *);
int var_record(Pair,...);
`

func TestRecordsAcrossTargets(t *testing.T) {
	compiler := compilerTool(t)
	path := headerFile(t, recordHeader)
	for _, target := range targets {
		t.Run(target.triple, func(t *testing.T) {
			h, err := Parse(context.Background(), path, Options{Compiler: compiler, Target: target.triple, Functions: []string{"echo", "nest", "mutate", "var_record"}})
			if err != nil {
				t.Fatal(err)
			}
			pair, err := h.LookupRecord("Pair")
			if err != nil {
				t.Fatal(err)
			}
			want := abi.Layout{Size: 24, Alignment: 8, Offsets: []uint64{0, 8, 16}}
			if target.os == "linux" && target.arch == "386" {
				want = abi.Layout{Size: 16, Alignment: 4, Offsets: []uint64{0, 4, 12}}
			}
			if !reflect.DeepEqual(pair.Layout, want) {
				t.Fatalf("pair layout: %+v; want %+v", pair.Layout, want)
			}
			outer, err := h.LookupRecord("struct Outer")
			if err != nil {
				t.Fatal(err)
			}
			want = abi.Layout{Size: 80, Alignment: 8, Offsets: []uint64{0, 48, 72}}
			if target.os == "linux" && target.arch == "386" {
				want = abi.Layout{Size: 60, Alignment: 4, Offsets: []uint64{0, 32, 56}}
			}
			if !reflect.DeepEqual(outer.Layout, want) {
				t.Fatalf("outer layout: %+v; want %+v", outer.Layout, want)
			}
			if outer.Description.Fields[0].Type.Type != abi.Array || outer.Description.Fields[0].Type.Len != 2 || outer.Description.Fields[1].Type.Elem.Type != abi.Array || outer.Description.Fields[2].Type.Elem != nil {
				t.Fatal("array/recursive pointer descriptions:", outer.Description)
			}
			function, err := h.Lookup("mutate")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(*function.Signature.ArgTypes[0].Elem, pair.Description) || !reflect.DeepEqual(*function.Signature.ResultType.Elem, pair.Description) {
				t.Fatal("typed struct-pointer declaration", function)
			}
			variable, err := h.Lookup("var_record")
			if err != nil {
				t.Fatal(err)
			}
			variable, err = variable.WithTail(abi.I8, abi.F32)
			if err != nil || len(variable.Signature.ArgTypes) != 3 || variable.Signature.ArgTypes[1].Type != abi.I8 {
				t.Fatal("record variadic prefix:", variable, err)
			}
			pair.Description.Fields[0].Name = "changed"
			pair.Layout.Offsets[0] = 999
			original, _ := h.LookupRecord("Pair")
			if original.Description.Fields[0].Name != "tag" || original.Layout.Offsets[0] != 0 {
				t.Fatal("LookupRecord exposed mutable storage")
			}
			source, err := h.GoSource("bindings", "Declarations")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parser.ParseFile(token.NewFileSet(), "generated.go", source, parser.AllErrors); err != nil {
				t.Fatal(err)
			}
			_, err = h.ForHost("echo")
			matching := target.os == runtime.GOOS && target.arch == runtime.GOARCH
			if matching && abi.StorageAvailable() && err != nil {
				t.Fatal("native layout validation:", err)
			}
			if matching && !abi.StorageAvailable() && !errors.Is(err, abi.ErrUnavailable) {
				t.Fatal("missing native layout backend:", err)
			}
			if !matching && (err == nil || errors.Is(err, abi.ErrUnavailable)) {
				t.Fatal("foreign target guard:", err)
			}
		})
	}
}

func TestUnsupportedRecords(t *testing.T) {
	compiler := compilerTool(t)
	for _, source := range []string{
		"struct S {int :1;}; struct S f(struct S);",
		"struct S {int values[];}; struct S f(struct S);",
		"struct S {int values[0];}; struct S f(struct S);",
		"struct S {}; struct S f(struct S);",
		"struct S {union {int a;float b;};}; struct S f(struct S);",
		"struct S {long double value;}; struct S f(struct S);",
		"struct S {int (*callback)();}; struct S f(struct S);",
		"struct S {int (*callbacks[])(int);}; struct S f(struct S);",
		"struct S {int (*callbacks[0])(int);}; struct S f(struct S);",
		"struct S {int (*callbacks[2])();}; struct S f(struct S);",
	} {
		if _, err := Parse(context.Background(), headerFile(t, source), Options{Compiler: compiler, Functions: []string{"f"}}); err == nil {
			t.Fatal("unsupported record accepted:", source)
		}
	}
}

func TestRecordMetadataValidationAndBudgets(t *testing.T) {
	h, err := Parse(context.Background(), headerFile(t, recordHeader), Options{Compiler: compilerTool(t), Functions: []string{"echo"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.LookupRecord("missing"); err == nil {
		t.Fatal("missing record found")
	}
	if err := h.decodeRecordLayouts([]byte(`{"kind":"TranslationUnitDecl"}`)); err == nil {
		t.Fatal("missing layout probes accepted")
	}
	if err := h.decodeRecordLayouts([]byte("invalid")); err == nil {
		t.Fatal("invalid layout AST accepted")
	}
	if abi.StorageAvailable() {
		original := h.Records[0].Layout
		h.Records[0].Layout.Size += h.Records[0].Layout.Alignment
		if _, err := h.ForHost("echo"); err == nil || !strings.Contains(err.Error(), "layout mismatch") {
			t.Fatal("changed compiler layout accepted:", err)
		}
		h.Records[0].Layout = original
		h.Records = nil
		if _, err := h.ForHost("echo"); err == nil || !strings.Contains(err.Error(), "missing compiler") {
			t.Fatal("absent compiler layout accepted:", err)
		}
	}
	// Small C declarations can expand through repeated record aliases. Bound the
	// full public description graph before rendering or returning cloned values.
	d := abi.TypeDesc{Type: abi.I32}
	for level := 0; level < 3; level++ {
		next := abi.TypeDesc{Type: abi.Struct}
		for i := 0; i < 32; i++ {
			next.Fields = append(next.Fields, abi.Field{Name: fmt.Sprintf("f%d", i), Type: d})
		}
		d = next
	}
	h = &Header{Target: Target{Triple: nativeTriple(), OS: runtime.GOOS, Arch: runtime.GOARCH, PointerSize: strconv.IntSize / 8}, Functions: []Declaration{{Name: "f", Symbol: "f", Signature: abi.Signature{Result: abi.Struct, ResultType: &d}}}, Records: []Record{{Name: "Big", Description: d}}}
	if _, err := h.Lookup("f"); err == nil || !strings.Contains(err.Error(), "nodes") {
		t.Fatal("description expansion not bounded:", err)
	}
	if _, err := h.GoSource("bindings", "Declarations"); err == nil || !strings.Contains(err.Error(), "nodes") {
		t.Fatal("generated expansion not bounded:", err)
	}
}
