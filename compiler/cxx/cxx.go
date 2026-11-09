// Package cxx generates explicit C facades for selected C++ classes and functions.
// A matching C++ compiler supplies class layout, calls, virtual dispatch and runtime
// support. The generated C declarations can use compiler/clang's Go/llgo bindings.
package cxx

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/ramblelabs/dylib-go/abi"
)

const maxFacadeFunctions = 256

// Status values returned by every generated C entry.
const (
	Success         int32 = 0
	InvalidArgument int32 = 1
	Exception       int32 = 2
)

// Constructor selects C argument transports and optional native object/function types
// used for C++ overload resolution. The appended void** slot must be null.
type Constructor struct {
	Symbol       string
	Args         []abi.Type
	ObjectArgs   []ObjectArg
	FunctionArgs []FunctionArg
}

// RefQualifier selects the implicit object's C++ value category.
type RefQualifier uint8

const (
	RefNone   RefQualifier = iota // No ref-qualifier; invoke on the existing lvalue.
	RefLValue                     // An exact &-qualified member.
	RefRValue                     // An exact &&-qualified member; does not consume the handle.
)

// Method selects an exact C++ member signature. Name accepts an identifier or
// "operator()" for named callable types; the latter cannot be Static in C++17. Const
// and Volatile select receiver cv-qualifiers; Ref selects an exact ref-qualifier.
// RefRValue invokes the existing object as an xvalue without deleting or clearing
// its handle. The native method defines its post-call state; the caller retains
// ownership and must eventually destroy it. Static omits the receiver and cannot
// have cv/ref qualifiers. A non-void result adds an output pointer after the
// arguments. ObjectArgs/ObjectResult replace
// pointer transports with named native types. Value results require an initially
// null output slot and a noexcept destructor, and allocate a new owned object.
// Pointer/reference results borrow the native API's lifetime. FunctionArgs and
// FunctionResult select exact native function pointers using opaque address
// transports. A FunctionResult is borrowed and cannot also be an ObjectResult;
// keep its provider alive while using it. Signature.Variadic selects a native
// ellipsis: FixedArgs counts the native fixed prefix (excluding the receiver),
// and Args also contains one concrete scalar/pointer tail. The C facade stays
// fixed, and the native compiler applies default argument promotions. ObjectArgs
// in the tail must be ObjectPointer. Ordinary aggregate transports are unsupported.
// Conversion selects a non-void conversion operator to the exact native result
// type. Name must be empty, Signature.Args empty, and Static false. Explicit
// conversion operators are supported; ABI, ownership and receiver qualifiers
// use the same contracts as ordinary methods.
type Method struct {
	Name, Symbol string
	// TemplateArgs is nil for ordinary selection; a non-nil empty slice emits
	// <> to select a template even when a non-template overload is visible.
	// Arguments use Function.TemplateArgs' bounded identifier/literal grammar.
	TemplateArgs   []string
	Signature      abi.Signature
	Const, Static  bool
	Volatile       bool
	Ref            RefQualifier
	Conversion     bool
	ObjectArgs     []ObjectArg
	ObjectResult   *ObjectType
	FunctionArgs   []FunctionArg
	FunctionResult *abi.Signature
}

// Base selects a borrowed view of a public, unambiguous C++ base subobject.
// The native compiler performs pointer adjustment, including virtual bases.
// The export takes a receiver and a void** output slot; it does not create or
// retain an object. Its view expires with the original object.
type Base struct {
	Name, Symbol string
}

// Class names an accessible C++ type, including namespace-qualified aliases.
// Optional Delete names the export that clears a void** handle slot before
// invoking delete. Borrowed base views must not be deleted unless the native
// contract permits it (including a virtual destructor when required). Omit Delete
// for borrowed/static-only selections; owned results still need native cleanup.
type Class struct {
	Name         string
	Delete       string
	Constructors []Constructor
	Methods      []Method
	Bases        []Base
}

// Facade contains independent source buffers and the ordered C export names.
// Write CHeader for compiler/clang.Parse with Functions, and compile CXXSource
// against the original header with a matching C++17 compiler/runtime. The source
// embeds its C declarations, so its output filename is independent of CHeader's.
type Facade struct {
	CHeader, CXXSource []byte
	Functions          []string
}

const apiMacro = `#if defined(_WIN32)
#define DYLIB_GO_CXX_API __declspec(dllexport) int32_t __cdecl
#else
#define DYLIB_GO_CXX_API __attribute__((visibility("default"))) int32_t
#endif
#ifdef __cplusplus
#define DYLIB_GO_CXX_NOEXCEPT noexcept
#else
#define DYLIB_GO_CXX_NOEXCEPT
#endif
`

// Generate renders a C facade without invoking a compiler or reading files.
// Header is a quoted-include path using forward slashes; include directories
// belong to the producer compiler. Compile errors reject missing/private members,
// signature mismatches and incompatible class definitions. This is an explicit
// manifest, not a C++ header parser. Constructors use C++ overload resolution;
// methods use an exact typed member/function pointer.
//
// Exported functions catch C++ exceptions and return Exception. This cannot
// recover from noexcept violations, termination, signals or invalid addresses.
// Objects, output storage, provider images and asynchronous native activity stay
// caller-owned. Use complete OS libraries for C++ runtime/exception registration.
func Generate(header string, classes []Class) (*Facade, error) {
	return generate(header, classes, false, "")
}

func generate(header string, classes []Class, allowGlobal bool, preamble string) (*Facade, error) {
	if header == "" || strings.ContainsAny(header, "\"\\\r\n\x00") {
		return nil, fmt.Errorf("cxx: expected a quoted-include path using forward slashes")
	}
	if len(classes) == 0 {
		return nil, fmt.Errorf("cxx: select at least one class")
	}
	var declarations, definitions bytes.Buffer
	result := &Facade{}
	seen := make(map[string]bool)
	emit := func(symbol string, parameters []string, body string) error {
		if !identifier(symbol) || seen[symbol] {
			return fmt.Errorf("cxx: invalid or duplicate C export %q", symbol)
		}
		if len(result.Functions) == maxFacadeFunctions {
			return fmt.Errorf("cxx: at most 256 C exports are supported")
		}
		seen[symbol] = true
		result.Functions = append(result.Functions, symbol)
		params := strings.Join(parameters, ", ")
		if params == "" {
			params = "void"
		}
		fmt.Fprintf(&declarations, "DYLIB_GO_CXX_API %s(%s) DYLIB_GO_CXX_NOEXCEPT;\n", symbol, params)
		fmt.Fprintf(&definitions, "extern \"C\" DYLIB_GO_CXX_API %s(%s) noexcept {\n%s}\n", symbol, params, body)
		return nil
	}
	for _, class := range classes {
		if !qualifiedName(class.Name) && !(allowGlobal && class.Name == "") {
			return nil, fmt.Errorf("cxx: invalid C++ type name %q", class.Name)
		}
		for _, constructor := range class.Constructors {
			parameters, _, args, checks, err := nativeArguments(constructor.Args, constructor.ObjectArgs, constructor.FunctionArgs)
			if err != nil {
				return nil, fmt.Errorf("cxx: constructor %s: %w", constructor.Symbol, err)
			}
			parameters = append(parameters, "void **output")
			body := fmt.Sprintf(`if (!output || *output) return DYLIB_CXX_INVALID_ARGUMENT;
%s
try {
auto object = new %s(%s);
if (!object) return DYLIB_CXX_EXCEPTION;
*output = const_cast<void *>(static_cast<const volatile void *>(object));
return DYLIB_CXX_SUCCESS;
} catch (...) { return DYLIB_CXX_EXCEPTION; }
`, checks, class.Name, args)
			if err := emit(constructor.Symbol, parameters, body); err != nil {
				return nil, err
			}
		}
		for _, method := range class.Methods {
			callOperator := method.Name == "operator()"
			template, err := templateArguments(method.TemplateArgs)
			if err != nil || method.Conversion && method.TemplateArgs != nil {
				if err == nil {
					err = fmt.Errorf("conversion template arguments require a named native wrapper")
				}
				return nil, fmt.Errorf("cxx: method %s: %w", method.Name, err)
			}
			if method.Conversion && (method.Name != "" || method.Static || len(method.Signature.Args) != 0 || method.Signature.Result == abi.Void) {
				return nil, fmt.Errorf("cxx: conversion %q requires an unnamed non-static member, no arguments and a non-void result", method.Symbol)
			}
			if !method.Conversion && (!identifier(method.Name) && !callOperator) || method.Ref > RefRValue || method.Static && (method.Const || method.Volatile || method.Ref != RefNone || callOperator) {
				return nil, fmt.Errorf("cxx: invalid method %q or static/cv/ref combination", method.Name)
			}
			if err := methodSignature(method.Signature); err != nil {
				return nil, fmt.Errorf("cxx: method %s: %w", method.Name, err)
			}
			parameters, types, args, checks, err := nativeArguments(method.Signature.Args, method.ObjectArgs, method.FunctionArgs)
			if err != nil {
				return nil, fmt.Errorf("cxx: method %s: %w", method.Name, err)
			}
			if method.Signature.Variadic {
				for _, object := range method.ObjectArgs {
					if object.Index >= method.Signature.FixedArgs && object.Type.Kind != ObjectPointer {
						return nil, fmt.Errorf("cxx: method %s: variadic object arguments require pointer transport", method.Name)
					}
				}
				types = append(types[:method.Signature.FixedArgs], "...")
			}
			prototype := strings.Join(types, ", ")
			var body bytes.Buffer
			if !method.Static {
				parameters = append([]string{"void *handle"}, parameters...)
			}
			returns := method.Signature.Result != abi.Void
			kind, _ := cType(method.Signature.Result)
			nativeResult := kind
			if signature := method.FunctionResult; signature != nil {
				if method.Signature.Result != abi.Pointer || method.ObjectResult != nil {
					return nil, fmt.Errorf("cxx: method %s: function result requires an exclusive pointer transport", method.Name)
				}
				alias, err := functionAlias(*signature, "dylib_function_result")
				if err != nil {
					return nil, fmt.Errorf("cxx: method %s: %w", method.Name, err)
				}
				checks += alias
				nativeResult = "dylib_function_result"
			}
			owned := method.ObjectResult != nil && method.ObjectResult.Kind == ObjectValue
			if object := method.ObjectResult; object != nil {
				if method.Signature.Result != abi.Pointer {
					return nil, fmt.Errorf("cxx: method %s: native object result requires a pointer transport", method.Name)
				}
				nativeResult, err = object.native()
				if err != nil {
					return nil, fmt.Errorf("cxx: method %s: %w", method.Name, err)
				}
				if owned {
					checks += valueDestructor(object.Name, "dylib_result_type")
				}
			}
			if returns {
				parameters = append(parameters, kind+" *output")
				if owned {
					fmt.Fprint(&body, "if (!output || *output) return DYLIB_CXX_INVALID_ARGUMENT;\n")
				} else {
					fmt.Fprint(&body, "if (!output) return DYLIB_CXX_INVALID_ARGUMENT;\n*output = {};\n")
				}
			}
			if !method.Static {
				fmt.Fprint(&body, "if (!handle) return DYLIB_CXX_INVALID_ARGUMENT;\n")
			}
			fmt.Fprint(&body, checks, "try {\n")
			call := "entry(" + args + ")"
			if method.Static {
				fmt.Fprintf(&body, "using entry_type = %s (*)(%s);\n", nativeResult, prototype)
			} else {
				qualifier := ""
				if method.Const {
					qualifier = " const"
				}
				if method.Volatile {
					qualifier += " volatile"
				}
				if method.Ref == RefLValue {
					qualifier += " &"
				} else if method.Ref == RefRValue {
					qualifier += " &&"
				}
				fmt.Fprintf(&body, "using entry_type = %s (%s::*)(%s)%s;\n", nativeResult, class.Name, prototype, qualifier)
				receiver := cvQualified(class.Name, method.Const, method.Volatile)
				call = "(static_cast<" + receiver + " *>(handle)->*entry)(" + args + ")"
				if method.Ref == RefRValue {
					call = "(static_cast<" + receiver + " &&>(*static_cast<" + receiver + " *>(handle)).*entry)(" + args + ")"
				}
			}
			selector := method.Name
			selector += template
			if method.Conversion {
				selector = "operator " + nativeResult
			}
			fmt.Fprintf(&body, "auto entry = static_cast<entry_type>(&%s::%s);\n", class.Name, selector)
			switch object := method.ObjectResult; {
			case method.FunctionResult != nil:
				fmt.Fprintf(&body, "*output = reinterpret_cast<void *>(%s);\n", call)
			case owned:
				fmt.Fprintf(&body, "auto object = new %s(%s);\nif (!object) return DYLIB_CXX_EXCEPTION;\n*output = const_cast<void *>(static_cast<const volatile void *>(object));\n", object.Name, call)
			case object != nil && (object.Kind == ObjectReference || object.Kind == ObjectRValueReference):
				// A byte reference bypasses overloaded operator& without a C++
				// standard-library header or compiler-specific intrinsic.
				fmt.Fprintf(&body, "auto &&view = %s;\n*output = const_cast<char *>(&reinterpret_cast<const volatile char &>(view));\n", call)
			case object != nil:
				fmt.Fprintf(&body, "*output = const_cast<void *>(static_cast<const volatile void *>(%s));\n", call)
			default:
				if returns {
					fmt.Fprint(&body, "*output = ")
				}
				fmt.Fprintf(&body, "%s;\n", call)
			}
			fmt.Fprint(&body, "return DYLIB_CXX_SUCCESS;\n} catch (...) { return DYLIB_CXX_EXCEPTION; }\n")
			if err := emit(method.Symbol, parameters, body.String()); err != nil {
				return nil, err
			}
		}
		for _, base := range class.Bases {
			if !qualifiedName(base.Name) {
				return nil, fmt.Errorf("cxx: invalid C++ base type name %q", base.Name)
			}
			// Implicit typed conversion accepts only public, unambiguous bases
			// (or an identical alias). An explicit static_cast to the target
			// would also accept unsafe base-to-derived downcasts.
			body := fmt.Sprintf(`if (!output) return DYLIB_CXX_INVALID_ARGUMENT;
*output = nullptr;
if (!handle) return DYLIB_CXX_INVALID_ARGUMENT;
%s *view = static_cast<%s *>(handle);
*output = view;
return DYLIB_CXX_SUCCESS;
`, base.Name, class.Name)
			if err := emit(base.Symbol, []string{"void *handle", "void **output"}, body); err != nil {
				return nil, err
			}
		}
		if class.Delete == "" {
			continue
		}
		body := fmt.Sprintf(`if (!handle) return DYLIB_CXX_INVALID_ARGUMENT;
auto object = static_cast<%s *>(*handle);
*handle = nullptr;
if (!object) return DYLIB_CXX_SUCCESS;
try {
delete object;
return DYLIB_CXX_SUCCESS;
} catch (...) { return DYLIB_CXX_EXCEPTION; }
`, class.Name)
		if err := emit(class.Delete, []string{"void **handle"}, body); err != nil {
			return nil, err
		}
	}
	if len(result.Functions) == 0 {
		return nil, fmt.Errorf("cxx: select at least one C export")
	}
	guard := fmt.Sprintf("DYLIB_GO_CXX_FACADE_%X", sha256.Sum256(declarations.Bytes()))
	var c bytes.Buffer
	fmt.Fprintf(&c, "// Code generated by compiler/cxx. DO NOT EDIT.\n#ifndef %s\n#define %s\n#include <stdint.h>\n#include <stdbool.h>\n", guard, guard)
	fmt.Fprintf(&c, "#ifndef DYLIB_GO_CXX_STATUS_DEFINED\n#define DYLIB_GO_CXX_STATUS_DEFINED\nenum { DYLIB_CXX_SUCCESS = %d, DYLIB_CXX_INVALID_ARGUMENT = %d, DYLIB_CXX_EXCEPTION = %d };\n#endif\n", Success, InvalidArgument, Exception)
	fmt.Fprint(&c, apiMacro, "#ifdef __cplusplus\nextern \"C\" {\n#endif\n", declarations.String(), "#ifdef __cplusplus\n}\n#endif\n#undef DYLIB_GO_CXX_API\n#undef DYLIB_GO_CXX_NOEXCEPT\n#endif\n")
	result.CHeader = c.Bytes()
	var cpp bytes.Buffer
	fmt.Fprintf(&cpp, "%s\n#include \"%s\"\n%s%s%s#undef DYLIB_GO_CXX_API\n#undef DYLIB_GO_CXX_NOEXCEPT\n", result.CHeader, header, preamble, apiMacro, definitions.String())
	result.CXXSource = cpp.Bytes()
	return result, nil
}

func methodSignature(s abi.Signature) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if s.Convention != abi.Default && s.Convention != abi.CDecl {
		return fmt.Errorf("expected a default/cdecl signature")
	}
	for _, d := range append(append([]abi.TypeDesc(nil), s.ArgTypes...), s.ReturnType()) {
		if d.Elem != nil {
			return fmt.Errorf("pointers must be opaque native addresses")
		}
	}
	if _, _, _, err := arguments(s.Args); err != nil {
		return err
	}
	_, err := cType(s.Result)
	return err
}

func arguments(args []abi.Type) (parameters []string, types, values string, err error) {
	var names, kinds []string
	for i, arg := range args {
		kind, e := cType(arg)
		if e != nil || arg == abi.Void {
			return nil, "", "", fmt.Errorf("unsupported argument %d type %d", i, arg)
		}
		name := fmt.Sprintf("p%d", i)
		parameters, names, kinds = append(parameters, kind+" "+name), append(names, name), append(kinds, kind)
	}
	return parameters, strings.Join(kinds, ", "), strings.Join(names, ", "), nil
}

var cTypes = map[abi.Type]string{
	abi.Void: "void", abi.Bool: "bool", abi.Pointer: "void *",
	abi.I8: "int8_t", abi.U8: "uint8_t", abi.I16: "int16_t", abi.U16: "uint16_t",
	abi.I32: "int32_t", abi.U32: "uint32_t", abi.I64: "int64_t", abi.U64: "uint64_t",
	abi.F32: "float", abi.F64: "double",
}

func cType(t abi.Type) (string, error) {
	if kind := cTypes[t]; kind != "" {
		return kind, nil
	}
	return "", fmt.Errorf("unsupported scalar/opaque-pointer type %d", t)
}

func qualifiedName(s string) bool {
	for _, part := range strings.Split(s, "::") {
		if !identifier(part) {
			return false
		}
	}
	return true
}

func identifier(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if c != '_' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(i != 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return !keywords[s]
}

var keywords = func() map[string]bool {
	words := `alignas alignof and and_eq asm auto bitand bitor bool break case catch
char char8_t char16_t char32_t class compl co_await co_return co_yield concept
const consteval constexpr constinit const_cast continue decltype default delete
do double dynamic_cast else enum explicit export extern false float for friend
goto if inline int long mutable namespace new noexcept not not_eq nullptr
operator or or_eq private protected public register reinterpret_cast requires
return short signed sizeof static static_assert static_cast struct switch
template this thread_local throw true try typedef typeid typename union unsigned
using virtual void volatile wchar_t while xor xor_eq _Alignas _Alignof _Atomic
_Bool _Complex _Generic _Imaginary _Noreturn _Static_assert _Thread_local restrict`
	result := make(map[string]bool)
	for _, word := range strings.Fields(words) {
		result[word] = true
	}
	return result
}()
