package cbridge

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/clang"
)

func (g *generator) function(out *bytes.Buffer, d clang.Declaration, symbol string) (Binding, error) {
	s := d.Signature
	input := abi.TypeDesc{Type: abi.Struct}
	for i := range s.Args {
		input.Fields = append(input.Fields, abi.Field{Name: fmt.Sprintf("a%d", i), Type: carrier(s.ArgumentType(i))})
	}
	if len(input.Fields) == 0 {
		input.Fields = []abi.Field{{Name: "reserved", Type: abi.TypeDesc{Type: abi.U8}}}
	}
	output := abi.TypeDesc{Type: abi.Struct, Fields: []abi.Field{{Name: "result", Type: carrier(s.ReturnType())}}}
	if s.Result == abi.Void {
		output.Fields[0] = abi.Field{Name: "reserved", Type: abi.TypeDesc{Type: abi.U8}}
	}
	in, err := g.transport(input)
	if err != nil {
		return Binding{}, err
	}
	result, err := g.transport(output)
	if err != nil {
		return Binding{}, err
	}
	var layouts []*transport
	seen := map[*transport]bool{}
	var collect func(*transport)
	collect = func(t *transport) {
		if seen[t] {
			return
		}
		seen[t] = true
		layouts = append(layouts, t)
		for _, child := range t.children {
			collect(child)
		}
	}
	collect(in)
	collect(result)
	words := 2
	for _, t := range layouts {
		words += 2
		if t.desc.Type == abi.Struct {
			words += len(t.desc.Fields)
		}
		if t.desc.Type == abi.Array {
			words++
		}
	}
	if words > 8192 {
		return Binding{}, fmt.Errorf("layout query exceeds 64 KiB")
	}
	// Private identifiers are derived from the facade symbol, never from source
	// parameter names. Macro or declaration conflicts remain compiler errors.
	local := g.prefix + symbol + "_"
	target, outputPtr, inputPtr := local+"target", local+"output", local+"input"
	fmt.Fprintf(out, "%s int32_t %s(void *%s, void *%s, const void *%s) {\n", g.api, symbol, target, outputPtr, inputPtr)
	fmt.Fprintf(out, "if (!%s || !%s) return 1;\nif (!%s) {\n", outputPtr, inputPtr, target)
	fmt.Fprintf(out, "uint64_t %scapacity; __builtin_memcpy(&%scapacity,%s,sizeof(%scapacity));\nif (%scapacity != %d) return 2;\nuint64_t *%smeta=(uint64_t *)%s;\n", local, local, inputPtr, local, local, words, local, outputPtr)
	index := 0
	write := func(expression string) {
		fmt.Fprintf(out, "%smeta[%d]=(uint64_t)(%s);\n", local, index, expression)
		index++
	}
	write(fmt.Sprintf("UINT64_C(0x%x)", layoutMagic))
	write(fmt.Sprint(words))
	for _, t := range layouts {
		write("sizeof(" + t.name + ")")
		write("_Alignof(" + t.name + ")")
		if t.desc.Type == abi.Struct {
			for i := range t.desc.Fields {
				write(fmt.Sprintf("offsetof(%s,v%d)", t.name, i))
			}
		}
		if t.desc.Type == abi.Array {
			write("sizeof(" + t.children[0].name + ")")
		}
	}
	fmt.Fprint(out, "return 0;\n}\n")
	fmt.Fprintf(out, "const %s *%sin=(const %s *)%s;\n%s *%sout=(%s *)%s;\n", in.name, local, in.name, inputPtr, result.name, local, result.name, outputPtr)
	var args []string
	// Macros are undefined after every argument, so functions share a short
	// facade-private namespace even when their requested exports have long names.
	initializer := nativeInitializer{generator: g, prefix: g.prefix + "init_"}
	for i := range s.Args {
		desc := s.ArgumentType(i)
		name := fmt.Sprintf("%sa%d", local, i)
		kind, err := scalar(desc.Type, false)
		if desc.Type == abi.Struct {
			record, lookupErr := g.record(desc)
			if lookupErr != nil {
				return Binding{}, lookupErr
			}
			kind, err = record.Name, nil
		}
		if err != nil {
			return Binding{}, err
		}
		expression, err := initializer.expression(desc, name, fmt.Sprintf("%sin->v%d", local, i), nil)
		if err != nil {
			return Binding{}, err
		}
		out.Write(initializer.definitions.Bytes())
		fmt.Fprintf(out, "%s %s=%s;\n", kind, name, expression)
		for _, macro := range initializer.macros {
			fmt.Fprintf(out, "#undef %s\n", macro)
		}
		initializer.definitions.Reset()
		initializer.macros = nil
		args = append(args, name)
	}
	call := fmt.Sprintf("((__typeof__(&%s))%s)(%s)", d.Name, target, strings.Join(args, ","))
	if s.Result == abi.Void {
		fmt.Fprintf(out, "%s;\n", call)
	} else {
		fmt.Fprintf(out, "__auto_type %sresult=%s;\n", local, call)
	}
	fmt.Fprintf(out, "__builtin_memset(%sout,0,sizeof(*%sout));\n", local, local)
	if s.Result != abi.Void {
		if err := g.copyOutput(out, s.ReturnType(), local+"out->v0", local+"result", local, 0); err != nil {
			return Binding{}, err
		}
	}
	fmt.Fprint(out, "return 0;\n}\n")
	return Binding{Name: d.Name, Symbol: symbol, NativeSymbol: d.Symbol, target: g.header.Target, signature: s.Clone(), input: input, output: output, layouts: layouts, words: words}, nil
}

func (g *generator) copyOutput(out *bytes.Buffer, d abi.TypeDesc, destination, source string, prefix string, depth int) error {
	if out.Len() > maxSource {
		return fmt.Errorf("source exceeds 1 MiB")
	}
	if d.Type == abi.Array {
		index := fmt.Sprintf("%si%d", prefix, depth)
		fmt.Fprintf(out, "for (size_t %s=0;%s<%d;++%s) {\n", index, index, d.Len, index)
		if err := g.copyOutput(out, *d.Elem, destination+"["+index+"]", source+"["+index+"]", prefix, depth+1); err != nil {
			return err
		}
		fmt.Fprint(out, "}\n")
		return nil
	}
	if d.Type == abi.Struct {
		if _, err := g.record(d); err != nil {
			return err
		}
		for i, field := range d.Fields {
			if !identifier(field.Name) {
				return fmt.Errorf("record field requires an accessible C identifier")
			}
			if err := g.copyOutput(out, field.Type, fmt.Sprintf("(%s).v%d", destination, i), "("+source+")."+field.Name, prefix, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if d.Type == abi.Pointer {
		fmt.Fprintf(out, "%s=(void *)(%s);\n", destination, source)
	} else {
		fmt.Fprintf(out, "%s=%s;\n", destination, source)
	}
	return nil
}
