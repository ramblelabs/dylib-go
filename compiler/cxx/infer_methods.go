package cxx

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ramblelabs/dylib-go/abi"
)

// MethodSelection names a non-static member of a qualified class
// or class alias. Name accepts an identifier or operator(). TemplateArgs follows
// Function.TemplateArgs. Constructors, destruction and ownership stay explicit.
type MethodSelection struct {
	Class, Name, Symbol string
	TemplateArgs        []string
	// Overload selects native parameter types and receiver cv/ref qualifiers.
	Overload *MethodOverload `json:",omitempty"`
}

// InferredMethod describes native arguments/result, excluding the receiver.
// The C export instead takes the receiver first, followed by these transports
// and an output pointer for non-void results, and returns an int32 status.
// Parse Facade.CHeader for bindings; do not bind Transport to the C export.
type InferredMethod struct {
	Selection       MethodSelection
	Transport       abi.Signature
	Const, Volatile bool
	Ref             RefQualifier
	Noexcept        bool
}

// InferredMethodFacade owns generated sources and the ordered method selections.
type InferredMethodFacade struct {
	Facade  *Facade
	Methods []InferredMethod
}

// InferMethods derives scalar/enum/address transports and receiver cv/ref and
// noexcept qualifiers through the same bounded compiler queries as InferFunctions.
// The native compiler handles inherited members, virtual dispatch and native member
// pointer representations. The input handle must already point to the selected
// Class subobject; no Go-side class layout, base offset or RTTI is inferred.
//
// Select 1-256 unambiguous non-static methods, each with at most 32 explicit native
// parameters. Overload selects native parameter and receiver types when resolving
// overloaded names. Static functions use InferFunctions; unresolved overloads, unsupported
// value/reference types and variadics need explicit manifests/native wrappers.
// Rvalue-qualified calls neither consume nor destroy a handle automatically.
// Ownership, constructor/destructor policy and callback prototypes stay explicit.
// Each name has a 256-byte limit; InferFunctions' aggregate/query limits apply.
func InferMethods(ctx context.Context, header string, input []MethodSelection, opts DiscoveryOptions) (*InferredMethodFacade, error) {
	methods, selections, namespace, err := inferenceMethods(input)
	if err != nil {
		return nil, err
	}
	result, err := inferSelected(ctx, header, selections, methods, namespace, opts)
	if err != nil {
		return nil, err
	}
	output := &InferredMethodFacade{Facade: result.facade, Methods: make([]InferredMethod, len(methods))}
	for i, s := range methods {
		q := result.qualifiers[i]
		output.Methods[i] = InferredMethod{Selection: s, Transport: result.Functions[i].Transport, Const: q&1 != 0, Volatile: q&2 != 0, Ref: RefQualifier(q % inferredReceiverForms / 4), Noexcept: q >= inferredReceiverForms}
	}
	return output, nil
}

// Receiver codes are cv +4*ref; noexcept adds one complete set of twelve forms.
const inferredReceiverForms = 12

func inferenceMethods(input []MethodSelection) ([]MethodSelection, []FunctionSelection, string, error) {
	if len(input) == 0 || len(input) > maxFacadeFunctions {
		return nil, nil, "", fmt.Errorf("cxx: select 1-256 methods for inference")
	}
	methods := make([]MethodSelection, len(input))
	selections := make([]FunctionSelection, len(input))
	seen := map[string]bool{}
	for i, s := range input {
		if len(s.Class) > maxDiscoveryName || !qualifiedName(s.Class) || len(s.Name) > maxDiscoveryName || (!identifier(s.Name) && s.Name != "operator()") {
			return nil, nil, "", fmt.Errorf("cxx: invalid inferred method name %q::%q", s.Class, s.Name)
		}
		if s.Overload != nil {
			if s.Overload.Ref > RefRValue {
				return nil, nil, "", fmt.Errorf("cxx: invalid overload receiver ref qualifier")
			}
			choice := *s.Overload
			selected, err := snapshotFunctionOverload(&FunctionOverload{ParameterTypes: choice.ParameterTypes})
			if err != nil {
				return nil, nil, "", fmt.Errorf("cxx: inferred %s::%s overload: %w", s.Class, s.Name, err)
			}
			choice.ParameterTypes = selected.ParameterTypes
			s.Overload = &choice
		}
		selected, err := snapshotInferenceExport(FunctionSelection{Name: s.Class + "::" + s.Name, Symbol: s.Symbol, TemplateArgs: s.TemplateArgs}, seen)
		if err != nil {
			return nil, nil, "", err
		}
		s.TemplateArgs = selected.TemplateArgs
		methods[i] = s
		selections[i] = selected
	}
	encoded, _ := json.Marshal(methods)
	hash := sha256.Sum256(encoded)
	return methods, selections, fmt.Sprintf("dylib_go_inferred_methods_%x", hash[:8]), nil
}

// Specialize all twelve receiver forms, preserving exact native member pointer
// types, including noexcept. No member-pointer bytes or vtable offsets enter Go.
func memberInferenceTraits(overloaded bool) string {
	var out strings.Builder
	out.WriteString("template<class F> struct Member;\n")
	if overloaded {
		out.WriteString("template<int Q,class C,class R,class... A> struct ChooseMember;\n")
	}
	for ref := RefNone; ref <= RefRValue; ref++ {
		for cv := 0; cv < 4; cv++ {
			qual := cvQualified("", cv&1 != 0, cv&2 != 0)
			qual = strings.TrimSpace(qual)
			suffix := ""
			if ref == RefLValue {
				suffix = "&"
			} else if ref == RefRValue {
				suffix = "&&"
			}
			spelling := strings.TrimSpace(qual + " " + suffix)
			receiver := cvQualified("T", cv&1 != 0, cv&2 != 0) + "&"
			if ref == RefRValue {
				receiver += "&"
			}
			q := int(ref)*4 + cv
			if overloaded {
				// Selected-class targets resolve template addresses. Deduced
				// declaring classes preserve inherited member pointers.
				fmt.Fprintf(&out, "template<class C,class R,class... A> struct ChooseMember<%d,C,R,A...> {\n static constexpr auto pick(R(C::*p)(A...) %s) { return p; }\n static constexpr auto pick(R(C::*p)(A...) %s noexcept) { return p; }\n template<class B> static constexpr auto pick(R(B::*p)(A...) %s) { return p; }\n template<class B> static constexpr auto pick(R(B::*p)(A...) %s noexcept) { return p; }\n};\n", q, spelling, spelling, spelling, spelling)
			}
			fmt.Fprintf(&out, "template<class C,class R,class... A> struct Member<R(C::*)(A...) %s>:Prototype<R(*)(A...)> { template<class T> using Receiver=%s; static constexpr int qualifiers=%d; };\n", spelling, receiver, q)
			fmt.Fprintf(&out, "template<class C,class R,class... A> struct Member<R(C::*)(A...) %s noexcept>:Member<R(C::*)(A...) %s> { static constexpr int qualifiers=%d; };\n", spelling, spelling, q+inferredReceiverForms)
		}
	}
	return out.String()
}
