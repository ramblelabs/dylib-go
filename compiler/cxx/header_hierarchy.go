package cxx

import "context"

// DiscoverHeaderHierarchy selects a root's public base closure from accessible
// class names/aliases throughout the header and its includes. Global, named
// namespace, public nested and class-template alias names use DiscoverHeader's
// class eligibility and ordering rules. Ordinary function/template catalog counts
// do not participate; the shared AST IO/node/depth bounds still apply.
//
// The query reuses DiscoverNamespaceHierarchy's traversal, alias treatment,
// compiler-checked public/unambiguous conversions and limits: at most 256 class
// candidates, 32 selected names and 256 direct base declarations. The explicit
// root is first. Base candidates follow header declaration order, and canonical
// aliases of the root itself are excluded. Ambiguous root conversions are omitted;
// available intermediate views can still select the base through separate paths.
//
// Direct base metadata includes non-public edges for diagnostics. This discovers
// names represented by the bounded header catalog, without guessing contextual
// spellings or inferring offsets, ABI, ownership, inherited members or C exports.
// Compiler/header paths, flags and target are frozen before additional queries.
// Files/environment must remain stable. Any error returns nil, never a partial
// graph. Use DiscoverHierarchy for explicit name mappings or branch exclusions.
func DiscoverHeaderHierarchy(ctx context.Context, header, root string, opts DiscoveryOptions) (*ClassHierarchy, error) {
	if err := validateHierarchyRoot(root); err != nil {
		return nil, err
	}
	query, frozen, err := hierarchyCompiler(ctx, header, opts)
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
	candidates, err = query.eligibleClasses(ctx, candidates)
	if err != nil {
		return nil, err
	}
	return discoverCatalogHierarchy(ctx, query, root, candidates, frozen)
}
