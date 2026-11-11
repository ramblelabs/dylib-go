package cxx

import (
	"fmt"
	"strings"
)

// FunctionOverload selects a function by its native C++ parameter types.
// An empty list selects zero parameters. Spellings are bounded builtin type
// keywords or qualified identifiers. Header aliases can name compound types,
// including native pointers and function pointers. The compiler resolves their
// meaning and infers the selected function's result and transport signature.
type FunctionOverload struct {
	ParameterTypes []string
}

// MethodOverload selects native parameter types and receiver cv/ref qualifiers.
// Noexcept is inferred from the selected member. Ordinary receiver forms use
// RefNone, lvalue-qualified forms RefLValue and rvalue-qualified forms RefRValue.
type MethodOverload struct {
	ParameterTypes  []string
	Const, Volatile bool
	Ref             RefQualifier
}

func snapshotFunctionOverload(input *FunctionOverload) (*FunctionOverload, error) {
	if len(input.ParameterTypes) > maxInferredArguments {
		return nil, fmt.Errorf("at most %d overload parameter types are supported", maxInferredArguments)
	}
	for i, name := range input.ParameterTypes {
		if !validNativeTypeName(name) {
			return nil, fmt.Errorf("invalid overload parameter type %d %q", i, name)
		}
	}
	result := *input
	if input.ParameterTypes != nil {
		result.ParameterTypes = append([]string{}, input.ParameterTypes...)
	}
	return &result, nil
}

func validNativeTypeName(name string) bool {
	return len(name) <= maxDiscoveryName && name != "void" && (qualifiedName(name) || nativeBuiltinType(name))
}

func overloadArguments(arguments []string) string {
	return "<" + strings.Join(arguments, ", ") + ">"
}

func methodOverloadQualifiers(choice *MethodOverload) int {
	qualifiers := int(choice.Ref) * 4
	if choice.Const {
		qualifiers++
	}
	if choice.Volatile {
		qualifiers += 2
	}
	return qualifiers
}

// Expressions participate only in decltype, allowing the compiler to derive
// the result before resolving an address with a complete pointer target type.
func overloadValues(types []string) string {
	values := make([]string, len(types))
	for i, name := range types {
		values[i] = "argument<" + name + ">()"
	}
	return strings.Join(values, ", ")
}

// Named types resolve in the header's global namespace. The probe's private
// trait names and per-selection aliases must not capture a supplied type name.
func overloadTypes(types []string) []string {
	result := make([]string, len(types))
	for i, name := range types {
		result[i] = name
		if !nativeBuiltinType(name) {
			result[i] = "::" + name
		}
	}
	return result
}

const inferenceOverloadTraits = `
template<class T> T&& argument() noexcept;
template<class R,class... A> struct ChooseFunction {
 static constexpr auto pick(R(*p)(A...)) { return p; }
 static constexpr auto pick(R(*p)(A...) noexcept) { return p; }
};
`
