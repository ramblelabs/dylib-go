package cxx

import (
	"fmt"

	"github.com/ramblelabs/dylib-go/abi"
)

// FunctionArg replaces a zero-based abi.Pointer argument with an exact native
// function pointer. Signatures accept fixed scalar/opaque-pointer default/cdecl
// calls, including void results. Null entries are passed through to the API.
// Addresses must belong to native functions or leased C callback entries; Go
// function values are not native entries. Retained entries need explicit leases
// and native unregister/join before release, for example using a Registration.
type FunctionArg struct {
	Index     int
	Signature abi.Signature
}

func functionAlias(signature abi.Signature, alias string) (string, error) {
	if signature.Variadic {
		return "", fmt.Errorf("native function pointer: expected a fixed signature")
	}
	if err := methodSignature(signature); err != nil {
		return "", fmt.Errorf("native function pointer: %w", err)
	}
	_, args, _, _ := arguments(signature.Args)
	result, _ := cType(signature.Result)
	// The qualified producer targets support conversions between opaque address
	// transports and function pointers. Spell cdecl explicitly on Windows so the
	// callback contract does not depend on the compiler's default call convention.
	return fmt.Sprintf("#if defined(_WIN32)\nusing %s = %s (__cdecl *)(%s);\n#else\nusing %s = %s (*)(%s);\n#endif\n", alias, result, args, alias, result, args), nil
}
