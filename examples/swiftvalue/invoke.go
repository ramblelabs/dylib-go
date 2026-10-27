package swiftvalue

import (
	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abibridge"
)

// Transform calls a thin Swift (T) -> T using guaranteed input ownership.
// shape includes the compiler-confirmed passing mode and prepared ValueType.
// The input remains owned by the caller. Close the returned Value when finished.
func Transform(runtime *abibridge.Runtime, symbol *dylib.Symbol, shape abibridge.Type, input *abibridge.Value) (*abibridge.Value, error) {
	call, err := runtime.Prepare(abibridge.Signature{Result: shape, Args: []abibridge.Type{shape}})
	if err != nil {
		return nil, err
	}
	defer call.Close()
	result, err := call.InvokeManaged(symbol, abibridge.Argument{Value: input})
	return result.Value, err
}
