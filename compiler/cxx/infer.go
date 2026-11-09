package cxx

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/internal/clangcmd"
)

// FunctionSelection names one unambiguous free/static function and its C export.
// TemplateArgs has Function.TemplateArgs' bounded syntax and nil/empty semantics.
// No native type spelling is interpreted as a Go ABI description.
type FunctionSelection struct {
	Name, Symbol string
	TemplateArgs []string
}

// InferredFunction reports the compiler-derived scalar/enum/address transports
// of one native function. Transport is NOT the status/output C export signature;
// parse Facade.CHeader for bindings. Pointer prototypes/layouts, enum value domains
// and ownership remain native API contracts, not inferred lifetime guarantees.
type InferredFunction struct {
	Selection FunctionSelection
	Transport abi.Signature
}

// InferredFacade contains independently owned generated buffers and selections.
type InferredFacade struct {
	Facade    *Facade
	Functions []InferredFunction
}

const maxInferredArguments = 32

// InferFunctions uses optional Clang C++17 queries to derive fixed scalar,
// enum-underlying and opaque-native-pointer transports for 1-256 unambiguous
// functions, then generates and compiler-checks a C facade. Each function has at
// most 32 arguments. Plain/typedef integer widths, signedness, bool, float/double,
// object/function pointers, noexcept and explicit template selections participate.
// Pointer addresses use the qualified producer's native conversion contract.
//
// Unsupported class/union values, references, extended numeric types, variadics,
// member pointers and ambiguous overloads fail without partial results. Native
// function-pointer extraction must match the compiler's default ABI. Use explicit
// manifests/native wrappers for other selections. No pointer ownership, retained
// callback lifetime, record layout or constructor/destructor policy is inferred.
//
// At most three compiler invocations are used: arity/result/pointer-width probes,
// argument probes, then generated-source validation. Existing header/source/AST
// IO, diagnostic and cancellation bounds apply. Files/environment must remain
// stable. Generated type/width guards reject incompatible producer flags/targets;
// calls still need a matching native SDK/runtime and valid native addresses.
func InferFunctions(ctx context.Context, header string, selections []FunctionSelection, opts DiscoveryOptions) (*InferredFacade, error) {
	selections, namespace, err := inferenceSelections(selections)
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
	source, err := inferenceProbes(selections, namespace, nil)
	if err != nil {
		return nil, err
	}
	data, err := query.run(ctx, source, namespace)
	if err != nil {
		return nil, err
	}
	widths, err := decodeInference(data, namespace, selections, nil)
	if err != nil {
		return nil, err
	}
	counts := make([]int, len(widths.Functions))
	for i, f := range widths.Functions {
		counts[i] = len(f.Transport.Args)
	}
	source, err = inferenceProbes(selections, namespace, counts)
	if err != nil {
		return nil, err
	}
	data, err = query.run(ctx, source, namespace)
	if err != nil {
		return nil, err
	}
	inferred, err := decodeInference(data, namespace, selections, counts)
	if err != nil {
		return nil, err
	}
	// Both invocations must describe the same target and result/arity contract.
	if inferred.pointerSize != widths.pointerSize {
		return nil, fmt.Errorf("cxx: inferred pointer width changed between queries")
	}
	for i, f := range inferred.Functions {
		if f.Transport.Result != widths.Functions[i].Transport.Result {
			return nil, fmt.Errorf("cxx: inferred result changed between queries")
		}
	}
	functions, preamble, err := inferenceWrappers(inferred, namespace)
	if err != nil {
		return nil, err
	}
	facade, err := generateFunctions(filepath.ToSlash(query.header), functions, preamble)
	if err != nil {
		return nil, err
	}
	if len(facade.CXXSource) > maxDiscoverySource {
		return nil, fmt.Errorf("cxx: inferred facade exceeds source budget")
	}
	// The generated source includes the original header exactly once, including
	// headers without guards. Do not add discoveryCommand.run's forced include.
	args := append([]string(nil), query.args...)
	args = append(args, "-x", "c++", "-std=c++17", "-fsyntax-only", "-")
	if _, err := clangcmd.Run(ctx, query.compiler, args, string(facade.CXXSource), maxDiscoveryAST, false); err != nil {
		return nil, fmt.Errorf("cxx: inferred facade: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &InferredFacade{Facade: facade, Functions: inferred.Functions}, nil
}

func inferenceSelections(input []FunctionSelection) ([]FunctionSelection, string, error) {
	if len(input) == 0 || len(input) > maxFacadeFunctions {
		return nil, "", fmt.Errorf("cxx: select 1-256 functions for inference")
	}
	result := make([]FunctionSelection, len(input))
	seen := map[string]bool{}
	for i, s := range input {
		if len(s.Name) > maxDiscoveryName || !qualifiedName(s.Name) || len(s.Symbol) > maxDiscoveryName || !identifier(s.Symbol) || seen[s.Symbol] {
			return nil, "", fmt.Errorf("cxx: invalid selection or duplicate inference export %q", s.Symbol)
		}
		if _, err := templateArguments(s.TemplateArgs); err != nil {
			return nil, "", fmt.Errorf("cxx: inferred %s: %w", s.Name, err)
		}
		seen[s.Symbol] = true
		if s.TemplateArgs != nil {
			s.TemplateArgs = append([]string{}, s.TemplateArgs...)
		}
		result[i] = s
	}
	encoded, _ := json.Marshal(result)
	hash := sha256.Sum256(encoded)
	return result, fmt.Sprintf("dylib_go_inferred_%x", hash[:8]), nil
}

func inferenceAliases(selections []FunctionSelection) string {
	var source strings.Builder
	source.WriteString(inferenceTraits())
	for i, s := range selections {
		template, _ := templateArguments(s.TemplateArgs)
		fmt.Fprintf(&source, "using P%d=Prototype<decltype(&::%s%s)>;\nstatic constexpr auto native_%d=&::%s%s;\n", i, s.Name, template, i, s.Name, template)
	}
	return source.String()
}

func inferenceProbes(selections []FunctionSelection, namespace string, counts []int) (string, error) {
	var source strings.Builder
	fmt.Fprintf(&source, "namespace %s {\ntemplate<int> struct Answer {};\nnamespace detail {\n%s}\nusing pointer_bytes=Answer<sizeof(void*)>;\n", namespace, inferenceAliases(selections))
	for i := range selections {
		fmt.Fprintf(&source, "using f%d_count=Answer<detail::P%d::count>;\nusing f%d_result=Answer<detail::code<typename detail::P%d::Result>()>;\n", i, i, i, i)
		if counts != nil {
			for j := 0; j < counts[i]; j++ {
				fmt.Fprintf(&source, "using f%d_arg%d=Answer<detail::code<typename detail::P%d::template Argument<%d>>() >;\n", i, j, i, j)
			}
		}
	}
	source.WriteString("}\n")
	if source.Len() > maxDiscoverySource {
		return "", fmt.Errorf("cxx: inference probes exceed source budget")
	}
	return source.String(), nil
}

type inferredDescriptions struct {
	Functions   []InferredFunction
	pointerSize int
}

func decodeInference(data []byte, namespace string, selections []FunctionSelection, counts []int) (*inferredDescriptions, error) {
	total := 1 + 2*len(selections)
	if counts != nil {
		for _, n := range counts {
			total += n
		}
	}
	values, err := decodeDiscoveryAnswers(data, namespace, total)
	if err != nil {
		return nil, err
	}
	number := func(name string) (int, error) {
		s, exists := values[name]
		n, err := strconv.Atoi(s)
		if !exists || err != nil || strconv.Itoa(n) != s {
			return 0, fmt.Errorf("cxx: invalid inferred probe %q", name)
		}
		return n, nil
	}
	size, err := number("pointer_bytes")
	if err != nil || size != 4 && size != 8 {
		return nil, fmt.Errorf("cxx: invalid inferred pointer width")
	}
	result := &inferredDescriptions{pointerSize: size}
	for i, s := range selections {
		n, err := number(fmt.Sprintf("f%d_count", i))
		if err != nil {
			return nil, err
		}
		if n < 0 || n > maxInferredArguments || counts != nil && n != counts[i] {
			return nil, fmt.Errorf("cxx: inferred %s requires at most 32 stable arguments", s.Name)
		}
		r, err := number(fmt.Sprintf("f%d_result", i))
		if err != nil {
			return nil, err
		}
		if !inferredType(r, true) {
			return nil, fmt.Errorf("cxx: unsupported inferred result for %s", s.Name)
		}
		f := InferredFunction{Selection: s, Transport: abi.Signature{Result: abi.Type(r), Args: make([]abi.Type, n)}}
		if counts != nil {
			for j := 0; j < n; j++ {
				k, err := number(fmt.Sprintf("f%d_arg%d", i, j))
				if err != nil {
					return nil, err
				}
				if !inferredType(k, false) {
					return nil, fmt.Errorf("cxx: unsupported inferred %s argument %d", s.Name, j)
				}
				f.Transport.Args[j] = abi.Type(k)
			}
		}
		result.Functions = append(result.Functions, f)
	}
	return result, nil
}

func inferredType(code int, result bool) bool {
	if code < 0 || code > int(abi.Bool) || !result && code == int(abi.Void) {
		return false
	}
	_, err := cType(abi.Type(code))
	return err == nil
}

func inferenceWrappers(inferred *inferredDescriptions, namespace string) ([]Function, string, error) {
	selections := make([]FunctionSelection, len(inferred.Functions))
	for i, f := range inferred.Functions {
		selections[i] = f.Selection
	}
	var source strings.Builder
	fmt.Fprintf(&source, "namespace %s {\nnamespace detail {\n%s}\nstatic_assert(sizeof(void*)==%d,\"inferred target pointer width changed\");\n", namespace, inferenceAliases(selections), inferred.pointerSize)
	functions := make([]Function, len(selections))
	for i, f := range inferred.Functions {
		signature := f.Transport
		rtype, err := cType(signature.Result)
		if err != nil {
			return nil, "", err
		}
		parameters, _, _, err := arguments(signature.Args)
		if err != nil {
			return nil, "", err
		}
		fmt.Fprintf(&source, "static_assert(detail::P%d::count==%d,\"inferred arity changed\");\nstatic_assert(detail::code<typename detail::P%d::Result>()==%d,\"inferred result changed\");\n", i, len(signature.Args), i, signature.Result)
		var values []string
		for j, t := range signature.Args {
			fmt.Fprintf(&source, "static_assert(detail::code<typename detail::P%d::template Argument<%d>>()==%d,\"inferred argument changed\");\n", i, j, t)
			cast := "static_cast"
			if t == abi.Pointer {
				cast = "reinterpret_cast"
			}
			values = append(values, fmt.Sprintf("%s<typename detail::P%d::template Argument<%d>>(p%d)", cast, i, j, j))
		}
		call := fmt.Sprintf("detail::native_%d(%s)", i, strings.Join(values, ", "))
		body := call + ";"
		if signature.Result == abi.Pointer {
			body = "return const_cast<void*>(reinterpret_cast<const volatile void*>(" + call + "));"
		} else if signature.Result != abi.Void {
			body = "return static_cast<" + rtype + ">(" + call + ");"
		}
		fmt.Fprintf(&source, "static %s call_%d(%s) { %s }\n", rtype, i, strings.Join(parameters, ", "), body)
		functions[i] = Function{Name: fmt.Sprintf("%s::call_%d", namespace, i), Symbol: f.Selection.Symbol, Signature: signature}
	}
	source.WriteString("}\n")
	if source.Len() > maxDiscoverySource {
		return nil, "", fmt.Errorf("cxx: inferred wrappers exceed source budget")
	}
	return functions, source.String(), nil
}
