package cxxexample

import (
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// TemplateFunctions selects concrete type/value arguments without putting C++
// expressions in the manifest. Aliases name compound types; nil and empty
// TemplateArgs deliberately select different overload sets.
func TemplateFunctions() []cxx.Function {
	function := func(name, symbol string, result abi.Type, args ...abi.Type) cxx.Function {
		return cxx.Function{Name: name, Symbol: symbol, Signature: abi.Signature{Result: result, Args: args}}
	}
	add := function("template_alias::add", "tmpl_add", abi.I32, abi.I32)
	add.TemplateArgs = []string{"templates::Scalar", "templates::offset"}
	floating := function("templates::add", "tmpl_floating", abi.F64, abi.F64)
	floating.TemplateArgs = []string{"double", "2"}
	chosen := function("templates::choose", "tmpl_choose", abi.I32, abi.I32)
	chosen.TemplateArgs = []string{}
	ordinary := function("templates::choose", "tmpl_ordinary", abi.I32, abi.I32)
	pack := function("templates::count", "tmpl_pack", abi.I32, abi.I32, abi.F64)
	pack.TemplateArgs = []string{"int32_t", "double"}
	boxed := function("templates::boxed", "tmpl_boxed", abi.I32, abi.I32)
	boxed.TemplateArgs = []string{"templates::Box"}
	signed := function("templates::signed_limit", "tmpl_signed", abi.I32)
	signed.TemplateArgs = []string{"-9223372036854775808LL"}
	unsigned := function("templates::unsigned_limit", "tmpl_unsigned", abi.I32)
	unsigned.TemplateArgs = []string{"18446744073709551615ULL"}
	sum := function("templates::sum", "tmpl_sum", abi.I32, abi.I32, abi.I8, abi.F32)
	sum.TemplateArgs = []string{"int32_t"}
	sum.Signature.Variadic = true
	sum.Signature.FixedArgs = 1
	entry := abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}
	factory := function("templates::factory", "tmpl_factory", abi.Pointer)
	factory.TemplateArgs = []string{"int32_t"}
	factory.FunctionResult = &entry
	invoke := function("templates::invoke", "tmpl_invoke", abi.I32, abi.Pointer, abi.I32)
	invoke.TemplateArgs = []string{"int32_t"}
	invoke.FunctionArgs = []cxx.FunctionArg{{Index: 0, Signature: entry}}
	token := cxx.ObjectType{Name: "templates::Token", Kind: cxx.ObjectReference}
	borrowed := function("templates::token", "tmpl_token", abi.Pointer)
	borrowed.ObjectResult = &token
	pointer := cxx.ObjectType{Name: token.Name, Kind: cxx.ObjectPointer}
	echo := function("templates::identity", "tmpl_pointer", abi.Pointer, abi.Pointer)
	echo.TemplateArgs = []string{"templates::TokenPtr"}
	echo.ObjectArgs = []cxx.ObjectArg{{Index: 0, Type: pointer}}
	echo.ObjectResult = &pointer
	constant := token
	constant.Const = true
	read := function("templates::read", "tmpl_read", abi.I32, abi.Pointer)
	read.TemplateArgs = []string{token.Name}
	read.ObjectArgs = []cxx.ObjectArg{{Index: 0, Type: constant}}
	lambda := function("templates::lambda", "tmpl_lambda", abi.Pointer)
	lambda.ObjectResult = &cxx.ObjectType{Name: "templates::Lambda", Kind: cxx.ObjectReference}
	return []cxx.Function{add, floating, chosen, ordinary, pack, boxed, signed, unsigned, sum, factory, invoke, borrowed, echo, read, lambda}
}

// TemplateClasses uses the same bounded argument syntax for ordinary/static
// member templates and a generic lambda's const call-operator template.
func TemplateClasses() []cxx.Class {
	return []cxx.Class{
		{Name: "templates::Counter", Delete: "tmpl_delete", Constructors: []cxx.Constructor{{Symbol: "tmpl_new", Args: []abi.Type{abi.I32}}}, Methods: []cxx.Method{
			{Name: "add", Symbol: "tmpl_member", Const: true, Ref: cxx.RefLValue, TemplateArgs: []string{"int32_t", "2"}, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}},
			{Name: "twice", Symbol: "tmpl_static", Static: true, TemplateArgs: []string{"int32_t"}, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}},
			{Name: "choose", Symbol: "tmpl_member_choose", Const: true, TemplateArgs: []string{}, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}},
			{Name: "choose", Symbol: "tmpl_member_ordinary", Const: true, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}},
		}},
		{Name: "templates::Sources", Methods: []cxx.Method{{Name: "get", Symbol: "tmpl_get_lambda", Static: true, Signature: abi.Signature{Result: abi.Pointer}, ObjectResult: &cxx.ObjectType{Name: "templates::Lambda", Kind: cxx.ObjectReference}}}},
		{Name: "templates::Lambda", Methods: []cxx.Method{{Name: "operator()", Symbol: "tmpl_call", Const: true, TemplateArgs: []string{"int32_t"}, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}}}},
	}
}
