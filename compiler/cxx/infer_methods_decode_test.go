package cxx

import (
	"strings"
	"testing"
)

func TestInferredMethodMetadata(t *testing.T) {
	selections := []FunctionSelection{{Name: "R::f", Symbol: "call"}}
	methods := []MethodSelection{{Class: "R", Name: "f", Symbol: "call"}}
	for _, mode := range []string{"valid", "missing", "negative", "too_high", "bad_number", "wrong_count"} {
		t.Run(mode, func(t *testing.T) {
			data := inferenceFixture(t, func(v map[string]int) {
				v["f0_receiver"] = 23
				switch mode {
				case "missing":
					delete(v, "f0_receiver")
				case "negative":
					v["f0_receiver"] = -1
				case "too_high":
					v["f0_receiver"] = 24
				case "wrong_count":
					v["extra"] = 0
				}
			})
			if mode == "bad_number" {
				data = []byte(strings.Replace(string(data), "probe::Answer<23>", "probe::Answer<023>", 1))
			}
			out, err := decodeInference(data, "probe", selections, []int{1}, methods)
			if mode == "valid" {
				if err != nil || out.qualifiers[0] != 23 {
					t.Fatal(out, err)
				}
				return
			}
			if err == nil || out != nil {
				t.Fatal(out, err)
			}
		})
	}
}

func TestInferredMethodSnapshots(t *testing.T) {
	input := []MethodSelection{{Class: "R", Name: "f", Symbol: "call", TemplateArgs: []string{"int"}}}
	got, _, ns, err := inferenceMethods(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0].Class = "Other"
	input[0].TemplateArgs[0] = "double"
	if got[0].Class != "R" || got[0].TemplateArgs[0] != "int" {
		t.Fatal(got)
	}
	plain, _, n, err := inferenceMethods([]MethodSelection{{Class: "R", Name: "f", Symbol: "call"}})
	if err != nil {
		t.Fatal(err)
	}
	empty, _, e, err := inferenceMethods([]MethodSelection{{Class: "R", Name: "f", Symbol: "call", TemplateArgs: []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	if plain[0].TemplateArgs != nil || empty[0].TemplateArgs == nil || ns == n || n == e {
		t.Fatal(ns, n, e)
	}
}
