package cxx

import (
	"fmt"

	"github.com/ramblelabs/dylib-go/abi"
)

// FunctionArg replaces a zero-based abi.Pointer argument with an exact native
// function pointer. Signatures accept scalar/opaque-pointer default/cdecl calls,
// including void results. A Variadic signature contains only its fixed prefix:
// len(Args) must equal FixedArgs. Concrete tails belong to each native call,
// not the pointer's type. Null entries are passed through to the API.
// Addresses must belong to native functions or leased C callback entries; Go
// function values are not native entries. Retained entries need explicit leases
// and native unregister/join before release, for example using a Registration.
type FunctionArg struct {
	Index     int
	Signature abi.Signature
}

func functionAlias(signature abi.Signature, alias string) (string, error) {
	if err := methodSignature(signature); err != nil {
		return "", fmt.Errorf("native function pointer: %w", err)
	}
	if signature.Variadic && len(signature.Args) != signature.FixedArgs {
		return "", fmt.Errorf("native variadic function pointer: describe only the fixed prefix")
	}
	_, args, _, _ := arguments(signature.Args)
	if signature.Variadic {
		args += ", ..."
	}
	result, _ := cType(signature.Result)
	// The qualified producer targets support conversions between opaque address
	// transports and function pointers. Spell cdecl explicitly on Windows so the
	// callback contract does not depend on the compiler's default call convention.
	return fmt.Sprintf("#if defined(_WIN32)\nusing %s = %s (__cdecl *)(%s);\n#else\nusing %s = %s (*)(%s);\n#endif\n", alias, result, args, alias, result, args), nil
}
