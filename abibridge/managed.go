package abibridge

import (
	"errors"
	"fmt"

	dylib "github.com/ramblelabs/dylib-go"
)

// ErrManagedUnsupported indicates a facade without managed invocation support.
var ErrManagedUnsupported = errors.New("abibridge: managed calls require a version 3 native facade")

// Argument supplies either trivial Bytes or an owned Value. The signature's
// ValueType selects which field is required. A Value is borrowed for the whole
// invocation; native code must use ordinary guaranteed parameter ownership.
type Argument struct {
	Bytes []byte
	Value *Value
}

// Result contains trivial Bytes or one owned Value, selected by the signature.
// Close Value when finished; Runtime.Close also retires it. Managed storage is
// never returned as representation bytes.
type Result struct {
	Bytes []byte
	Value *Value
}

// InvokeManaged accepts mixed trivial and owned Swift values. Each managed
// argument must use the exact ValueType in the prepared signature. Inputs retain
// their original ownership; a successful managed result owns the returned value.
// This does not infer Swift passing modes from metadata: declare the actual
// compiler-confirmed scalar/components or formally indirect Type as usual.
// Native access still needs caller synchronization for non-Sendable values.
func (c *Call) InvokeManaged(function *dylib.Symbol, arguments ...Argument) (Result, error) {
	if c == nil || c.owner == nil {
		return Result{}, ErrClosed
	}
	r := c.owner
	if err := r.begin(); err != nil {
		return Result{}, err
	}
	defer r.end()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.plan == 0 {
		return Result{}, ErrClosed
	}
	if r.invokeValues == 0 {
		return Result{}, ErrManagedUnsupported
	}
	if len(arguments) != len(c.signature.Args) {
		return Result{}, fmt.Errorf("abibridge: got %d arguments, want %d", len(arguments), len(c.signature.Args))
	}
	var packed []byte
	var borrowed []*Value
	var handle uintptr
	defer func() {
		if handle != 0 {
			nativeRelease(r.valueOps.release, handle)
		}
		for i := len(borrowed) - 1; i >= 0; i-- {
			borrowed[i].releaseUse()
		}
	}()
	// Only opaque native handles cross the C boundary. Borrowing protects every
	// handle before validation/allocation can fail and before native entry.
	handles := make([]uint64, len(arguments)+1)
	for i, argument := range arguments {
		t := c.signature.Args[i]
		if t.ValueType == nil {
			if argument.Value != nil || len(argument.Bytes) != int(t.Size) {
				return Result{}, fmt.Errorf("abibridge: argument %d requires %d trivial bytes", i, t.Size)
			}
			packed = append(packed, argument.Bytes...)
			continue
		}
		v := argument.Value
		if v == nil || len(argument.Bytes) != 0 || v.valueType != t.ValueType {
			return Result{}, fmt.Errorf("abibridge: argument %d requires its declared Swift ValueType", i)
		}
		if _, err := v.borrow(); err != nil {
			return Result{}, err
		}
		borrowed = append(borrowed, v)
		// handle is immutable while a borrow is active, even during retirement.
		handles[i+1] = uint64(v.handle)
	}
	var output Result
	if t := c.signature.Result.ValueType; t != nil {
		var err error
		handle, err = nativeValueNew(r.valueOps.newValue, t.plan, 0)
		if err != nil {
			return Result{}, err
		}
		handles[0] = uint64(handle)
	} else {
		output.Bytes = make([]byte, c.signature.Result.Size)
	}
	err := function.WithAddress(func(address uintptr) error {
		return nativeInvokeValues(r.invokeValues, c.plan, address, packed, output.Bytes, handles)
	})
	if err != nil {
		return Result{}, err
	}
	if handle != 0 {
		// Native success commits the initialized result before Go publication.
		// A concurrent Runtime.Close makes publishValue destroy it exactly once.
		output.Value, err = r.publishValue(c.signature.Result.ValueType, handle)
		handle = 0
	}
	return output, err
}
