package cxx

import (
	"context"
	"fmt"
	"reflect"
)

// CallableTemplateDeclaration describes a primary member-function template.
// Native types and nested template parameters are descriptive metadata in the
// selected class's context, never ABI descriptors or inferred ownership.
type CallableTemplateDeclaration struct {
	Callable   CallableDeclaration
	Parameters []TemplateParameterDeclaration
}

// DiscoverMemberTemplates queries direct public member-function templates of
// one complete non-cv class/alias, including generic lambda aliases resolved by
// DiscoverMembers' bounded anonymous-record lookup. Constructors, ordinary or
// static methods, conversions and call operators retain declaration order.
// Non-public, inherited, implicit and instantiated specializations are excluded.
// Ordinary DiscoverMembers continues to exclude function-template declarations.
//
// At most 256 templates and 64 template parameters (including nested lists) per
// declaration are reported, with DiscoverMembers' name, IO and cancellation
// bounds. Failures return no partial catalog. Callers choose native signatures,
// qualifiers, template arguments, exports and ownership for Generate. Constructor
// overload deduction is native; explicit conversion-template arguments still
// require a named wrapper. Metadata alone does not make a template callable.
func DiscoverMemberTemplates(ctx context.Context, header, name string, opts DiscoveryOptions) ([]CallableTemplateDeclaration, error) {
	record, err := discoverMemberDefinition(ctx, header, name, opts)
	if err != nil {
		return nil, err
	}
	return publicMemberTemplates(record)
}

func publicMemberTemplates(record *memberNode) ([]CallableTemplateDeclaration, error) {
	nodes, err := publicMemberDeclarations(record)
	if err != nil {
		return nil, err
	}
	var result []CallableTemplateDeclaration
	seen := make(map[string]CallableTemplateDeclaration)
	for _, n := range nodes {
		if n.Kind != "FunctionTemplateDecl" {
			continue
		}
		primary, parameters, err := primaryTemplateDeclaration(n, "CXXConstructorDecl", "CXXMethodDecl", "CXXConversionDecl")
		if err != nil {
			return nil, err
		}
		call, ok, err := callableDeclaration(*primary)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("cxx: invalid member template callable")
		}
		decl := CallableTemplateDeclaration{Callable: call, Parameters: parameters}
		if old, exists := seen[n.ID]; exists {
			if !reflect.DeepEqual(old, decl) {
				return nil, fmt.Errorf("cxx: conflicting member template identity")
			}
			continue
		}
		if len(result) == maxFacadeFunctions {
			return nil, fmt.Errorf("cxx: class exceeds %d member templates", maxFacadeFunctions)
		}
		seen[n.ID] = decl
		result = append(result, decl)
	}
	return result, nil
}
