package cxx

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestOverloadInputValidation(t *testing.T) {
	for _, mode := range []string{"empty_type", "expression", "pointer_spelling", "value", "void", "name_budget", "parameter_budget", "receiver"} {
		t.Run(mode, func(t *testing.T) {
			parameters := []string{"int"}
			switch mode {
			case "empty_type":
				parameters[0] = ""
			case "expression":
				parameters[0] = "decltype(f())"
			case "pointer_spelling":
				parameters[0] = "int*"
			case "value":
				parameters[0] = "true"
			case "void":
				parameters[0] = "void"
			case "name_budget":
				parameters[0] = strings.Repeat("a", 257)
			case "parameter_budget":
				parameters = make([]string, 33)
			}
			function := FunctionSelection{Name: "f", Symbol: "call", Overload: &FunctionOverload{ParameterTypes: parameters}}
			method := MethodSelection{Class: "R", Name: "f", Symbol: "call", Overload: &MethodOverload{ParameterTypes: parameters}}
			if mode == "receiver" {
				method.Overload.Ref = RefQualifier(3)
			} else if _, _, err := inferenceSelections([]FunctionSelection{function}); err == nil {
				t.Fatal("invalid function overload accepted")
			}
			if _, _, _, err := inferenceMethods([]MethodSelection{method}); err == nil {
				t.Fatal("invalid method overload accepted")
			}
		})
	}
}

func TestOverloadSnapshotsAndLegacyNamespaces(t *testing.T) {
	function := FunctionSelection{Name: "f", Symbol: "call", Overload: &FunctionOverload{ParameterTypes: []string{"int"}}}
	functions, _, err := inferenceSelections([]FunctionSelection{function})
	if err != nil {
		t.Fatal(err)
	}
	function.Overload.ParameterTypes[0] = "double"
	if functions[0].Overload.ParameterTypes[0] != "int" {
		t.Fatal(functions)
	}
	method := MethodSelection{Class: "R", Name: "f", Symbol: "call", Overload: &MethodOverload{ParameterTypes: []string{"int"}, Const: true, Ref: RefLValue}}
	methods, _, _, err := inferenceMethods([]MethodSelection{method})
	if err != nil {
		t.Fatal(err)
	}
	method.Overload.ParameterTypes[0] = "double"
	method.Overload.Const = false
	if methods[0].Overload.ParameterTypes[0] != "int" || !methods[0].Overload.Const {
		t.Fatal(methods)
	}

	// A nil selector preserves the serialized inputs used for existing names.
	legacyFunctions := []struct {
		Name, Symbol string
		TemplateArgs []string
	}{{Name: "f", Symbol: "call"}}
	encoded, err := json.Marshal(legacyFunctions)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(encoded)
	_, namespace, err := inferenceSelections([]FunctionSelection{{Name: "f", Symbol: "call"}})
	if err != nil || namespace != fmt.Sprintf("dylib_go_inferred_%x", hash[:8]) {
		t.Fatal(namespace, err)
	}
	_, zero, err := inferenceSelections([]FunctionSelection{{Name: "f", Symbol: "call", Overload: &FunctionOverload{}}})
	if err != nil || namespace == zero {
		t.Fatal(namespace, zero, err)
	}
	legacyMethods := []struct {
		Class, Name, Symbol string
		TemplateArgs        []string
	}{{Class: "R", Name: "f", Symbol: "call"}}
	encoded, err = json.Marshal(legacyMethods)
	if err != nil {
		t.Fatal(err)
	}
	hash = sha256.Sum256(encoded)
	_, _, namespace, err = inferenceMethods([]MethodSelection{{Class: "R", Name: "f", Symbol: "call"}})
	if err != nil || namespace != fmt.Sprintf("dylib_go_inferred_methods_%x", hash[:8]) {
		t.Fatal(namespace, err)
	}
	_, _, zero, err = inferenceMethods([]MethodSelection{{Class: "R", Name: "f", Symbol: "call", Overload: &MethodOverload{}}})
	if err != nil || namespace == zero {
		t.Fatal(namespace, zero, err)
	}
}

func TestOverloadQuerySourceBudget(t *testing.T) {
	selections := make([]FunctionSelection, 256)
	for i := range selections {
		types := make([]string, 32)
		for j := range types {
			types[j] = strings.Repeat("T", 256)
		}
		selections[i] = FunctionSelection{Name: "f", Symbol: fmt.Sprintf("call_%d", i), Overload: &FunctionOverload{ParameterTypes: types}}
	}
	selected, namespace, err := inferenceSelections(selections)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inferenceProbes(selected, namespace, nil, nil); err == nil {
		t.Fatal("overload source exceeded query budget")
	}
}
