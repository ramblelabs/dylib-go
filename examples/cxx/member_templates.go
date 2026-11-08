package cxxexample

import (
	"context"
	"fmt"
	"strings"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// DiscoverTemplateClasses selects direct public templates from named class and
// generic-lambda aliases. NativeType helps select the declaration; ABI types,
// template arguments, qualifiers and lifetime remain explicit caller choices.
func DiscoverTemplateClasses(ctx context.Context, header string, opts cxx.DiscoveryOptions) ([]cxx.Class, error) {
	names := []string{"member_templates::Alias", "member_templates::Selected", "member_templates::Lambda"}
	catalogs := make([][]cxx.CallableTemplateDeclaration, len(names))
	for i, name := range names {
		var err error
		catalogs[i], err = cxx.DiscoverMemberTemplates(ctx, header, name, opts)
		if err != nil {
			return nil, err
		}
	}
	required := []struct {
		catalog      int
		kind         cxx.CallableKind
		name, suffix string
	}{
		{0, cxx.ConstructorDeclaration, "", ""},
		{0, cxx.MethodDeclaration, "add", "const &"},
		{0, cxx.MethodDeclaration, "add", "&&"},
		{0, cxx.MethodDeclaration, "twice", ""},
		{0, cxx.MethodDeclaration, "boxed", ""},
		{0, cxx.MethodDeclaration, "count", ""},
		{0, cxx.MethodDeclaration, "sum", ""},
		{0, cxx.ConversionDeclaration, "", "const"},
		{1, cxx.ConstructorDeclaration, "", ""},
		{1, cxx.MethodDeclaration, "add", "const"},
		{2, cxx.MethodDeclaration, "operator()", "const"},
	}
	for _, selection := range required {
		found := false
		for _, d := range catalogs[selection.catalog] {
			f := d.Callable
			if f.Kind == selection.kind && (selection.name == "" || f.Name == selection.name) && strings.HasSuffix(f.NativeType, selection.suffix) && !f.Deleted {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("missing public template %s::%s (%s)", names[selection.catalog], selection.name, selection.suffix)
		}
	}
	method := func(name, symbol string, result abi.Type, args ...abi.Type) cxx.Method {
		return cxx.Method{Name: name, Symbol: symbol, Signature: abi.Signature{Result: result, Args: args}}
	}
	add := method("add", "mt_add", abi.I32, abi.I32)
	add.TemplateArgs, add.Const, add.Ref = []string{"int32_t", "2"}, true, cxx.RefLValue
	move := method("add", "mt_move", abi.I32, abi.I32)
	move.TemplateArgs, move.Ref = []string{"int32_t"}, cxx.RefRValue
	floating := method("twice", "mt_float", abi.F64, abi.F64)
	floating.TemplateArgs, floating.Static = []string{"double"}, true
	boxed := method("boxed", "mt_box", abi.I32, abi.I32)
	boxed.TemplateArgs, boxed.Const = []string{"int32_t", "member_templates::Box"}, true
	pack := method("count", "mt_pack", abi.I32, abi.I32, abi.F64)
	pack.TemplateArgs, pack.Const = []string{"int32_t", "double"}, true
	sum := method("sum", "mt_sum", abi.I32, abi.I32, abi.I8, abi.F32)
	sum.TemplateArgs, sum.Const = []string{"int32_t"}, true
	sum.Signature.Variadic, sum.Signature.FixedArgs = true, 1
	conversion := method("", "mt_convert", abi.I32)
	conversion.Conversion, conversion.Const = true, true
	live := method("live", "mt_live", abi.I32)
	live.Static = true
	selected := method("add", "mt_selected_add", abi.I32, abi.I32)
	selected.TemplateArgs, selected.Const = []string{"int32_t"}, true
	lambda := method("operator()", "mt_call", abi.I32, abi.I32)
	lambda.TemplateArgs, lambda.Const = []string{"int32_t"}, true
	source := method("lambda", "mt_lambda", abi.Pointer)
	source.Static = true
	source.ObjectResult = &cxx.ObjectType{Name: names[2], Kind: cxx.ObjectReference}
	return []cxx.Class{
		{Name: names[0], Delete: "mt_delete", Constructors: []cxx.Constructor{{Symbol: "mt_new", Args: []abi.Type{abi.I32}}}, Methods: []cxx.Method{add, move, floating, boxed, pack, sum, conversion, live}},
		{Name: names[1], Delete: "mt_selected_delete", Constructors: []cxx.Constructor{{Symbol: "mt_selected_new", Args: []abi.Type{abi.I32}}}, Methods: []cxx.Method{selected}},
		{Name: "member_templates::Sources", Methods: []cxx.Method{source}},
		{Name: names[2], Methods: []cxx.Method{lambda}},
	}, nil
}
