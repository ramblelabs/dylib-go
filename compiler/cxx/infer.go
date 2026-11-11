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

// FunctionSelection names a free/static function and its C export.
// TemplateArgs has Function.TemplateArgs' bounded syntax and nil/empty semantics.
// Overload selects native parameter types for compiler overload resolution.
type FunctionSelection struct {
	Name, Symbol string
	TemplateArgs []string
	// Overload selects native parameter types when resolving an overload set.
	Overload *FunctionOverload `json:",omitempty"`
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
// functions, then generates and compiler-checks a C facade. Overload can select
// native parameter types within an overload set. Each function has at
// most 32 arguments. Plain/typedef integer widths, signedness, bool, float/double,
// object/function pointers, noexcept and explicit template selections participate.
// Pointer addresses use the qualified producer's native conversion contract.
//
// Unsupported class/union values, references, extended numeric types, variadics,
// member pointers and unresolved overloads fail without partial results. Native
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
	result, err := inferSelected(ctx, header, selections, nil, namespace, opts)
	if err != nil {
		return nil, err
	}
	return &InferredFacade{Facade: result.facade, Functions: result.Functions}, nil
}

// inferSelected shares all compiler probes, bounds, transport decoding, source
// generation and final validation between functions and non-static methods.
func inferSelected(ctx context.Context, header string, selections []FunctionSelection, methods []MethodSelection, namespace string, opts DiscoveryOptions) (*inferredDescriptions, error) {
	query, err := discoveryCommand(ctx, header, opts)
	if err != nil {
		return nil, err
	}
	query.compiler, err = filepath.Abs(query.compiler)
	if err != nil {
		return nil, err
	}
	source, err := inferenceProbes(selections, namespace, nil, methods)
	if err != nil {
		return nil, err
	}
	data, err := query.run(ctx, source, namespace)
	if err != nil {
		return nil, err
	}
	widths, err := decodeInference(data, namespace, selections, nil, methods)
	if err != nil {
		return nil, err
	}
	counts := make([]int, len(widths.Functions))
	for i, f := range widths.Functions {
		counts[i] = len(f.Transport.Args)
	}
	source, err = inferenceProbes(selections, namespace, counts, methods)
	if err != nil {
		return nil, err
	}
	data, err = query.run(ctx, source, namespace)
	if err != nil {
		return nil, err
	}
	inferred, err := decodeInference(data, namespace, selections, counts, methods)
	if err != nil {
		return nil, err
	}
	// Both invocations must describe the same target and result/arity contract.
	if inferred.pointerSize != widths.pointerSize {
		return nil, fmt.Errorf("cxx: inferred pointer width changed between queries")
	}
	for i, f := range inferred.Functions {
		if methods != nil && inferred.qualifiers[i] != widths.qualifiers[i] {
			return nil, fmt.Errorf("cxx: inferred receiver qualifiers changed between queries")
		}
		if f.Transport.Result != widths.Functions[i].Transport.Result {
			return nil, fmt.Errorf("cxx: inferred result changed between queries")
		}
	}
	functions, preamble, err := inferenceWrappers(inferred, namespace, methods)
	if err != nil {
		return nil, err
	}
	facade, err := generateFunctions(filepath.ToSlash(query.header), functions, facadeOptions{preamble: preamble, nonnullReceiver: methods != nil})
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
	inferred.facade = facade
	return inferred, nil
}

func inferenceSelections(input []FunctionSelection) ([]FunctionSelection, string, error) {
	if len(input) == 0 || len(input) > maxFacadeFunctions {
		return nil, "", fmt.Errorf("cxx: select 1-256 functions for inference")
	}
	result := make([]FunctionSelection, len(input))
	seen := map[string]bool{}
	for i, s := range input {
		if len(s.Name) > maxDiscoveryName || !qualifiedName(s.Name) {
			return nil, "", fmt.Errorf("cxx: invalid inferred function name %q", s.Name)
		}
		selected, err := snapshotInferenceExport(s, seen)
		if err != nil {
			return nil, "", err
		}
		result[i] = selected
	}
	encoded, _ := json.Marshal(result)
	hash := sha256.Sum256(encoded)
	return result, fmt.Sprintf("dylib_go_inferred_%x", hash[:8]), nil
}

// Shared export/template validation and snapshots apply to both selectors.
func snapshotInferenceExport(s FunctionSelection, seen map[string]bool) (FunctionSelection, error) {
	if len(s.Symbol) > maxDiscoveryName || !identifier(s.Symbol) || seen[s.Symbol] {
		return FunctionSelection{}, fmt.Errorf("cxx: invalid selection or duplicate inference export %q", s.Symbol)
	}
	if _, err := templateArguments(s.TemplateArgs); err != nil {
		return FunctionSelection{}, fmt.Errorf("cxx: inferred %s: %w", s.Name, err)
	}
	if s.Overload != nil {
		selected, err := snapshotFunctionOverload(s.Overload)
		if err != nil {
			return FunctionSelection{}, fmt.Errorf("cxx: inferred %s overload: %w", s.Name, err)
		}
		s.Overload = selected
	}
	seen[s.Symbol] = true
	if s.TemplateArgs != nil {
		s.TemplateArgs = append([]string{}, s.TemplateArgs...)
	}
	return s, nil
}

func inferenceAliases(selections []FunctionSelection, methods []MethodSelection) string {
	var source strings.Builder
	overloaded := false
	for i, s := range selections {
		if s.Overload != nil || methods != nil && methods[i].Overload != nil {
			overloaded = true
			break
		}
	}
	source.WriteString(inferenceTraits())
	if overloaded {
		source.WriteString(inferenceOverloadTraits)
	}
	if methods != nil {
		source.WriteString(memberInferenceTraits(overloaded))
	}
	for i, s := range selections {
		template, _ := templateArguments(s.TemplateArgs)
		if methods == nil {
			selector := "&::" + s.Name + template
			if s.Overload != nil {
				types := overloadTypes(s.Overload.ParameterTypes)
				result := fmt.Sprintf("R%d", i)
				call := "::" + s.Name + template + "(" + overloadValues(types) + ")"
				fmt.Fprintf(&source, "using %s=decltype(%s);\n", result, call)
				args := append([]string{result}, types...)
				selector = "ChooseFunction" + overloadArguments(args) + "::pick(" + selector + ")"
			}
			fmt.Fprintf(&source, "using P%d=Prototype<decltype(%s)>;\nstatic constexpr auto native_%d=%s;\n", i, selector, i, selector)
		} else {
			fmt.Fprintf(&source, "using C%d=::%s;\n", i, methods[i].Class)
			name := inferredMethodName(methods[i])
			selector := fmt.Sprintf("&C%d::%s%s", i, name, template)
			if choice := methods[i].Overload; choice != nil {
				types := overloadTypes(choice.ParameterTypes)
				class := fmt.Sprintf("C%d", i)
				receiver := cvQualified(class, choice.Const, choice.Volatile) + "&"
				if choice.Ref == RefRValue {
					receiver += "&"
				}
				call := "argument<" + receiver + ">()." + name + template + "(" + overloadValues(types) + ")"
				result := fmt.Sprintf("R%d", i)
				resultType := "decltype(" + call + ")"
				if methods[i].ConversionType != "" {
					// The conversion target also preserves top-level result cv,
					// which scalar prvalue call expressions would discard.
					resultType = overloadTypes([]string{methods[i].ConversionType})[0]
				}
				fmt.Fprintf(&source, "using %s=%s;\n", result, resultType)
				args := []string{fmt.Sprint(methodOverloadQualifiers(choice)), class, result}
				args = append(args, types...)
				selector = "ChooseMember" + overloadArguments(args) + "::pick(" + selector + ")"
			}
			fmt.Fprintf(&source, "using P%d=Member<decltype(%s)>;\nstatic constexpr auto native_%d=%s;\n", i, selector, i, selector)
		}
	}
	return source.String()
}

func inferenceProbes(selections []FunctionSelection, namespace string, counts []int, methods []MethodSelection) (string, error) {
	var source strings.Builder
	fmt.Fprintf(&source, "namespace %s {\ntemplate<int> struct Answer {};\nnamespace detail {\n%s}\nusing pointer_bytes=Answer<sizeof(void*)>;\n", namespace, inferenceAliases(selections, methods))
	for i := range selections {
		if methods != nil {
			fmt.Fprintf(&source, "using f%d_receiver=Answer<detail::P%d::qualifiers>;\n", i, i)
		}
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
	qualifiers  []int
	facade      *Facade
}

func decodeInference(data []byte, namespace string, selections []FunctionSelection, counts []int, methods []MethodSelection) (*inferredDescriptions, error) {
	total := 1 + 2*len(selections)
	if methods != nil {
		total += len(selections)
	}
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
		if methods != nil {
			q, err := number(fmt.Sprintf("f%d_receiver", i))
			if err != nil || q < 0 || q >= 2*inferredReceiverForms {
				return nil, fmt.Errorf("cxx: invalid inferred receiver qualifiers for %s", s.Name)
			}
			result.qualifiers = append(result.qualifiers, q)
		}
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
		if !inferredType(r, true) || methods != nil && methods[i].ConversionType != "" && r == int(abi.Void) {
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

func inferenceWrappers(inferred *inferredDescriptions, namespace string, methods []MethodSelection) ([]Function, string, error) {
	selections := make([]FunctionSelection, len(inferred.Functions))
	for i, f := range inferred.Functions {
		selections[i] = f.Selection
	}
	var source strings.Builder
	fmt.Fprintf(&source, "namespace %s {\nnamespace detail {\n%s}\nstatic_assert(sizeof(void*)==%d,\"inferred target pointer width changed\");\n", namespace, inferenceAliases(selections, methods), inferred.pointerSize)
	functions := make([]Function, len(selections))
	for i, f := range inferred.Functions {
		signature := f.Transport
		offset := 0
		carrier := signature
		if methods != nil {
			offset = 1
			carrier.Args = append([]abi.Type{abi.Pointer}, signature.Args...)
			fmt.Fprintf(&source, "static_assert(detail::P%d::qualifiers==%d,\"inferred receiver qualifiers changed\");\n", i, inferred.qualifiers[i])
		}
		rtype, err := cType(signature.Result)
		if err != nil {
			return nil, "", err
		}
		parameters, _, _, err := arguments(carrier.Args)
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
			values = append(values, fmt.Sprintf("%s<typename detail::P%d::template Argument<%d>>(p%d)", cast, i, j, j+offset))
		}
		call := fmt.Sprintf("detail::native_%d(%s)", i, strings.Join(values, ", "))
		if methods != nil {
			call = fmt.Sprintf("(static_cast<typename detail::P%d::template Receiver<detail::C%d>>(*static_cast<detail::C%d*>(p0)).*detail::native_%d)(%s)", i, i, i, i, strings.Join(values, ", "))
		}
		body := call + ";"
		if signature.Result == abi.Pointer {
			body = "return const_cast<void*>(reinterpret_cast<const volatile void*>(" + call + "));"
		} else if signature.Result != abi.Void {
			body = "return static_cast<" + rtype + ">(" + call + ");"
		}
		fmt.Fprintf(&source, "static %s call_%d(%s) { %s }\n", rtype, i, strings.Join(parameters, ", "), body)
		functions[i] = Function{Name: fmt.Sprintf("%s::call_%d", namespace, i), Symbol: f.Selection.Symbol, Signature: carrier}
	}
	source.WriteString("}\n")
	if source.Len() > maxDiscoverySource {
		return nil, "", fmt.Errorf("cxx: inferred wrappers exceed source budget")
	}
	return functions, source.String(), nil
}
