package cxx

import (
	"fmt"
	"strings"

	"github.com/ramblelabs/dylib-go/abi"
)

// Function selects an exact C++ free-function or static-member signature. Name
// is a qualified native identifier (without a leading ::); Symbol is the C
// export. Arguments, results, object/function-pointer transports and concrete
// variadic tails have Method's contracts, without a receiver or cv/ref qualifiers.
type Function struct {
	Name, Symbol string
	// TemplateArgs selects an explicit specialization. Nil keeps ordinary
	// pointer-based selection; an empty non-nil slice emits <>. Up to 16
	// arguments may be qualified identifiers, builtin type/value keywords or
	// decimal integer literals with optional U/L/LL/UL/ULL suffixes. Use header aliases/constexpr names for
	// compound types and expressions. The C++ compiler checks their meaning.
	TemplateArgs   []string
	Signature      abi.Signature
	ObjectArgs     []ObjectArg
	ObjectResult   *ObjectType
	FunctionArgs   []FunctionArg
	FunctionResult *abi.Signature
}

// GenerateFunctions emits fixed C exports for an explicit function manifest.
// The matching C++17 compiler selects overloads through exact function pointers
// and supplies native calls, promotions and exception handling. No ABI or
// ownership is inferred from declarations. TemplateArgs selects bounded explicit
// template arguments; compound expressions/types use header names. Operators and
// non-static members require a separately named native wrapper. Generated C
// declarations and status/output/lifetime rules are identical to Generate's.
func GenerateFunctions(header string, functions []Function) (*Facade, error) {
	return generateFunctions(header, functions, facadeOptions{})
}

func generateFunctions(header string, functions []Function, options facadeOptions) (*Facade, error) {
	if len(functions) == 0 || len(functions) > maxFacadeFunctions {
		return nil, fmt.Errorf("cxx: select 1-256 functions")
	}
	classes := make([]Class, 0, len(functions))
	for _, f := range functions {
		if !qualifiedName(f.Name) {
			return nil, fmt.Errorf("cxx: invalid C++ function name %q", f.Name)
		}
		scope, name := "", f.Name
		if i := strings.LastIndex(f.Name, "::"); i >= 0 {
			scope, name = f.Name[:i], f.Name[i+2:]
		}
		// Static calls already provide the exact function-pointer and transport
		// path. Only global scope needs a spelling unavailable to Class manifests.
		classes = append(classes, Class{Name: scope, Methods: []Method{{
			Name: name, Symbol: f.Symbol, TemplateArgs: f.TemplateArgs, Signature: f.Signature, Static: true,
			ObjectArgs: f.ObjectArgs, ObjectResult: f.ObjectResult,
			FunctionArgs: f.FunctionArgs, FunctionResult: f.FunctionResult,
		}}})
	}
	options.allowGlobal = true
	return generate(header, classes, options)
}
