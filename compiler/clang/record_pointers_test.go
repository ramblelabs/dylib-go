package clang

import (
	"context"
	"go/parser"
	"go/token"
	"runtime"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/abi"
)

const recordPointerHeader = `typedef int (*Adder)(int,int);
typedef Adder (*Maker)(void);
typedef struct Pair { int a,b; } Pair;
typedef struct Callbacks {
  int bias;
  Adder add;
  int (*direct)(int,int);
  Maker make;
  Adder *slot;
  int (*variable)(int,...);
  Pair (*pair)(Pair);
  int (*self)(struct Callbacks);
} Callbacks;
typedef struct { Callbacks items[2]; } Nested;
Callbacks echo(Callbacks);
Nested nested(Nested);
Callbacks *pointer(Callbacks *);
struct Unused { int (*old_style)(); int flag:1; };
typedef struct {
 Adder entries[2][3];
 Maker makers[2];
 Adder *slots[2];
} ArrayCallbacks;
ArrayCallbacks arrays(ArrayCallbacks);
`

func TestCallbackRecordFieldsAcrossTargets(t *testing.T) {
	path := headerFile(t, recordPointerHeader)
	for _, target := range targets {
		t.Run(target.triple, func(t *testing.T) {
			h, err := Parse(context.Background(), path, Options{Compiler: compilerTool(t), Target: target.triple, Functions: []string{"echo", "nested", "pointer", "arrays"}})
			if err != nil {
				t.Fatal(err)
			}
			r, err := h.LookupRecord("Callbacks")
			if err != nil || len(r.FunctionPointers) != 7 {
				t.Fatal(r, err)
			}
			for i, p := range r.FunctionPointers {
				if p.Position != i+1 || r.Description.Fields[p.Position].Type.Type != abi.Pointer {
					t.Fatal("incorrect pointer field", p)
				}
			}
			make, _ := r.LookupFunctionPointer("make")
			adder, err := make.LookupFunctionPointer(-1)
			if err != nil || len(adder.Signature.Args) != 2 {
				t.Fatal(adder, err)
			}
			slot, _ := r.LookupFunctionPointer("slot")
			if slot.Indirection != 2 {
				t.Fatal(slot)
			}
			variable, _ := r.LookupFunctionPointer("variable")
			if _, err := variable.WithTail(abi.I8, abi.F32); err != nil {
				t.Fatal(err)
			}
			pair, _ := r.LookupFunctionPointer("pair")
			if pair.Signature.Result != abi.Struct {
				t.Fatal(pair)
			}
			if _, err := h.LookupRecord("Pair"); err != nil {
				t.Fatal("record used only by field prototype was omitted", err)
			}
			self, _ := r.LookupFunctionPointer("self")
			if len(self.Signature.ArgTypes[0].Fields) != 8 {
				t.Fatal("self-referencing prototype", self)
			}
			array, err := h.LookupRecord("ArrayCallbacks")
			if err != nil || len(array.FunctionPointers) != 3 {
				t.Fatal(array, err)
			}
			shape := array.Description.Fields[0].Type
			if shape.Type != abi.Array || shape.Len != 2 || shape.Elem.Type != abi.Array || shape.Elem.Len != 3 || shape.Elem.Elem.Type != abi.Pointer {
				t.Fatal("array shape", shape)
			}
			entry, err := array.LookupFunctionPointer("entries")
			if err != nil || entry.Signature.Result != abi.I32 || len(entry.Signature.Args) != 2 {
				t.Fatal(entry, err)
			}
			maker, _ := array.LookupFunctionPointer("makers")
			if _, err := maker.LookupFunctionPointer(-1); err != nil {
				t.Fatal(err)
			}
			slots, _ := array.LookupFunctionPointer("slots")
			if slots.Indirection != 2 {
				t.Fatal(slots)
			}
			make.FunctionPointers[0].Signature.Args[0] = abi.F64
			r.FunctionPointers[3].Signature.Result = abi.Void
			original, _ := h.LookupRecord("Callbacks")
			make, _ = original.LookupFunctionPointer("make")
			if make.FunctionPointers[0].Signature.Args[0] != abi.I32 || original.FunctionPointers[3].Signature.Result != abi.I32 {
				t.Fatal("field prototype snapshot shared storage")
			}
			generators := []func(string, string) ([]byte, error){h.GoSource, h.CgoSource}
			if target.arch != "386" {
				generators = append(generators, h.LLGoSource)
			}
			for _, generate := range generators {
				source, err := generate("bindings", "Bindings")
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(source), "FunctionPointers:") {
					t.Fatal("generated field metadata missing")
				}
				if _, err := parser.ParseFile(token.NewFileSet(), "generated.go", source, parser.AllErrors); err != nil {
					t.Fatal(err)
				}
			}
			if target.os == runtime.GOOS && target.arch == runtime.GOARCH {
				if abi.StorageAvailable() {
					if _, err := h.ForHost("echo"); err != nil {
						t.Fatal(err)
					}
					if _, err := h.ForHostRecord("Callbacks"); err != nil {
						t.Fatal(err)
					}
					for i := range h.Records {
						if h.Records[i].Name == "Pair" {
							h.Records[i].Layout.Size *= 2
						}
					}
					if _, err := h.ForHost("echo"); err == nil {
						t.Fatal("callback-only record layout mismatch accepted")
					}
					if _, err := h.ForHostRecord("Callbacks"); err == nil {
						t.Fatal("record callback layout mismatch accepted")
					}
				}
				// An unrelated scalar call does not need record/callback layouts.
				h.Functions = append(h.Functions, Declaration{Name: "scalar", Symbol: "scalar", Signature: abi.Signature{Result: abi.I32}})
				if _, err := h.ForHost("scalar"); err != nil {
					t.Fatal("unrelated scalar call", err)
				}
			}
		})
	}
}

func TestCallbackRecordFieldConventions(t *testing.T) {
	h, err := Parse(context.Background(), headerFile(t, `struct R {
 int (__attribute__((stdcall)) *std)(int,int);
 int (__attribute__((fastcall)) *fast)(int,int);
}; int f(struct R);`), Options{Compiler: compilerTool(t), Target: "i686-pc-windows-msvc", Functions: []string{"f"}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := h.LookupRecord("struct R")
	if err != nil {
		t.Fatal(err)
	}
	std, err := r.LookupFunctionPointer("std")
	if err != nil || std.Signature.Convention != abi.StdCall {
		t.Fatal(std, err)
	}
	fast, err := r.LookupFunctionPointer("fast")
	if err != nil || fast.Signature.Convention != abi.FastCall {
		t.Fatal(fast, err)
	}
}

func TestCallbackRecordFieldValidation(t *testing.T) {
	callback := FunctionPointer{Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}}
	r := Record{Name: "R", Description: abi.TypeDesc{Type: abi.Struct, Fields: []abi.Field{{Name: "callback", Type: abi.TypeDesc{Type: abi.Pointer}}}}}
	for _, position := range []int{-1, 1} {
		callback.Position = position
		r.FunctionPointers = []FunctionPointer{callback}
		if _, err := r.LookupFunctionPointer("callback"); err == nil {
			t.Fatal("invalid field position accepted")
		}
	}
	callback.Position = 0
	r.FunctionPointers = []FunctionPointer{callback, callback}
	if _, err := r.LookupFunctionPointer("callback"); err == nil {
		t.Fatal("duplicate field accepted")
	}
	r.FunctionPointers = []FunctionPointer{callback}
	r.FunctionPointers[0].FunctionPointers = r.FunctionPointers
	if _, err := r.LookupFunctionPointer("callback"); err == nil {
		t.Fatal("cyclic metadata accepted")
	}
	r.FunctionPointers = []FunctionPointer{callback}
	if _, err := r.LookupFunctionPointer("missing"); err == nil {
		t.Fatal("unknown field accepted")
	}
	r.Description.Fields[0].Type.Type = abi.I32
	if _, err := r.LookupFunctionPointer("callback"); err == nil {
		t.Fatal("nonpointer field accepted")
	}
	if _, err := (&Header{}).ForHostRecord("R"); err == nil {
		t.Fatal("invalid host accepted")
	}
	// Array metadata still requires a pointer leaf, not a scalar or record.
	element := abi.TypeDesc{Type: abi.I32}
	r.Description.Fields[0].Type = abi.TypeDesc{Type: abi.Array, Len: 2, Elem: &element}
	if _, err := r.LookupFunctionPointer("callback"); err == nil {
		t.Fatal("non-pointer array accepted")
	}
	element.Type = abi.Pointer
	if _, err := r.LookupFunctionPointer("callback"); err != nil {
		t.Fatal("pointer array rejected", err)
	}
}
