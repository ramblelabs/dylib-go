package cxx

import (
	"context"
	"fmt"
	"reflect"
	"strings"
)

// FunctionDeclaration describes a named namespace function declaration. Name
// uses the caller's namespace spelling; NativeType and parameter types retain
// their native C++ declaration context. They are descriptive metadata, not ABI
// descriptors. Internal reports an explicit static storage class.
type FunctionDeclaration struct {
	Name, NativeType            string
	Parameters                  []ParameterDeclaration
	Variadic, Deleted, Internal bool
}

// DiscoverFunctions returns named free-function declarations in one explicit
// namespace, including named child/inline namespaces and linkage blocks. It
// preserves declaration order, overloads and redeclarations. Implicit functions,
// operators, templates, class members, locals, anonymous namespaces and imported
// using declarations/directives are excluded. Included headers participate.
//
// The query uses at most two filtered Clang AST invocations and reports at most
// 256 declarations, with DiscoverBases' name, IO and cancellation bounds. Each
// invocation resolves namespace identity afresh. Errors return no partial data.
// The caller chooses exact ABI signatures, ownership and export names for
// GenerateFunctions; native compilation still checks the selected prototypes.
func DiscoverFunctions(ctx context.Context, header, namespace string, opts DiscoveryOptions) ([]FunctionDeclaration, error) {
	ast, ref, _, err := discoverNamespace(ctx, header, namespace, opts)
	if err != nil {
		return nil, err
	}
	return ast.namespaceFunctions(namespace, ref.ID)
}

func (ast *memberAST) namespaceFunctions(namespace, id string) ([]FunctionDeclaration, error) {
	var result []FunctionDeclaration
	seen := make(map[string]FunctionDeclaration)
	var walk func([]memberNode, string) error
	walk = func(nodes []memberNode, prefix string) error {
		for _, n := range nodes {
			if n.IsImplicit {
				continue
			}
			switch n.Kind {
			case "LinkageSpecDecl":
				if err := walk(n.Inner, prefix); err != nil {
					return err
				}
			case "NamespaceDecl":
				if n.Name == "" {
					continue
				}
				if !identifier(n.Name) || len(prefix)+2+len(n.Name) > maxDiscoveryName {
					return fmt.Errorf("cxx: invalid or excessive function namespace")
				}
				if err := walk(n.Inner, prefix+"::"+n.Name); err != nil {
					return err
				}
			case "FunctionDecl":
				if !identifier(n.Name) {
					if strings.HasPrefix(n.Name, "operator") {
						continue
					}
					return fmt.Errorf("cxx: invalid function name")
				}
				name := prefix + "::" + n.Name
				if len(name) > maxDiscoveryName || n.ID == "" || n.Type.QualType == "" {
					return fmt.Errorf("cxx: invalid or excessive function declaration")
				}
				f := FunctionDeclaration{Name: name, NativeType: n.Type.QualType, Variadic: n.Variadic, Deleted: n.ExplicitlyDeleted, Internal: n.StorageClass == "static"}
				for _, p := range n.Inner {
					if p.Kind == "ParmVarDecl" {
						if p.Type.QualType == "" {
							return fmt.Errorf("cxx: missing native function parameter type")
						}
						f.Parameters = append(f.Parameters, ParameterDeclaration{Name: p.Name, NativeType: p.Type.QualType, HasDefault: p.Init != ""})
					}
				}
				if old, ok := seen[n.ID]; ok {
					if !reflect.DeepEqual(old, f) {
						return fmt.Errorf("cxx: conflicting function declaration identity")
					}
					continue
				}
				if len(result) == maxFacadeFunctions {
					return fmt.Errorf("cxx: namespace exceeds %d function declarations", maxFacadeFunctions)
				}
				seen[n.ID] = f
				result = append(result, f)
			}
		}
		return nil
	}
	for _, n := range ast.namespaces {
		if n.ID == id || n.OriginalNamespace != nil && n.OriginalNamespace.ID == id {
			if err := walk(n.Inner, namespace); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}
