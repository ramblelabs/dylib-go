package cxx

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
)

const maxHierarchyNamespaces = 16

// DiscoverNamespaceHierarchy selects a root's public base closure from accessible
// class names/aliases discovered in 1-16 explicit namespaces. It uses
// DiscoverClasses' namespace scope and eligibility rules, then the compiler's
// strict public/unambiguous conversion checks rather than contextual base-name
// spellings. Named aliases can represent template or otherwise inaccessible types.
// Types absent from these catalogs are outside the query's scope.
//
// Classes starts with the explicit root. For each visited class, all convertible
// candidate bases are appended in namespace/declaration order, including transitive
// bases. Exact names are visited once; distinct aliases may remain separate except
// aliases of the root itself. Ambiguous root-to-base conversions are omitted, but
// visiting available intermediate views can still select that base. Conversions
// contains all verified conversions among the selected names. Direct base metadata,
// ABI choices, ownership and export names retain DiscoverHierarchy's semantics.
//
// At most 256 distinct catalog names, 32 selected names and 256 direct base
// declarations across selected classes are allowed. Compiler/header paths, target
// and flags are frozen before discovery. Existing per-query IO/cancellation bounds
// apply; files and process environment must remain stable throughout. Invalid
// inputs, compiler errors, cancellation or exceeded bounds return nil, never a
// partial result. Use DiscoverHierarchy for explicit name mapping or branch skips.
func DiscoverNamespaceHierarchy(ctx context.Context, header, root string, namespaces []string, opts DiscoveryOptions) (*ClassHierarchy, error) {
	if len(root) > maxDiscoveryName || !qualifiedName(root) {
		return nil, fmt.Errorf("cxx: invalid hierarchy root %q", root)
	}
	namespaces = append([]string(nil), namespaces...)
	if len(namespaces) == 0 || len(namespaces) > maxHierarchyNamespaces {
		return nil, fmt.Errorf("cxx: select 1-%d hierarchy namespaces", maxHierarchyNamespaces)
	}
	seenNamespaces := make(map[string]bool)
	for _, name := range namespaces {
		if len(name) > maxDiscoveryName || !qualifiedName(name) || seenNamespaces[name] {
			return nil, fmt.Errorf("cxx: invalid or duplicate hierarchy namespace %q", name)
		}
		seenNamespaces[name] = true
	}
	query, err := discoveryCommand(ctx, header, opts)
	if err != nil {
		return nil, err
	}
	query.compiler, err = filepath.Abs(query.compiler)
	if err != nil {
		return nil, err
	}
	opts = DiscoveryOptions{Compiler: query.compiler, Flags: append([]string(nil), query.args...)}
	var candidates []string
	cataloged := make(map[string]bool)
	for _, namespace := range namespaces {
		found, err := DiscoverClasses(ctx, query.header, namespace, opts)
		if err != nil {
			return nil, err
		}
		for _, name := range found {
			if !cataloged[name] {
				if len(candidates) == maxDiscoveredClasses {
					return nil, fmt.Errorf("cxx: hierarchy catalogs exceed %d candidate classes", maxDiscoveredClasses)
				}
				cataloged[name] = true
				candidates = append(candidates, name)
			}
		}
	}
	result := &ClassHierarchy{}
	names := []string{root}
	seen := map[string]bool{root: true}
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
		if len(candidates) == 0 {
			continue
		}
		source, namespace, err := namespaceBaseProbes(names[i], candidates)
		if err != nil {
			return nil, err
		}
		data, err := query.run(ctx, source, namespace)
		if err != nil {
			return nil, err
		}
		values, err := decodeDiscoveryValues(data, namespace, len(candidates))
		if err != nil {
			return nil, err
		}
		for j, name := range candidates {
			value, ok := values[fmt.Sprintf("base_%d", j)]
			if !ok {
				return nil, fmt.Errorf("cxx: missing hierarchy base probe %d", j)
			}
			if value && !seen[name] {
				if len(names) == maxDiscoveryTypes {
					return nil, fmt.Errorf("cxx: hierarchy exceeds %d selected names", maxDiscoveryTypes)
				}
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	result.Conversions, err = DiscoverBases(ctx, query.header, names, opts)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func namespaceBaseProbes(derived string, candidates []string) (source, namespace string, err error) {
	hash := sha256.Sum256([]byte(derived + "\x00" + strings.Join(candidates, "\x00")))
	namespace = fmt.Sprintf("dylib_go_namespace_bases_%x", hash[:8])
	var body strings.Builder
	fmt.Fprintf(&body, "namespace %s {\ntemplate<bool> struct Answer {};\n", namespace)
	for i, base := range candidates {
		fmt.Fprintf(&body, "using base_%d = Answer<%s>;\n", i, baseConversion("::"+derived, "::"+base))
	}
	body.WriteString("}\n")
	if body.Len() > maxDiscoverySource {
		return "", "", fmt.Errorf("cxx: hierarchy probes exceed source budget")
	}
	return body.String(), namespace, nil
}
