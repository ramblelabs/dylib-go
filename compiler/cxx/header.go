package cxx

import "context"

// HeaderDeclaration catalogs accessible class/alias names, ordinary functions
// and primary function templates in a header's complete translation unit.
// Native spellings are descriptive metadata, not inferred ABI or ownership.
type HeaderDeclaration struct {
	Classes           []string
	Functions         []FunctionDeclaration
	FunctionTemplates []FunctionTemplateDeclaration
}

// DiscoverHeader queries global and named/inline namespace declarations,
// including included headers. It shares the existing class/function/template
// catalogs' eligibility, exclusions, ordering and identity rules. Namespace
// aliases and using imports do not duplicate declarations. Public nested class
// names and aliases to class-template specializations or lambdas participate.
// Members, anonymous scopes, locals and unselected class templates are excluded.
// Call DiscoverMembers/DiscoverMemberTemplates for a selected returned class.
//
// One complete Clang C++17 AST query and at most one class-eligibility query
// are used. Each catalog has at most 256 entries (class candidates are bounded
// before eligibility); template declarations have at most 64 parameters including
// nested lists. The existing header, name, AST, node/depth and cancellation bounds
// apply. Large included/system headers can exceed them; use the namespace APIs
// to narrow such inputs. Failures return nil, never a partial catalog.
func DiscoverHeader(ctx context.Context, header string, opts DiscoveryOptions) (*HeaderDeclaration, error) {
	query, err := discoveryCommand(ctx, header, opts)
	if err != nil {
		return nil, err
	}
	ast, err := query.headerAST(ctx)
	if err != nil {
		return nil, err
	}
	candidates, _, err := ast.classCandidates("", ast.unit.ID)
	if err != nil {
		return nil, err
	}
	functions, err := ast.namespaceFunctions("", ast.unit.ID)
	if err != nil {
		return nil, err
	}
	templates, err := ast.namespaceFunctionTemplates("", ast.unit.ID)
	if err != nil {
		return nil, err
	}
	classes, err := query.eligibleClasses(ctx, candidates)
	if err != nil {
		return nil, err
	}
	return &HeaderDeclaration{Classes: classes, Functions: functions, FunctionTemplates: templates}, nil
}

func qualifiedDeclarationName(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "::" + name
}

// Whole-unit traversal starts at its genuine AST root once; namespace catalogs
// still select only the nominated namespace identity and its reopenings.
func (ast *memberAST) catalogRoots(namespace, id string) []*memberNode {
	if namespace == "" {
		if ast.unit != nil && ast.unit.ID == id {
			return []*memberNode{ast.unit}
		}
		return nil
	}
	var roots []*memberNode
	for _, n := range ast.namespaces {
		if n.ID == id || n.OriginalNamespace != nil && n.OriginalNamespace.ID == id {
			roots = append(roots, n)
		}
	}
	return roots
}

// Header catalogs share one complete AST query and the same bounded decoder.
func (query *discoveryCompiler) headerAST(ctx context.Context) (*memberAST, error) {
	data, err := query.run(ctx, "", "")
	if err != nil {
		return nil, err
	}
	return decodeHeaderAST(data)
}
