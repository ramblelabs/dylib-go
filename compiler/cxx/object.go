package cxx

import (
	"fmt"
	"strings"

	"github.com/ramblelabs/dylib-go/abi"
)

// ObjectKind selects how a native class crosses a C++ call. The C facade always
// transports its address as an opaque pointer; Go never copies its object bytes.
type ObjectKind uint8

const (
	ObjectPointer ObjectKind = iota + 1
	ObjectReference
	ObjectValue
	ObjectRValueReference // Borrowed T&&; does not consume the caller's handle.
)

// ObjectType selects an exact named native type. ObjectPointer also accepts
// named scalar, void, enum, union, array and pointer pointees; no Go layout is
// required. Const and Volatile qualify the named pointee/referent;
// cv-qualified values are unsupported. Value results are newly allocated and
// owned; pointer/reference results borrow the native API's lifetime. Const is a
// native signature contract, not a restriction enforced on Go address values.
type ObjectType struct {
	Name            string
	Kind            ObjectKind
	Const, Volatile bool
}

// ObjectArg replaces a zero-based abi.Pointer argument with a named native type.
// References and values require non-null addresses. Value arguments copy the
// object in C++ and require a noexcept destructor; copy constructors may throw.
// Rvalue references pass the existing object as an xvalue without copying it or
// consuming its handle. The native operation defines its post-call state.
type ObjectArg struct {
	Index int
	Type  ObjectType
}

func (o ObjectType) native() (string, error) {
	if !qualifiedName(o.Name) || o.Kind < ObjectPointer || o.Kind > ObjectRValueReference || (o.Const || o.Volatile) && o.Kind == ObjectValue {
		return "", fmt.Errorf("invalid native object type %+v", o)
	}
	name := cvQualified(o.Name, o.Const, o.Volatile)
	switch o.Kind {
	case ObjectPointer:
		name += " *"
	case ObjectReference:
		name += " &"
	case ObjectRValueReference:
		name += " &&"
	}
	return name, nil
}

func cvQualified(name string, constant, volatile bool) string {
	if constant {
		name = "const " + name
	}
	if volatile {
		name = "volatile " + name
	}
	return name
}

// Throwing temporary destructors could run after a new result is constructed but
// before its handle is published. Exclude that contract for by-value boundaries.
func valueDestructor(name, alias string) string {
	return fmt.Sprintf("using %s = %s;\nstatic_assert(noexcept(static_cast<%s *>(nullptr)->~%s()), \"class values require noexcept destruction\");\n", alias, name, alias, alias)
}

func nativeArguments(args []abi.Type, objects []ObjectArg, functions []FunctionArg) (parameters, types []string, values, checks string, err error) {
	parameters, _, _, err = arguments(args)
	if err != nil {
		return
	}
	kinds, names := make([]string, len(args)), make([]string, len(args))
	for i, arg := range args {
		kinds[i], _ = cType(arg)
		names[i] = fmt.Sprintf("p%d", i)
	}
	seen := make(map[int]bool)
	var validation strings.Builder
	for _, object := range objects {
		i, native := object.Index, object.Type
		if i < 0 || i >= len(args) || args[i] != abi.Pointer || seen[i] {
			err = fmt.Errorf("invalid or duplicate native object argument %d: expected a pointer slot", i)
			return
		}
		seen[i] = true
		kinds[i], err = native.native()
		if err != nil {
			return
		}
		pointee := cvQualified(native.Name, native.Const, native.Volatile)
		names[i] = fmt.Sprintf("static_cast<%s *>(p%d)", pointee, i)
		if native.Kind != ObjectPointer {
			fmt.Fprintf(&validation, "if (!p%d) return DYLIB_CXX_INVALID_ARGUMENT;\n", i)
			names[i] = "*" + names[i]
		}
		if native.Kind == ObjectValue {
			validation.WriteString(valueDestructor(native.Name, fmt.Sprintf("dylib_value_%d", i)))
			names[i] = native.Name + "(" + names[i] + ")"
		} else if native.Kind == ObjectRValueReference {
			names[i] = fmt.Sprintf("static_cast<%s &&>(%s)", pointee, names[i])
		}
	}
	for _, function := range functions {
		i := function.Index
		if i < 0 || i >= len(args) || args[i] != abi.Pointer || seen[i] {
			err = fmt.Errorf("invalid or duplicate native function argument %d: expected an exclusive pointer slot", i)
			return
		}
		seen[i] = true
		alias := fmt.Sprintf("dylib_function_%d", i)
		var declaration string
		declaration, err = functionAlias(function.Signature, alias)
		if err != nil {
			return
		}
		validation.WriteString(declaration)
		kinds[i] = alias
		names[i] = fmt.Sprintf("reinterpret_cast<%s>(p%d)", alias, i)
	}
	types, values, checks = kinds, strings.Join(names, ", "), validation.String()
	return
}
