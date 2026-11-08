package cxx

import (
	"context"
	"fmt"
	"reflect"
)

// TemplateParameterKind classifies a native C++ template parameter.
type TemplateParameterKind string

const (
	TemplateTypeParameter     TemplateParameterKind = "type"
	TemplateValueParameter    TemplateParameterKind = "value"
	TemplateTemplateParameter TemplateParameterKind = "template"
)

// TemplateParameterDeclaration is descriptive metadata. NativeType is present
// for value parameters; template parameters retain their nested parameter list.
// Default values/expressions are not evaluated. Name can be empty.
type TemplateParameterDeclaration struct {
	Kind             TemplateParameterKind
	Name, NativeType string
	Pack, HasDefault bool
	Parameters       []TemplateParameterDeclaration
}

// FunctionTemplateDeclaration retains a primary function-template declaration,
// not its instantiated specializations. Function's native types may be dependent
// and must not be interpreted as ABI descriptions.
type FunctionTemplateDeclaration struct {
	Function   FunctionDeclaration
	Parameters []TemplateParameterDeclaration
}

const maxTemplateParameters = 64

// DiscoverFunctionTemplates catalogs named free-function templates in an explicit
// namespace. It shares DiscoverFunctions' namespace, linkage, exclusion, query
// and IO bounds, preserving overloads/redeclarations and excluding instantiated
// specializations. Ordinary DiscoverFunctions behavior is unchanged. At most
// 256 declarations and 64 template parameters (including nested lists) per
// declaration are returned. Defaults and packs are descriptive only; callers
// choose Function.TemplateArgs, ABI, ownership and C export names explicitly.
func DiscoverFunctionTemplates(ctx context.Context, header, namespace string, opts DiscoveryOptions) ([]FunctionTemplateDeclaration, error) {
	ast, ref, _, err := discoverNamespace(ctx, header, namespace, opts)
	if err != nil {
		return nil, err
	}
	return ast.namespaceFunctionTemplates(namespace, ref.ID)
}

func (ast *memberAST) namespaceFunctionTemplates(namespace, id string) ([]FunctionTemplateDeclaration, error) {
	var result []FunctionTemplateDeclaration
	seen := make(map[string]FunctionTemplateDeclaration)
	err := ast.walkNamespaceFunctions(namespace, id, "FunctionTemplateDecl", func(prefix string, n memberNode) error {
		var primary *memberNode
		// Clang visits the templated declaration first, then specialization
		// definitions/references. A reference can have no TemplateArgument nodes.
		for i := range n.Inner {
			f := &n.Inner[i]
			if f.Kind != "FunctionDecl" {
				continue
			}
			if f.Name != n.Name || f.IsImplicit {
				return fmt.Errorf("cxx: invalid primary function template")
			}
			for _, child := range f.Inner {
				if child.Kind == "TemplateArgument" {
					return fmt.Errorf("cxx: specialization precedes primary function template")
				}
			}
			primary = f
			break
		}
		if n.ID == "" || primary == nil {
			return fmt.Errorf("cxx: missing primary function template")
		}
		f, err := functionDeclaration(prefix, *primary)
		if err != nil {
			return err
		}
		count := 0
		parameters, err := templateParameters(n.Inner, &count)
		if err != nil {
			return err
		}
		if len(parameters) == 0 {
			return fmt.Errorf("cxx: missing template parameters")
		}
		decl := FunctionTemplateDeclaration{Function: f, Parameters: parameters}
		if old, ok := seen[n.ID]; ok {
			if !reflect.DeepEqual(old, decl) {
				return fmt.Errorf("cxx: conflicting function template identity")
			}
			return nil
		}
		if len(result) == maxFacadeFunctions {
			return fmt.Errorf("cxx: namespace exceeds %d function template declarations", maxFacadeFunctions)
		}
		seen[n.ID] = decl
		result = append(result, decl)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func templateParameters(nodes []memberNode, count *int) ([]TemplateParameterDeclaration, error) {
	var result []TemplateParameterDeclaration
	for _, n := range nodes {
		var kind TemplateParameterKind
		switch n.Kind {
		case "TemplateTypeParmDecl":
			kind = TemplateTypeParameter
		case "NonTypeTemplateParmDecl":
			kind = TemplateValueParameter
		case "TemplateTemplateParmDecl":
			kind = TemplateTemplateParameter
		default:
			continue
		}
		if *count == maxTemplateParameters {
			return nil, fmt.Errorf("cxx: template exceeds %d parameters", maxTemplateParameters)
		}
		*count += 1
		if n.Name != "" && !identifier(n.Name) || kind == TemplateValueParameter && n.Type.QualType == "" || n.DefaultArg != nil && n.DefaultArg.Kind != "TemplateArgument" {
			return nil, fmt.Errorf("cxx: invalid template parameter metadata")
		}
		p := TemplateParameterDeclaration{Kind: kind, Name: n.Name, NativeType: n.Type.QualType, Pack: n.IsParameterPack, HasDefault: n.DefaultArg != nil}
		if kind == TemplateTemplateParameter {
			var err error
			p.Parameters, err = templateParameters(n.Inner, count)
			if err != nil {
				return nil, err
			}
			if len(p.Parameters) == 0 {
				return nil, fmt.Errorf("cxx: missing template-template parameters")
			}
		}
		result = append(result, p)
	}
	return result, nil
}
