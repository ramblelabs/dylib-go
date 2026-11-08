package cxxexample

import (
	"context"
	"fmt"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// HeaderSelections discovers a complete header, then supplies explicit ABI and
// ownership choices for selected global/qualified functions and class aliases.
func HeaderSelections(ctx context.Context, header string, opts cxx.DiscoveryOptions) ([]cxx.Function, []cxx.Class, error) {
	catalog, err := cxx.DiscoverHeader(ctx, header, opts)
	if err != nil {
		return nil, nil, err
	}
	functions := []cxx.Function{
		{Name: "global_add", Symbol: "hd_add", Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32, abi.I32}}},
		{Name: "global_choose", Symbol: "hd_choose", Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}},
		{Name: "global_choose", Symbol: "hd_float", Signature: abi.Signature{Result: abi.F64, Args: []abi.Type{abi.F64}}},
		{Name: "local_global", Symbol: "hd_local", Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}},
		{Name: "header_c", Symbol: "hd_c", Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}},
		{Name: "hdr::scale", Symbol: "hd_scale", Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}},
		{Name: "hdr::included", Symbol: "hd_included", Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}},
		{Name: "hdr::inlined::offset", Symbol: "hd_offset", Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}},
		{Name: "header_plus", Symbol: "hd_plus", TemplateArgs: []string{"int32_t", "2"}, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}},
		{Name: "header_count", Symbol: "hd_pack", TemplateArgs: []string{"int32_t", "double"}, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32, abi.F64}}},
	}
	for _, f := range functions {
		found := false
		if f.TemplateArgs == nil {
			for _, d := range catalog.Functions {
				found = found || d.Name == f.Name && !d.Deleted
			}
		} else {
			for _, d := range catalog.FunctionTemplates {
				found = found || d.Function.Name == f.Name && !d.Function.Deleted
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("missing discovered function %s", f.Name)
		}
	}
	classes := []cxx.Class{
		{Name: "CounterAlias", Delete: "hd_delete", Constructors: []cxx.Constructor{{Symbol: "hd_new", Args: []abi.Type{abi.I32}}}, Methods: []cxx.Method{
			{Name: "add", Symbol: "hd_member", Const: true, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}},
			{Name: "twice", Symbol: "hd_twice", Const: true, TemplateArgs: []string{"int32_t"}, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}},
			{Name: "live", Symbol: "hd_live", Static: true, Signature: abi.Signature{Result: abi.I32}},
		}},
		{Name: "GlobalLambda", Methods: []cxx.Method{{Name: "operator()", Symbol: "hd_call", Const: true, TemplateArgs: []string{"int32_t"}, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}}}},
		{Name: "Sources", Methods: []cxx.Method{{Name: "get", Symbol: "hd_lambda", Static: true, Signature: abi.Signature{Result: abi.Pointer}, ObjectResult: &cxx.ObjectType{Name: "GlobalLambda", Kind: cxx.ObjectReference}}}},
	}
	for _, class := range classes {
		found := false
		for _, name := range catalog.Classes {
			found = found || name == class.Name
		}
		if !found {
			return nil, nil, fmt.Errorf("missing discovered class %s", class.Name)
		}
	}
	return functions, classes, nil
}
