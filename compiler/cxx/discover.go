package cxx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ramblelabs/dylib-go/compiler/internal/clangcmd"
)

// DiscoveryOptions selects an optional Clang-compatible C++ compiler. Target
// defaults to that compiler's target, independently of Go's host architecture.
// Target and preprocessing Flags must match the native facade producer.
type DiscoveryOptions struct {
	Compiler string // Executable path/name, default clang++; never a shell command.
	Target   string
	Flags    []string
}

// BaseRelation names a public, unambiguous base conversion among selected types.
// It includes transitive and virtual bases. Canonical self aliases are excluded.
// Assign an export Symbol when adding Base to the derived Class.Bases manifest;
// Generate delegates all native pointer adjustments to the C++ compiler.
type BaseRelation struct {
	Derived, Base string
}

const (
	maxDiscoveryTypes  = 32
	maxDiscoveryName   = 256
	maxDiscoveryHeader = 16 << 20
	maxDiscoverySource = 1 << 20
	maxDiscoveryAST    = 32 << 20
)

// DiscoverBases queries inheritance among 1-32 explicit accessible C++ type
// names/aliases, each at most 256 bytes. Names must select complete non-cv class
// types. The optional external compiler reads the header at its original path,
// preserving relative includes and preprocessing flags. This neither discovers
// declarations nor infers class layouts, runtime ownership or export names.
// Results follow the requested derived/base order. Failures return no results.
func DiscoverBases(ctx context.Context, header string, names []string, opts DiscoveryOptions) ([]BaseRelation, error) {
	if ctx == nil {
		return nil, fmt.Errorf("cxx: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names = append([]string(nil), names...)
	if len(names) == 0 || len(names) > maxDiscoveryTypes {
		return nil, fmt.Errorf("cxx: select 1-%d explicit class names", maxDiscoveryTypes)
	}
	seen := make(map[string]bool)
	for _, name := range names {
		if len(name) > maxDiscoveryName || !qualifiedName(name) || seen[name] {
			return nil, fmt.Errorf("cxx: invalid or duplicate class name %q", name)
		}
		seen[name] = true
	}
	query, err := discoveryCommand(ctx, header, opts)
	if err != nil {
		return nil, err
	}
	source, namespace, probes, err := baseProbes(names)
	if err != nil {
		return nil, err
	}
	data, err := query.run(ctx, source, namespace)
	if err != nil {
		return nil, err
	}
	return decodeBases(data, namespace, names, probes)
}

type discoveryCompiler struct {
	compiler, header string
	args             []string
}

func discoveryCommand(ctx context.Context, header string, opts DiscoveryOptions) (*discoveryCompiler, error) {
	if ctx == nil {
		return nil, fmt.Errorf("cxx: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	header, err := filepath.Abs(header)
	if err != nil {
		return nil, err
	}
	stat, err := os.Stat(header)
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > maxDiscoveryHeader {
		return nil, fmt.Errorf("cxx: expected a regular header of at most %d bytes", maxDiscoveryHeader)
	}
	compiler := opts.Compiler
	if compiler == "" {
		compiler = "clang++"
	}
	compiler, err = exec.LookPath(compiler)
	if err != nil {
		return nil, err
	}
	args := append([]string(nil), opts.Flags...)
	if opts.Target != "" {
		args = append(args, "--target="+opts.Target)
	}
	return &discoveryCompiler{compiler: compiler, header: header, args: args}, nil
}

func (q *discoveryCompiler) run(ctx context.Context, source, filter string) ([]byte, error) {
	args := append([]string(nil), q.args...)
	args = append(args, "-x", "c++", "-std=c++17", "-include", q.header, "-Xclang", "-ast-dump=json", "-Xclang", "-ast-dump-filter="+filter, "-fsyntax-only", "-")
	data, err := clangcmd.Run(ctx, q.compiler, args, source, maxDiscoveryAST, false)
	if err != nil {
		return nil, fmt.Errorf("cxx: %w", err)
	}
	return data, nil
}

type baseProbe struct {
	name    string
	derived int
	base    int // -1 selects class validity, rather than a base relation.
}

func baseProbes(names []string) (source, namespace string, probes []baseProbe, err error) {
	hash := sha256.Sum256([]byte(strings.Join(names, "\x00")))
	namespace = fmt.Sprintf("dylib_go_base_query_%x", hash[:8])
	var body strings.Builder
	fmt.Fprintf(&body, "namespace %s {\ntemplate<bool> struct Answer {};\n", namespace)
	for i, name := range names {
		name = "::" + name
		alias := fmt.Sprintf("valid_%d", i)
		fmt.Fprintf(&body, "using %s = Answer<__is_class(%s) && __is_convertible_to(%s *, void *) && (sizeof(%s) > 0)>;\n", alias, name, name, name)
		probes = append(probes, baseProbe{alias, i, -1})
	}
	for i, derived := range names {
		derived = "::" + derived
		for j, base := range names {
			base = "::" + base
			if i == j {
				continue
			}
			alias := fmt.Sprintf("pair_%d_%d", i, j)
			fmt.Fprintf(&body, "using %s = Answer<%s>;\n", alias, baseConversion(derived, base))
			probes = append(probes, baseProbe{alias, i, j})
		}
	}
	body.WriteString("}\n")
	if body.Len() > maxDiscoverySource {
		return "", "", nil, fmt.Errorf("cxx: base probes exceed source budget")
	}
	return body.String(), namespace, probes, nil
}

// Both selected-name and namespace-catalog queries require the same strict,
// public, unambiguous conversion. Inputs are validated qualified C++ names.
func baseConversion(derived, base string) string {
	return fmt.Sprintf("!__is_same(%s, %s) && __is_base_of(%s, %s) && __is_convertible_to(%s *, %s *)", derived, base, base, derived, derived, base)
}

func decodeBases(data []byte, namespace string, names []string, probes []baseProbe) ([]BaseRelation, error) {
	values, err := decodeDiscoveryValues(data, namespace, len(probes))
	if err != nil {
		return nil, err
	}
	var result []BaseRelation
	for _, probe := range probes {
		value, exists := values[probe.name]
		if !exists {
			return nil, fmt.Errorf("cxx: missing inheritance probe %q", probe.name)
		}
		if probe.base < 0 {
			if !value {
				return nil, fmt.Errorf("cxx: %s must name a complete non-cv class type", names[probe.derived])
			}
		} else if value {
			result = append(result, BaseRelation{Derived: names[probe.derived], Base: names[probe.base]})
		}
	}
	return result, nil
}

// Both inheritance and class discovery use exact compiler Boolean answers.
func decodeDiscoveryValues(data []byte, namespace string, count int) (map[string]bool, error) {
	answers, err := decodeDiscoveryAnswers(data, namespace, count)
	if err != nil {
		return nil, err
	}
	values := make(map[string]bool, len(answers))
	for name, answer := range answers {
		switch answer {
		case "true":
			values[name] = true
		case "false":
			values[name] = false
		default:
			return nil, fmt.Errorf("cxx: unrecognized discovery probe %q", name)
		}
	}
	return values, nil
}

// Both Boolean discovery and scalar inference require exact named Answer values,
// one namespace root, unique aliases and the expected probe count.
func decodeDiscoveryAnswers(data []byte, namespace string, count int) (map[string]string, error) {
	if err := preflightDiscoveryAST(data); err != nil {
		return nil, err
	}
	var ast struct {
		Kind, Name string
		Inner      []struct {
			Kind, Name string
			Type       struct{ DesugaredQualType string }
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&ast); err != nil {
		return nil, fmt.Errorf("cxx: invalid discovery AST: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF || ast.Kind != "NamespaceDecl" || ast.Name != namespace {
		return nil, fmt.Errorf("cxx: unexpected discovery AST root or trailing data")
	}
	values := make(map[string]string)
	for _, node := range ast.Inner {
		if node.Kind != "TypeAliasDecl" {
			continue
		}
		if _, duplicate := values[node.Name]; duplicate {
			return nil, fmt.Errorf("cxx: duplicate discovery probe %q", node.Name)
		}
		prefix := namespace + "::Answer<"
		value, ok := strings.CutPrefix(node.Type.DesugaredQualType, prefix)
		if !ok || !strings.HasSuffix(value, ">") {
			return nil, fmt.Errorf("cxx: unrecognized discovery probe %q", node.Name)
		}
		value = strings.TrimSuffix(value, ">")
		if value == "" || strings.ContainsAny(value, "<>") {
			return nil, fmt.Errorf("cxx: unrecognized discovery probe %q", node.Name)
		}
		values[node.Name] = value
	}
	if len(values) != count {
		return nil, fmt.Errorf("cxx: unexpected discovery probe count")
	}
	return values, nil
}
