package cbridge

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/ramblelabs/dylib-go/abi"
)

// Native arguments are initialized, never assigned member by member. This also
// constructs const subobjects and arrays using their original C declarations.
type nativeInitializer struct {
	generator   *generator
	prefix      string
	serial      int
	definitions bytes.Buffer
	macros      []string
}

func (n *nativeInitializer) define(name string, parameters []string, expression string) error {
	fmt.Fprintf(&n.definitions, "#define %s(%s) %s\n", name, strings.Join(parameters, ","), expression)
	n.macros = append(n.macros, name)
	if n.definitions.Len() > maxSource {
		return fmt.Errorf("initializer source exceeds 1 MiB")
	}
	return nil
}

func (n *nativeInitializer) expression(d abi.TypeDesc, native, source string, indices []string) (string, error) {
	switch d.Type {
	case abi.Pointer:
		// The declarator is in scope in its initializer. typeof is unevaluated,
		// retaining the exact native object/function-pointer field type.
		return fmt.Sprintf("(__typeof__(%s))(%s)", native, source), nil
	case abi.Struct:
		if _, err := n.generator.record(d); err != nil {
			return "", err
		}
		var out strings.Builder
		out.WriteString("{")
		for i, field := range d.Fields {
			if !identifier(field.Name) {
				return "", fmt.Errorf("record field requires an accessible C identifier")
			}
			value, err := n.expression(field.Type, "("+native+")."+field.Name, fmt.Sprintf("(%s).v%d", source, i), indices)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&out, ".%s=%s,", field.Name, value)
			if out.Len() > maxSource {
				return "", fmt.Errorf("initializer source exceeds 1 MiB")
			}
		}
		out.WriteString("}")
		return out.String(), nil
	case abi.Array:
		n.serial++
		name := fmt.Sprintf("%sa%d", n.prefix, n.serial)
		index := name + "_i"
		parameters := append(append([]string(nil), indices...), index)
		value, err := n.expression(*d.Elem, native+"[("+index+")]", source+"[("+index+")]", parameters)
		if err != nil {
			return "", err
		}
		if err := n.define(name, parameters, value); err != nil {
			return "", err
		}
		// Constant-size blocks keep a 64 KiB byte array's source bounded without
		// repeating long access paths. Nested macros explicitly capture every
		// outer index; expansion must not depend on a free parent parameter.
		const blockSize = 64
		block := name + "_block"
		if d.Len >= blockSize {
			items := make([]string, blockSize)
			for i := range items {
				args := append(append([]string(nil), indices...), fmt.Sprintf("((%s)+%d)", index, i))
				items[i] = name + "(" + strings.Join(args, ",") + ")"
			}
			if err := n.define(block, parameters, strings.Join(items, ",")); err != nil {
				return "", err
			}
		}
		var out strings.Builder
		out.WriteString("{")
		for i := 0; i < d.Len; {
			macro, step := name, 1
			if d.Len-i >= blockSize {
				macro, step = block, blockSize
			}
			args := append(append([]string(nil), indices...), fmt.Sprint(i))
			fmt.Fprintf(&out, "%s(%s),", macro, strings.Join(args, ","))
			i += step
			if out.Len() > maxSource {
				return "", fmt.Errorf("initializer source exceeds 1 MiB")
			}
		}
		out.WriteString("}")
		return out.String(), nil
	default:
		return source, nil
	}
}
