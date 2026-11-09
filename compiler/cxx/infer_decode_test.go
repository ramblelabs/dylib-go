package cxx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/abi"
)

func inferenceFixture(t *testing.T, patch func(map[string]int)) []byte {
	t.Helper()
	values := map[string]int{"pointer_bytes": 8, "f0_count": 1, "f0_result": int(abi.I32), "f0_arg0": int(abi.I8)}
	if patch != nil {
		patch(values)
	}
	var aliases []map[string]any
	for name, value := range values {
		aliases = append(aliases, map[string]any{"kind": "TypeAliasDecl", "name": name, "type": map[string]string{"desugaredQualType": fmt.Sprintf("probe::Answer<%d>", value)}})
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(map[string]any{"kind": "NamespaceDecl", "name": "probe", "inner": aliases})
	data := buffer.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestInferenceMetadataValidation(t *testing.T) {
	selections := []FunctionSelection{{Name: "f", Symbol: "call"}}
	good := inferenceFixture(t, nil)
	got, err := decodeInference(good, "probe", selections, []int{1})
	if err != nil || got.pointerSize != 8 || got.Functions[0].Transport.Args[0] != abi.I8 {
		t.Fatal(got, err)
	}
	for name, patch := range map[string]func(map[string]int){
		"pointer_width":      func(v map[string]int) { v["pointer_bytes"] = 16 },
		"missing":            func(v map[string]int) { delete(v, "f0_arg0") },
		"unknown_key":        func(v map[string]int) { delete(v, "f0_arg0"); v["wrong"] = 8 },
		"negative_arity":     func(v map[string]int) { v["f0_count"] = -1 },
		"excess_arity":       func(v map[string]int) { v["f0_count"] = 33 },
		"changed_arity":      func(v map[string]int) { v["f0_count"] = 2 },
		"unsupported_result": func(v map[string]int) { v["f0_result"] = -1 },
		"record_result":      func(v map[string]int) { v["f0_result"] = int(abi.Struct) },
		"void_arg":           func(v map[string]int) { v["f0_arg0"] = int(abi.Void) },
		"unsupported_arg":    func(v map[string]int) { v["f0_arg0"] = -1 },
		"record_arg":         func(v map[string]int) { v["f0_arg0"] = int(abi.Struct) },
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := decodeInference(inferenceFixture(t, patch), "probe", selections, []int{1}); err == nil || got != nil {
				t.Fatal(got, err)
			}
		})
	}
	for name, input := range map[string][]byte{
		"invalid_json": []byte("{"), "trailing": append(append([]byte(nil), good...), []byte(" {}")...),
		"namespace":      []byte(strings.Replace(string(good), `"name":"probe"`, `"name":"wrong"`, 1)),
		"number":         []byte(strings.Replace(string(good), "probe::Answer<8>", "probe::Answer<+8>", 1)),
		"nested":         []byte(strings.Replace(string(good), "probe::Answer<8>", "probe::Answer<Answer<8>>", 1)),
		"wrong_template": []byte(strings.Replace(string(good), "probe::Answer<8>", "wrong::Answer<8>", 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := decodeInference(input, "probe", selections, []int{1}); err == nil || got != nil {
				t.Fatal(got, err)
			}
		})
	}
}

func TestInferenceSelectionSnapshots(t *testing.T) {
	selections := []FunctionSelection{{Name: "f", Symbol: "call", TemplateArgs: []string{"int"}}}
	got, ns, err := inferenceSelections(selections)
	if err != nil {
		t.Fatal(err)
	}
	selections[0].Name = "other"
	selections[0].TemplateArgs[0] = "double"
	if got[0].Name != "f" || got[0].TemplateArgs[0] != "int" {
		t.Fatal(got)
	}
	nilArgs, nsNil, err := inferenceSelections([]FunctionSelection{{Name: "f", Symbol: "call"}})
	if err != nil {
		t.Fatal(err)
	}
	emptyArgs, nsEmpty, err := inferenceSelections([]FunctionSelection{{Name: "f", Symbol: "call", TemplateArgs: []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	if nilArgs[0].TemplateArgs != nil || emptyArgs[0].TemplateArgs == nil || ns == nsNil || nsNil == nsEmpty {
		t.Fatal(ns, nsNil, nsEmpty)
	}
}

func TestInferenceQueryBudgets(t *testing.T) {
	t.Run("ast_bytes", func(t *testing.T) {
		if _, err := decodeDiscoveryAnswers([]byte(strings.Repeat(" ", maxDiscoveryAST+1)), "probe", 1); err == nil {
			t.Fatal("oversized AST accepted")
		}
	})
	t.Run("ast_depth", func(t *testing.T) {
		data := []byte(strings.Repeat("[", 2*maxMemberDepth+65) + strings.Repeat("]", 2*maxMemberDepth+65))
		if _, err := decodeDiscoveryAnswers(data, "probe", 1); err == nil {
			t.Fatal("deep AST accepted")
		}
	})
	t.Run("source_bytes", func(t *testing.T) {
		selections := make([]FunctionSelection, 256)
		for i := range selections {
			selections[i] = FunctionSelection{Name: strings.Repeat("n", 256), Symbol: fmt.Sprintf("f%d", i), TemplateArgs: make([]string, 16)}
			for j := range selections[i].TemplateArgs {
				selections[i].TemplateArgs[j] = strings.Repeat("T", 128)
			}
		}
		// Each selection passes the individual grammar limits; their aggregate query
		// must still respect the separate source cap before invoking the compiler.
		selected, ns, err := inferenceSelections(selections)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inferenceProbes(selected, ns, nil); err == nil {
			t.Fatal("oversized query accepted")
		}
	})
}
