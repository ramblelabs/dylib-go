package cxx

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestHierarchySelectionMetadata(t *testing.T) {
	selections := []BaseRelation{{Derived: "Derived", Base: "Base"}}
	_, namespace := hierarchyProbes(selections)
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(map[string]any{"kind": "NamespaceDecl", "name": namespace, "inner": []any{map[string]any{"kind": "TypeAliasDecl", "name": "edge_0", "type": map[string]string{"desugaredQualType": namespace + "::Answer<true>"}}}})
	if err != nil {
		t.Fatal(err)
	}
	data := encoded.Bytes()
	if err := decodeHierarchySelections(data, namespace, selections); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"false": strings.Replace(string(data), "<true>", "<false>", 1), "missing": strings.Replace(string(data), "edge_0", "edge_1", 1), "invalid": strings.Replace(string(data), "<true>", "<1>", 1), "trailing": string(data) + " {}"} {
		t.Run(name, func(t *testing.T) {
			if err := decodeHierarchySelections([]byte(text), namespace, selections); err == nil {
				t.Fatal("malformed hierarchy selection accepted")
			}
		})
	}
}
