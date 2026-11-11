package cxx

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ramblelabs/dylib-go/abi"
)

// ConstructorSelection names an allocated native type, a C export, and the
// native argument expression types passed to its new-expression. ArgumentTypes
// uses FunctionOverload.ParameterTypes' bounded names/header aliases. These are
// argument types, not an extracted constructor parameter list: normal C++
// overload resolution, conversions, defaults and template deduction apply.
type ConstructorSelection struct {
	Class, Symbol string
	ArgumentTypes []string
}

// InferredConstructor describes scalar/enum/address argument carriers and a
// pointer allocation result. Transport is NOT the status/output C export ABI;
// parse Facade.CHeader for bindings. Destruction and ownership remain explicit.
type InferredConstructor struct {
	Selection ConstructorSelection
	Transport abi.Signature
}

// InferredConstructorFacade owns generated buffers and selection snapshots.
type InferredConstructorFacade struct {
	Facade       *Facade
	Constructors []InferredConstructor
}

// InferConstructors uses optional Clang C++17 queries to generate 1-256 selected
// allocation exports with at most 32 scalar, enum or opaque pointer arguments.
// Native casts restore each argument's type before constructor selection. The
// ordinary constructor facade preserves occupied output slots, catches native
// exceptions and publishes a non-null allocation only on success.
//
// Class/union values, references, extended numerics and unsupported allocations
// fail without partial output. Pointer prototypes, lifetimes and valid addresses
// remain native contracts. Add destruction exports through an explicit manifest;
// neither layout nor an ownership policy is inferred. Target/flags must match
// the producer, including its C++ runtime and SDK.
//
// One bounded compiler query derives transports; a second validates generated
// new-expressions and source. DiscoveryOptions' IO, cancellation and
// diagnostic limits apply. Files/environment must remain stable. Generated
// pointer-width and transport guards reject incompatible producers.
func InferConstructors(ctx context.Context, header string, input []ConstructorSelection, opts DiscoveryOptions) (*InferredConstructorFacade, error) {
	selections, namespace, err := constructorSelections(input)
	if err != nil {
		return nil, err
	}
	query, err := discoveryCommand(ctx, header, opts)
	if err != nil {
		return nil, err
	}
	query.compiler, err = filepath.Abs(query.compiler)
	if err != nil {
		return nil, err
	}
	aliases := constructorAliases(selections)
	var source strings.Builder
	fmt.Fprintf(&source, "namespace %s {\ntemplate<int> struct Answer {};\nnamespace detail {\n%s}\nusing pointer_bytes=Answer<sizeof(void*)>;\n", namespace, aliases)
	functions := make([]FunctionSelection, len(selections))
	counts := make([]int, len(selections))
	for i, s := range selections {
		functions[i] = FunctionSelection{Name: s.Class, Symbol: s.Symbol}
		counts[i] = len(s.ArgumentTypes)
		fmt.Fprintf(&source, "using f%d_count=Answer<detail::P%d::count>;\nusing f%d_result=Answer<detail::code<typename detail::P%d::Result>()>;\n", i, i, i, i)
		for j := range s.ArgumentTypes {
			fmt.Fprintf(&source, "using f%d_arg%d=Answer<detail::code<typename detail::P%d::template Argument<%d>>() >;\n", i, j, i, j)
		}
	}
	source.WriteString("}\n")
	if source.Len() > maxDiscoverySource {
		return nil, fmt.Errorf("cxx: constructor probes exceed source budget")
	}
	data, err := query.run(ctx, source.String(), namespace)
	if err != nil {
		return nil, err
	}
	inferred, err := decodeInference(data, namespace, functions, counts, nil)
	if err != nil {
		return nil, err
	}
	source.Reset()
	fmt.Fprintf(&source, "namespace %s {\nnamespace detail {\n%s}\nstatic_assert(sizeof(void*)==%d,\"inferred pointer width changed\");\n", namespace, aliases, inferred.pointerSize)
	classes := make([]Class, len(selections))
	arguments := make(map[string]string, len(selections))
	result := &InferredConstructorFacade{Constructors: make([]InferredConstructor, len(selections))}
	for i, f := range inferred.Functions {
		if f.Transport.Result != abi.Pointer {
			return nil, fmt.Errorf("cxx: inferred allocation requires a pointer result")
		}
		s := selections[i]
		result.Constructors[i] = InferredConstructor{Selection: s, Transport: f.Transport}
		classes[i] = Class{Name: s.Class, Constructors: []Constructor{{Symbol: s.Symbol, Args: f.Transport.Args}}}
		fmt.Fprintf(&source, "static_assert(detail::P%d::count==%d,\"inferred arity changed\");\nstatic_assert(detail::code<typename detail::P%d::Result>()==%d,\"inferred result changed\");\n", i, len(f.Transport.Args), i, f.Transport.Result)
		var values []string
		for j, t := range f.Transport.Args {
			fmt.Fprintf(&source, "static_assert(detail::code<typename detail::P%d::template Argument<%d>>()==%d,\"inferred argument changed\");\n", i, j, t)
			cast := "static_cast"
			if t == abi.Pointer {
				cast = "reinterpret_cast"
			}
			values = append(values, fmt.Sprintf("%s<typename ::%s::detail::P%d::template Argument<%d>>(p%d)", cast, namespace, i, j, j))
		}
		arguments[s.Symbol] = strings.Join(values, ", ")
	}
	source.WriteString("}\n")
	result.Facade, err = generate(filepath.ToSlash(query.header), classes, facadeOptions{preamble: source.String(), constructorArguments: arguments})
	if err != nil {
		return nil, err
	}
	if err := validateInferredFacade(ctx, query, result.Facade); err != nil {
		return nil, err
	}
	return result, nil
}

func constructorSelections(input []ConstructorSelection) ([]ConstructorSelection, string, error) {
	if len(input) == 0 || len(input) > maxFacadeFunctions {
		return nil, "", fmt.Errorf("cxx: select 1-256 constructors for inference")
	}
	result := make([]ConstructorSelection, len(input))
	seen := map[string]bool{}
	for i, s := range input {
		if len(s.Class) > maxDiscoveryName || !qualifiedName(s.Class) {
			return nil, "", fmt.Errorf("cxx: invalid inferred allocation type %q", s.Class)
		}
		if len(s.Symbol) > maxDiscoveryName || !identifier(s.Symbol) || seen[s.Symbol] {
			return nil, "", fmt.Errorf("cxx: invalid or duplicate constructor export %q", s.Symbol)
		}
		args, err := snapshotFunctionOverload(&FunctionOverload{ParameterTypes: s.ArgumentTypes})
		if err != nil {
			return nil, "", fmt.Errorf("cxx: inferred constructor %s: %w", s.Class, err)
		}
		s.ArgumentTypes = args.ParameterTypes
		seen[s.Symbol] = true
		result[i] = s
	}
	encoded, _ := json.Marshal(result)
	hash := sha256.Sum256(encoded)
	return result, fmt.Sprintf("dylib_go_inferred_constructors_%x", hash[:8]), nil
}

func constructorAliases(selections []ConstructorSelection) string {
	var source strings.Builder
	source.WriteString(inferenceTraits())
	for i, s := range selections {
		types := overloadTypes(s.ArgumentTypes)
		for _, t := range types {
			// Check before function-parameter adjustment could turn an array or
			// function alias into an otherwise supported opaque pointer.
			fmt.Fprintf(&source, "static_assert(code<%s>()>=0 && code<%s>()!=%d,\"unsupported constructor argument\");\n", t, t, abi.Void)
		}
		// The actual new-expression is checked in generated source. A synthetic
		// prototype normalizes top-level parameter cv for the scalar/pointer
		// prvalues made by its casts, without inventing a constructor prototype.
		fmt.Fprintf(&source, "using P%d=Prototype<::%s*(*)(%s)>;\n", i, s.Class, strings.Join(types, ", "))
	}
	return source.String()
}
