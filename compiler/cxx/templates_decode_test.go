package cxx

import (
	"fmt"
	"testing"
)

func templateFixture() *memberAST {
	p := memberNode{Kind: "ParmVarDecl", Name: "value"}
	p.Type.QualType = "T"
	f := memberNode{Kind: "FunctionDecl", ID: "primary", Name: "f", Inner: []memberNode{p}}
	f.Type.QualType = "T (T)"
	ty := memberNode{Kind: "TemplateTypeParmDecl", Name: "T"}
	value := memberNode{Kind: "NonTypeTemplateParmDecl", Name: "N", DefaultArg: &memberReference{Kind: "TemplateArgument"}}
	value.Type.QualType = "int"
	spec := f
	spec.ID = "specialization"
	spec.Inner = []memberNode{{Kind: "TemplateArgument"}, p}
	spec.Type.QualType = "int (int)"
	ref := spec
	ref.ID = "specialization_ref"
	ref.Inner = nil
	declaration := memberNode{Kind: "FunctionTemplateDecl", ID: "template", Name: "f", Inner: []memberNode{ty, value, f, spec, ref}}
	ns := memberNode{Kind: "NamespaceDecl", ID: "chosen", Name: "catalog", Inner: []memberNode{declaration}}
	decoy := ns
	decoy.ID = "wrong"
	return &memberAST{namespaces: []*memberNode{&ns, &decoy}}
}

func TestTemplateDiscoveryIdentity(t *testing.T) {
	for _, name := range []string{"primary_only", "duplicates", "redeclaration", "nested_parameters"} {
		t.Run(name, func(t *testing.T) {
			ast := templateFixture()
			root := ast.namespaces[0]
			switch name {
			case "duplicates":
				root.Inner = append(root.Inner, root.Inner[0])
			case "redeclaration":
				copy := root.Inner[0]
				copy.ID = "redecl"
				root.Inner = append(root.Inner, copy)
			case "nested_parameters":
				root.Inner[0].Inner = append(root.Inner[0].Inner, memberNode{Kind: "TemplateTemplateParmDecl", Name: "Box", IsParameterPack: true, Inner: []memberNode{{Kind: "TemplateTypeParmDecl"}}})
			}
			got, err := ast.namespaceFunctionTemplates("alias", "chosen")
			want := 1
			if name == "redeclaration" {
				want = 2
			}
			if err != nil || len(got) != want || got[0].Function.Name != "alias::f" || got[0].Function.NativeType != "T (T)" || !got[0].Parameters[1].HasDefault {
				t.Fatal(got, err)
			}
			if name == "nested_parameters" && (len(got[0].Parameters) != 3 || !got[0].Parameters[2].Pack || len(got[0].Parameters[2].Parameters) != 1) {
				t.Fatal(got)
			}
		})
	}
}

func TestTemplateDiscoveryMetadataValidation(t *testing.T) {
	for name, patch := range map[string]func(*memberNode){
		"missing_id":           func(n *memberNode) { n.ID = "" },
		"missing_primary":      func(n *memberNode) { n.Inner = n.Inner[:2] },
		"specialization_first": func(n *memberNode) { n.Inner[2], n.Inner[3] = n.Inner[3], n.Inner[2] },
		"primary_name":         func(n *memberNode) { n.Inner[2].Name = "other" },
		"primary_implicit":     func(n *memberNode) { n.Inner[2].IsImplicit = true },
		"primary_id":           func(n *memberNode) { n.Inner[2].ID = "" },
		"primary_type":         func(n *memberNode) { n.Inner[2].Type.QualType = "" },
		"no_parameters":        func(n *memberNode) { n.Inner = n.Inner[2:] },
		"value_type":           func(n *memberNode) { n.Inner[1].Type.QualType = "" },
		"parameter_name":       func(n *memberNode) { n.Inner[0].Name = "bad;" },
		"default_kind":         func(n *memberNode) { n.Inner[1].DefaultArg = &memberReference{Kind: "wrong"} },
		"nested_empty":         func(n *memberNode) { n.Inner[0].Kind = "TemplateTemplateParmDecl" },
	} {
		t.Run(name, func(t *testing.T) {
			ast := templateFixture()
			patch(&ast.namespaces[0].Inner[0])
			if v, err := ast.namespaceFunctionTemplates("alias", "chosen"); err == nil || v != nil {
				t.Fatal("malformed template accepted", v, err)
			}
		})
	}
	t.Run("conflicting_identity", func(t *testing.T) {
		ast := templateFixture()
		n := ast.namespaces[0].Inner[0]
		n.Inner = append([]memberNode(nil), n.Inner...)
		n.Inner[2].Type.QualType = "int (T)"
		ast.namespaces[0].Inner = append(ast.namespaces[0].Inner, n)
		if v, err := ast.namespaceFunctionTemplates("alias", "chosen"); err == nil || v != nil {
			t.Fatal(v, err)
		}
	})
}

func TestTemplateParameterBudget(t *testing.T) {
	for _, count := range []int{64, 65} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			nodes := make([]memberNode, count)
			for i := range nodes {
				nodes[i].Kind = "TemplateTypeParmDecl"
			}
			seen := 0
			v, err := templateParameters(nodes, &seen)
			if count == 64 {
				if err != nil || len(v) != 64 {
					t.Fatal(len(v), err)
				}
			} else if err == nil || v != nil {
				t.Fatal(v, err)
			}
		})
	}
	t.Run("recursive", func(t *testing.T) {
		n := memberNode{Kind: "TemplateTypeParmDecl"}
		for i := 0; i < 65; i++ {
			n = memberNode{Kind: "TemplateTemplateParmDecl", Inner: []memberNode{n}}
		}
		seen := 0
		if v, err := templateParameters([]memberNode{n}, &seen); err == nil || v != nil {
			t.Fatal(v, err)
		}
	})
}
