package cxx

import (
	"strings"
	"testing"
)

func TestHeaderASTIdentity(t *testing.T) {
	global := memberNode{Kind: "CXXRecordDecl", ID: "class", Name: "Global", TagUsed: "struct", CompleteDefinition: true}
	f := memberNode{Kind: "FunctionDecl", ID: "function", Name: "add"}
	f.Type.QualType = "int (int)"
	templ := templateFixture().namespaces[0].Inner[0]
	childClass, childFunction := global, f
	childClass.ID, childFunction.ID = "childClass", "childFunction"
	childTemplate := templateFixture().namespaces[0].Inner[0]
	childTemplate.ID = "childTemplate"
	child := memberNode{Kind: "NamespaceDecl", ID: "child", Name: "child", Inner: []memberNode{childClass, childFunction, childTemplate}}
	root := memberNode{Kind: "TranslationUnitDecl", ID: "unit", Inner: []memberNode{global, f, templ, child}}
	ast, err := decodeHeaderAST(encodeMembers(t, []memberNode{root}))
	if err != nil {
		t.Fatal(err)
	}
	classes, found, err := ast.classCandidates("", "unit")
	if err != nil || !found || len(classes) != 2 || classes[0] != "Global" || classes[1] != "child::Global" {
		t.Fatal(classes, found, err)
	}
	functions, err := ast.namespaceFunctions("", "unit")
	if err != nil || len(functions) != 2 || functions[0].Name != "add" || functions[1].Name != "child::add" {
		t.Fatal(functions, err)
	}
	templates, err := ast.namespaceFunctionTemplates("", "unit")
	if err != nil || len(templates) != 2 || templates[0].Function.Name != "f" || templates[1].Function.Name != "child::f" {
		t.Fatal(templates, err)
	}
	if len(ast.catalogRoots("", "wrong")) != 0 || len((&memberAST{}).catalogRoots("", "unit")) != 0 {
		t.Fatal("wrong whole-unit identity selected")
	}
}

func TestHeaderASTValidation(t *testing.T) {
	good := memberNode{Kind: "TranslationUnitDecl", ID: "unit"}
	for name, roots := range map[string][]memberNode{
		"missing": nil, "wrong_kind": {{Kind: "NamespaceDecl", ID: "unit"}}, "no_id": {{Kind: "TranslationUnitDecl"}}, "duplicate": {good, good}, "trailing": {good, {Kind: "NamespaceDecl"}}, "nested": {{Kind: "TranslationUnitDecl", ID: "unit", Inner: []memberNode{good}}},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := decodeHeaderAST(encodeMembers(t, roots)); err == nil || got != nil {
				t.Fatal(got, err)
			}
		})
	}
	for _, text := range []string{"{", `{"kind":"TranslationUnitDecl","id":"x"} true`, strings.Repeat(" ", maxDiscoveryAST+1)} {
		if got, err := decodeHeaderAST([]byte(text)); err == nil || got != nil {
			t.Fatal(got, err)
		}
	}
}
