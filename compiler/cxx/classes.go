package cxx

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
)

const maxDiscoveredClasses = 256

// DiscoverClasses finds accessible complete non-cv class names and aliases in
// one explicit namespace, including named child namespaces and public nested
// types. Namespace aliases are accepted. Results retain their first declaration
// order and use the caller's namespace spelling. Included headers participate.
// Anonymous namespaces, local types, class templates without a selected alias,
// inherited names and using-declaration/directive imports are excluded.
//
// Discovery uses at most two filtered Clang AST queries and one compiler
// eligibility query, with at most 256 candidate names and the same name, IO and
// cancellation bounds as DiscoverBases. Exceeding a bound returns no results.
// The caller selects returned names for DiscoverMembers and chooses ABI,
// ownership and export names for Generate; discovery does not infer them.
func DiscoverClasses(ctx context.Context, header, namespace string, opts DiscoveryOptions) ([]string, error) {
	if len(namespace) > maxDiscoveryName || !qualifiedName(namespace) {
		return nil, fmt.Errorf("cxx: invalid discovery namespace %q", namespace)
	}
	query, err := discoveryCommand(ctx, header, opts)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(namespace, "::")
	filter := parts[len(parts)-1]
	var candidates []string
	for attempt := 0; attempt < 2; attempt++ {
		source, probe := namespaceProbe(namespace, filter)
		data, err := query.run(ctx, source, filter)
		if err != nil {
			return nil, err
		}
		ast, err := decodeMemberAST(data, probe)
		if err != nil {
			return nil, err
		}
		ref, err := ast.selectedNamespace()
		if err != nil {
			return nil, err
		}
		var found bool
		candidates, found, err = ast.classCandidates(namespace, ref.ID)
		if err != nil {
			return nil, err
		}
		if found {
			break
		}
		// A namespace alias can have a different name from its target. Repeat
		// with that name, resolving the probe and namespace IDs afresh together.
		if attempt != 0 || !identifier(ref.Name) || len(ref.Name) > maxDiscoveryName {
			return nil, fmt.Errorf("cxx: missing selected namespace definition")
		}
		filter = ref.Name
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	source, probe := classProbes(candidates)
	data, err := query.run(ctx, source, probe)
	if err != nil {
		return nil, err
	}
	values, err := decodeDiscoveryValues(data, probe, len(candidates))
	if err != nil {
		return nil, err
	}
	var result []string
	for i, name := range candidates {
		key := fmt.Sprintf("class_%d", i)
		value, exists := values[key]
		if !exists {
			return nil, fmt.Errorf("cxx: missing class probe %q", key)
		}
		if value {
			result = append(result, name)
		}
	}
	return result, nil
}

func namespaceProbe(namespace, filter string) (source, probe string) {
	hash := sha256.Sum256([]byte(namespace))
	probe = fmt.Sprintf("dylib_go_classes_%s_%x", filter, hash[:8])
	return fmt.Sprintf("namespace %s { using namespace ::%s; }\n", probe, namespace), probe
}

func (ast *memberAST) selectedNamespace() (*memberReference, error) {
	var ref *memberReference
	for _, n := range ast.probe.Inner {
		if n.Kind == "UsingDirectiveDecl" {
			if ref != nil || n.NominatedNamespace == nil {
				return nil, fmt.Errorf("cxx: invalid namespace discovery probe")
			}
			ref = n.NominatedNamespace
		}
	}
	if ref == nil || ref.ID == "" || ref.Kind != "NamespaceDecl" {
		return nil, fmt.Errorf("cxx: missing selected namespace reference")
	}
	return ref, nil
}

func (ast *memberAST) classCandidates(namespace, id string) ([]string, bool, error) {
	var names []string
	seen := make(map[string]bool)
	add := func(name string) error {
		if len(name) > maxDiscoveryName || !qualifiedName(name) {
			return fmt.Errorf("cxx: discovered name exceeds the qualified-name budget")
		}
		if !seen[name] {
			if len(names) == maxDiscoveredClasses {
				return fmt.Errorf("cxx: namespace exceeds %d candidate classes", maxDiscoveredClasses)
			}
			seen[name] = true
			names = append(names, name)
		}
		return nil
	}
	var walk func([]memberNode, string, string) error
	walk = func(nodes []memberNode, prefix, access string) error {
		for _, n := range nodes {
			if n.Kind == "AccessSpecDecl" {
				if n.Access != "public" && n.Access != "protected" && n.Access != "private" {
					return fmt.Errorf("cxx: invalid discovered member access")
				}
				access = n.Access
				continue
			}
			if access != "public" || n.IsImplicit {
				continue
			}
			if n.Kind == "LinkageSpecDecl" {
				if err := walk(n.Inner, prefix, access); err != nil {
					return err
				}
				continue
			}
			if n.Name == "" {
				continue
			}
			name := prefix + "::" + n.Name
			switch n.Kind {
			case "NamespaceDecl":
				if err := walk(n.Inner, name, "public"); err != nil {
					return err
				}
			case "CXXRecordDecl":
				if n.TagUsed != "class" && n.TagUsed != "struct" && n.TagUsed != "union" {
					return fmt.Errorf("cxx: invalid discovered record kind")
				}
				if err := add(name); err != nil {
					return err
				}
				memberAccess := "public"
				if n.TagUsed == "class" {
					memberAccess = "private"
				}
				if err := walk(n.Inner, name, memberAccess); err != nil {
					return err
				}
			case "TypeAliasDecl", "TypedefDecl":
				if err := add(name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	found := false
	for _, n := range ast.namespaces {
		if n.ID == id || n.OriginalNamespace != nil && n.OriginalNamespace.ID == id {
			found = true
			if err := walk(n.Inner, namespace, "public"); err != nil {
				return nil, false, err
			}
		}
	}
	return names, found, nil
}

func classProbes(names []string) (source, namespace string) {
	hash := sha256.Sum256([]byte(strings.Join(names, "\x00")))
	namespace = fmt.Sprintf("dylib_go_class_types_%x", hash[:8])
	var body strings.Builder
	fmt.Fprintf(&body, "namespace %s {\ntemplate<bool> struct Answer {};\n", namespace)
	body.WriteString("template<class T, bool = __is_class(T), class = void> struct Eligible { static constexpr bool value = false; };\n")
	body.WriteString("template<class T> struct Eligible<T, true, decltype(void(sizeof(T)), void(static_cast<void *>(static_cast<T *>(nullptr))))> { static constexpr bool value = true; };\n")
	for i, name := range names {
		probe := fmt.Sprintf("class_%d", i)
		fmt.Fprintf(&body, "using %s = Answer<Eligible<::%s>::value>;\n", probe, name)
	}
	body.WriteString("}\n")
	return body.String(), namespace
}
