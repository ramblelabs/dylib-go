package cxx

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
)

// BaseNameResolver selects an accessible qualified class name/alias for a public
// direct base declaration in derived's context. An empty name skips that branch.
// NativeType is descriptive metadata; resolving it is an application contract,
// not a namespace-prefix heuristic. The compiler checks that the selected type
// is a strict base of derived, but does not prove this spelling-to-alias mapping.
// Non-public declarations are retained in Classes but never passed to Resolve.
type BaseNameResolver func(derived string, base BaseDeclaration) (string, error)

// ClassHierarchy contains selected class declarations in the producing query's
// order and compiler-verified public, unambiguous conversions among those names.
// Conversions includes transitive/virtual relations; it is not a list of direct
// edges. Canonically equal aliases have no conversion between them. Declarations
// keep their original contextual Bases, including non-public or skipped entries.
// No offsets, ownership, inherited callables or export names are inferred.
type ClassHierarchy struct {
	Classes     []ClassDeclaration
	Conversions []BaseRelation
}

const maxHierarchyBases = 256

// DiscoverHierarchy follows public bases from one root using explicit Resolve
// selections. Each distinct selected name is visited once, including aliases of
// an already visited native type. At most 32 names and 256 direct base declarations
// across visited classes are allowed. Empty resolver results leave that branch
// unvisited; this API does not promise whole-header or complete graph discovery.
//
// Target, flags and compiler/header paths are frozen before callbacks run. Every
// class uses DiscoverMembers' existing query/AST bounds. Batched compiler checks
// reject unrelated/self selections and validate all returned conversions with
// DiscoverBases' rules. Invalid inputs, resolver/compiler errors, cancellation or
// exceeded budgets return nil without publishing partial results. Resolve runs
// synchronously and must honor any application-specific cancellation needs.
func DiscoverHierarchy(ctx context.Context, header, root string, resolve BaseNameResolver, opts DiscoveryOptions) (*ClassHierarchy, error) {
	if resolve == nil {
		return nil, fmt.Errorf("cxx: hierarchy requires a base-name resolver")
	}
	if err := validateHierarchyRoot(root); err != nil {
		return nil, err
	}
	query, opts, err := hierarchyCompiler(ctx, header, opts)
	if err != nil {
		return nil, err
	}
	names := []string{root}
	seen := map[string]bool{root: true}
	result := &ClassHierarchy{}
	var selections []BaseRelation
	bases := 0
	for i := 0; i < len(names); i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		declaration, err := DiscoverMembers(ctx, query.header, names[i], opts)
		if err != nil {
			return nil, err
		}
		bases += len(declaration.Bases)
		if bases > maxHierarchyBases {
			return nil, fmt.Errorf("cxx: hierarchy exceeds %d direct base declarations", maxHierarchyBases)
		}
		result.Classes = append(result.Classes, *declaration)
		for _, base := range declaration.Bases {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if base.Access != "public" {
				continue
			}
			name, err := resolve(declaration.Name, base)
			if err != nil {
				return nil, fmt.Errorf("cxx: resolve %s base %s: %w", declaration.Name, base.NativeType, err)
			}
			if name == "" {
				continue
			}
			if len(name) > maxDiscoveryName || !qualifiedName(name) {
				return nil, fmt.Errorf("cxx: invalid resolved base name %q", name)
			}
			selections = append(selections, BaseRelation{Derived: declaration.Name, Base: name})
			if !seen[name] {
				if len(names) == maxDiscoveryTypes {
					return nil, fmt.Errorf("cxx: hierarchy exceeds %d selected names", maxDiscoveryTypes)
				}
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(selections) == 0 {
		return result, nil
	}
	source, namespace := hierarchyProbes(selections)
	data, err := query.run(ctx, source, namespace)
	if err != nil {
		return nil, err
	}
	if err := decodeHierarchySelections(data, namespace, selections); err != nil {
		return nil, err
	}
	source, namespace, probes, err := baseProbes(names)
	if err != nil {
		return nil, err
	}
	data, err = query.run(ctx, source, namespace)
	if err != nil {
		return nil, err
	}
	result.Conversions, err = decodeBases(data, namespace, names, probes)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// Names and count were validated before building this bounded probe source.
func hierarchyProbes(selections []BaseRelation) (source, namespace string) {
	hash := sha256.Sum256([]byte(fmt.Sprint(selections)))
	namespace = fmt.Sprintf("dylib_go_hierarchy_%x", hash[:8])
	var body strings.Builder
	fmt.Fprintf(&body, "namespace %s {\ntemplate<bool> struct Answer {};\n", namespace)
	for i, s := range selections {
		fmt.Fprintf(&body, "using edge_%d = Answer<!__is_same(::%s, ::%s) && __is_base_of(::%s, ::%s)>;\n", i, s.Derived, s.Base, s.Base, s.Derived)
	}
	body.WriteString("}\n")
	return body.String(), namespace
}

func decodeHierarchySelections(data []byte, namespace string, selections []BaseRelation) error {
	values, err := decodeDiscoveryValues(data, namespace, len(selections))
	if err != nil {
		return err
	}
	for i, s := range selections {
		value, ok := values[fmt.Sprintf("edge_%d", i)]
		if !ok || !value {
			return fmt.Errorf("cxx: resolved %s must be a strict base of %s", s.Base, s.Derived)
		}
	}
	return nil
}

func validateHierarchyRoot(root string) error {
	if len(root) > maxDiscoveryName || !qualifiedName(root) {
		return fmt.Errorf("cxx: invalid hierarchy root %q", root)
	}
	return nil
}

// Freeze paths, flags and target before a hierarchy makes additional queries or
// invokes caller callbacks. query.args already includes the selected target.
func hierarchyCompiler(ctx context.Context, header string, opts DiscoveryOptions) (*discoveryCompiler, DiscoveryOptions, error) {
	query, err := discoveryCommand(ctx, header, opts)
	if err != nil {
		return nil, DiscoveryOptions{}, err
	}
	query.compiler, err = filepath.Abs(query.compiler)
	if err != nil {
		return nil, DiscoveryOptions{}, err
	}
	return query, DiscoveryOptions{Compiler: query.compiler, Flags: append([]string(nil), query.args...)}, nil
}
