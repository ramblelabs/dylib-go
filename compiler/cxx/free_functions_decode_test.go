package cxx

import (
	"strings"
	"testing"
)

func functionFixture() *memberAST {
	function := memberNode{Kind: "FunctionDecl", ID: "first", Name: "f"}
	function.Type.QualType = "int (int)"
	parameter := memberNode{Kind: "ParmVarDecl", Name: "value", Init: "c"}
	parameter.Type.QualType = "int"
	function.Inner = []memberNode{parameter}
	forward := function
	forward.ID = "second"
	forward.PreviousDecl = "first"
	chosen := memberNode{Kind: "NamespaceDecl", ID: "chosen", Name: "selected", Inner: []memberNode{function}}
	reopened := memberNode{Kind: "NamespaceDecl", ID: "reopened", Name: "selected", OriginalNamespace: &memberReference{ID: "chosen", Kind: "NamespaceDecl"}, Inner: []memberNode{forward}}
	decoy := chosen
	decoy.ID = "wrong"
	decoy.Inner = []memberNode{{Kind: "FunctionDecl", Name: "wrong"}}
	return &memberAST{namespaces: []*memberNode{&chosen, &decoy, &reopened}}
}

func TestFunctionDiscoveryIdentity(t *testing.T) {
	for _, name := range []string{"redeclared", "duplicated", "excluded", "nested"} {
		t.Run(name, func(t *testing.T) {
			ast := functionFixture()
			root := ast.namespaces[0]
			switch name {
			case "duplicated":
				root.Inner = append(root.Inner, root.Inner[0])
			case "excluded":
				f := root.Inner[0]
				for _, kind := range []string{"CXXRecordDecl", "FunctionTemplateDecl", "UsingDecl", "UsingDirectiveDecl"} {
					root.Inner = append(root.Inner, memberNode{Kind: kind, Name: "skip", Inner: []memberNode{f}})
				}
				root.Inner = append(root.Inner, memberNode{Kind: "NamespaceDecl", Inner: []memberNode{f}}, memberNode{Kind: "FunctionDecl", Name: "operator+"})
			case "nested":
				root.Inner = append(root.Inner, memberNode{Kind: "NamespaceDecl", Name: "child", Inner: []memberNode{{Kind: "LinkageSpecDecl", Inner: []memberNode{root.Inner[0]}}}})
				root.Inner[1].Inner[0].Inner[0].ID = "nested"
			}
			got, err := ast.namespaceFunctions("alias", "chosen")
			want := 2
			if name == "nested" {
				want = 3
			}
			if err != nil || len(got) != want || got[0].Name != "alias::f" || got[len(got)-1].Name != "alias::f" || !got[0].Parameters[0].HasDefault {
				t.Fatal(got, err)
			}
			if name == "nested" && got[1].Name != "alias::child::f" {
				t.Fatal(got)
			}
		})
	}
}

func TestFunctionDiscoveryMetadataValidation(t *testing.T) {
	for name, patch := range map[string]func(*memberNode){
		"missing_id":     func(n *memberNode) { n.ID = "" },
		"missing_type":   func(n *memberNode) { n.Type.QualType = "" },
		"missing_name":   func(n *memberNode) { n.Name = "" },
		"bad_name":       func(n *memberNode) { n.Name = "bad;" },
		"name_budget":    func(n *memberNode) { n.Name = strings.Repeat("x", 257) },
		"parameter_type": func(n *memberNode) { n.Inner[0].Type.QualType = "" },
	} {
		t.Run(name, func(t *testing.T) {
			ast := functionFixture()
			patch(&ast.namespaces[0].Inner[0])
			if v, err := ast.namespaceFunctions("alias", "chosen"); err == nil || v != nil {
				t.Fatal("malformed metadata accepted", v, err)
			}
		})
	}
	t.Run("conflicting_identity", func(t *testing.T) {
		ast := functionFixture()
		f := ast.namespaces[0].Inner[0]
		f.Type.QualType = "void ()"
		ast.namespaces[0].Inner = append(ast.namespaces[0].Inner, f)
		if v, err := ast.namespaceFunctions("alias", "chosen"); err == nil || v != nil {
			t.Fatal(v, err)
		}
	})
	t.Run("invalid_namespace", func(t *testing.T) {
		ast := functionFixture()
		ast.namespaces[0].Inner = append(ast.namespaces[0].Inner, memberNode{Kind: "NamespaceDecl", Name: "bad;"})
		if v, err := ast.namespaceFunctions("alias", "chosen"); err == nil || v != nil {
			t.Fatal(v, err)
		}
	})
}
