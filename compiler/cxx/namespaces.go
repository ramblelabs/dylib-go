package cxx

import (
	"context"
	"fmt"
	"strings"
)

// Resolve a namespace and its declarations within each filtered AST invocation.
// Namespace aliases can require a second filter; never reuse IDs across queries.
func discoverNamespace(ctx context.Context, header, namespace string, opts DiscoveryOptions) (*memberAST, *memberReference, *discoveryCompiler, error) {
	if len(namespace) > maxDiscoveryName || !qualifiedName(namespace) {
		return nil, nil, nil, fmt.Errorf("cxx: invalid discovery namespace %q", namespace)
	}
	query, err := discoveryCommand(ctx, header, opts)
	if err != nil {
		return nil, nil, nil, err
	}
	parts := strings.Split(namespace, "::")
	filter := parts[len(parts)-1]
	for attempt := 0; attempt < 2; attempt++ {
		source, probe := namespaceProbe(namespace, filter)
		data, err := query.run(ctx, source, filter)
		if err != nil {
			return nil, nil, nil, err
		}
		ast, err := decodeMemberAST(data, probe)
		if err != nil {
			return nil, nil, nil, err
		}
		ref, err := ast.selectedNamespace()
		if err != nil {
			return nil, nil, nil, err
		}
		for _, n := range ast.namespaces {
			if n.ID == ref.ID || n.OriginalNamespace != nil && n.OriginalNamespace.ID == ref.ID {
				return ast, ref, query, nil
			}
		}
		// A namespace alias can have a different name from its target. Repeat
		// with that name, resolving the probe and namespace IDs afresh together.
		if attempt != 0 || !identifier(ref.Name) || len(ref.Name) > maxDiscoveryName {
			return nil, nil, nil, fmt.Errorf("cxx: missing selected namespace definition")
		}
		filter = ref.Name
	}
	return nil, nil, nil, fmt.Errorf("cxx: missing selected namespace definition")
}
