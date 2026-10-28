// Package cbridge generates C facades that keep attributed record values inside
// the producer's compiler. Go and llgo exchange ordinary, checked C storage.
package cbridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/clang"
)

const maxSource = 1 << 20
const layoutMagic uint64 = 0x4342524944474501

// Function selects an original declaration and a distinct C facade export.
// Expand a variadic declaration in Header with WithTail before generation.
type Function struct{ Name, Symbol string }

// Facade contains independent source/header buffers and logical binding metadata.
// Compile CSource with the producer's compiler, target, includes and flags. The
// generated header is independent of the original header and uses three pointers
// and an int32 status. No attributed record crosses that boundary by value.
type Facade struct {
	CHeader, CSource []byte
	Functions        []Binding
}

// Binding describes one synchronous C call. Input/Output return ordinary C
// storage descriptors; Signature retains the original logical declaration.
// Native addresses are borrowed. Native code must not retain facade temporaries.
type Binding struct {
	Name, Symbol  string
	NativeSymbol  string
	target        clang.Target
	signature     abi.Signature
	input, output abi.TypeDesc
	layouts       []*transport
	words         int
}

func (b Binding) Input() abi.TypeDesc      { return b.input.Clone() }
func (b Binding) Output() abi.TypeDesc     { return b.output.Clone() }
func (b Binding) Signature() abi.Signature { return b.signature.Clone() }

type transport struct {
	name     string
	desc     abi.TypeDesc
	children []*transport
}

type generator struct {
	header *clang.Header
	prefix string
	types  map[string]*transport
	order  []*transport
	defs   bytes.Buffer
	api    string
}

// Generate uses a validated Clang declaration model as a logical schema. Header
// and preprocessing inputs must remain the same as during Parse. The original
// function's __typeof__ supplies its native calling convention in CSource.
// This supports C17 scalar/native-address/record values, nested arrays, bitfields
// and concrete variadic tails. Function-pointer parameters/results require a
// separate adapter. Native arguments use C17 initialization, including const
// subobjects. This is not a C++ exception or object-lifetime adapter.
// Generation runs no tools and works without cgo. Limits are 256 exports,
// 64 arguments per export, 64 KiB layout queries and 1 MiB generated source.
func Generate(header string, declarations *clang.Header, functions []Function) (*Facade, error) {
	if header == "" || len(header) > 4096 || strings.ContainsAny(header, "\"\\\r\n\x00") {
		return nil, fmt.Errorf("cbridge: expected a quoted-include path with forward slashes")
	}
	if declarations == nil || len(functions) == 0 || len(functions) > 256 {
		return nil, fmt.Errorf("cbridge: select 1 to 256 declarations")
	}
	identity, err := json.Marshal([]any{header, declarations, functions})
	if err != nil {
		return nil, fmt.Errorf("cbridge: invalid declaration metadata: %w", err)
	}
	digest := sha256.Sum256(identity)
	g := &generator{header: declarations, prefix: fmt.Sprintf("dylib_cbridge_%x_", digest[:8]), types: map[string]*transport{}}
	g.api = strings.ToUpper(g.prefix) + "API"
	seen := map[string]bool{}
	native := map[string]bool{}
	for _, function := range declarations.Functions {
		native[function.Name] = true
		native[function.Symbol] = true
	}
	var public, bodies bytes.Buffer
	result := &Facade{}
	for _, request := range functions {
		if !identifier(request.Name) || !identifier(request.Symbol) || seen[request.Symbol] || native[request.Symbol] {
			return nil, fmt.Errorf("cbridge: invalid, duplicate or native-conflicting export %q", request.Symbol)
		}
		seen[request.Symbol] = true
		declaration, err := declarations.Lookup(request.Name)
		if err != nil {
			return nil, err
		}
		if len(declaration.Signature.Args) > 64 || len(declaration.FunctionPointers) != 0 {
			return nil, fmt.Errorf("cbridge: %s requires at most 64 arguments and no function-pointer parameters/results", request.Name)
		}
		binding, err := g.function(&bodies, declaration, request.Symbol)
		if err != nil {
			return nil, fmt.Errorf("cbridge: %s: %w", request.Name, err)
		}
		fmt.Fprintf(&public, "%s int32_t %s(void *, void *, const void *);\n", g.api, request.Symbol)
		result.Functions = append(result.Functions, binding)
		if g.defs.Len()+public.Len()+bodies.Len() > maxSource {
			return nil, fmt.Errorf("cbridge: source exceeds 1 MiB")
		}
	}
	digest = sha256.Sum256(append(public.Bytes(), g.defs.Bytes()...))
	guard := fmt.Sprintf("DYLIB_CBRIDGE_%X", digest[:8])
	api := fmt.Sprintf("#if defined(_WIN32)\n#define %s __declspec(dllexport) __cdecl\n#else\n#define %s __attribute__((visibility(\"default\")))\n#endif\n", g.api, g.api)
	result.CHeader = []byte(fmt.Sprintf("#ifndef %s\n#define %s\n#include <stdint.h>\n#include <stddef.h>\n%s#pragma pack(push, 16)\n%s#pragma pack(pop)\n#ifdef __cplusplus\nextern \"C\" {\n#endif\n%s#ifdef __cplusplus\n}\n#endif\n#endif\n", guard, guard, api, g.defs.String(), public.String()))
	result.CSource = []byte(fmt.Sprintf("#include <stdint.h>\n#include <stdbool.h>\n#include <stddef.h>\n#pragma pack(push)\n#include \"%s\"\n#pragma pack(pop)\n%s\n%s", header, result.CHeader, bodies.String()))
	if len(result.CSource) > maxSource {
		return nil, fmt.Errorf("cbridge: source exceeds 1 MiB")
	}
	return result, nil
}

func identifier(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for i, r := range s {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func (g *generator) record(d abi.TypeDesc) (clang.Record, error) {
	var found *clang.Record
	for i := range g.header.Records {
		r := &g.header.Records[i]
		if !reflect.DeepEqual(r.Description, d) {
			continue
		}
		if found != nil && found.Name != r.Name {
			return clang.Record{}, fmt.Errorf("ambiguous record; parse with NominalRecords")
		}
		found = r
	}
	if found == nil {
		return clang.Record{}, fmt.Errorf("missing native record spelling")
	}
	name := strings.TrimPrefix(found.Name, "struct ")
	if !identifier(name) {
		return clang.Record{}, fmt.Errorf("inaccessible native record spelling %q", found.Name)
	}
	return *found, nil
}

func carrier(d abi.TypeDesc) abi.TypeDesc {
	d = d.Clone()
	if d.Type == abi.Pointer {
		d.Elem = nil
	}
	for i := range d.Fields {
		d.Fields[i].Type = carrier(d.Fields[i].Type)
	}
	if d.Type == abi.Array {
		e := carrier(*d.Elem)
		d.Elem = &e
	}
	return d
}

func scalar(t abi.Type, logical bool) (string, error) {
	switch t {
	case abi.I8:
		return "int8_t", nil
	case abi.U8:
		return "uint8_t", nil
	case abi.I16:
		return "int16_t", nil
	case abi.U16:
		return "uint16_t", nil
	case abi.I32:
		return "int32_t", nil
	case abi.U32:
		return "uint32_t", nil
	case abi.I64:
		return "int64_t", nil
	case abi.U64:
		return "uint64_t", nil
	case abi.F32:
		return "float", nil
	case abi.F64:
		return "double", nil
	case abi.Pointer:
		return "void *", nil
	case abi.Bool:
		if logical {
			return "uint8_t", nil
		}
		return "bool", nil
	}
	return "", fmt.Errorf("unsupported scalar type %d", t)
}

func (g *generator) transport(d abi.TypeDesc) (*transport, error) {
	d = carrier(d)
	key, _ := json.Marshal(d)
	if t := g.types[string(key)]; t != nil {
		return t, nil
	}
	t := &transport{desc: d}
	if d.Type != abi.Struct && d.Type != abi.Array {
		var err error
		t.name, err = scalar(d.Type, true)
		if err != nil {
			return nil, err
		}
	} else {
		members := len(d.Fields)
		if d.Type == abi.Array {
			members = 1
		}
		for i := 0; i < members; i++ {
			var child abi.TypeDesc
			if d.Type == abi.Struct {
				child = d.Fields[i].Type
			} else {
				child = *d.Elem
			}
			part, err := g.transport(child)
			if err != nil {
				return nil, err
			}
			t.children = append(t.children, part)
		}
		t.name = fmt.Sprintf("%st%d", g.prefix, len(g.order))
		if d.Type == abi.Array {
			fmt.Fprintf(&g.defs, "typedef %s %s[%d];\n", t.children[0].name, t.name, d.Len)
		} else {
			fmt.Fprintf(&g.defs, "typedef struct %s {\n", t.name)
			for i, child := range t.children {
				fmt.Fprintf(&g.defs, "%s v%d;\n", child.name, i)
			}
			fmt.Fprintf(&g.defs, "} %s;\n", t.name)
		}
	}
	g.types[string(key)] = t
	g.order = append(g.order, t)
	return t, nil
}
