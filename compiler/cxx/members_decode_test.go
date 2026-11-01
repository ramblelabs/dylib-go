package cxx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func memberFixture() []memberNode {
	ref := memberNode{ID: "forward", Kind: "CXXRecordDecl", Name: "Counter"}
	alias := memberNode{Kind: "TypeAliasDecl", Name: "selected", Inner: []memberNode{
		{Kind: "TemplateArgument", Inner: []memberNode{{Kind: "RecordType", Decl: &memberReference{ID: "argument", Name: "Argument"}}}},
		{Kind: "RecordType", Decl: &memberReference{ID: ref.ID, Kind: ref.Kind, Name: ref.Name}},
	}}
	call := memberNode{Kind: "CXXMethodDecl", Name: "read"}
	call.Type.QualType = "int () const"
	definition := memberNode{ID: "definition", PreviousDecl: "forward", Kind: "CXXRecordDecl", Name: "Counter", TagUsed: "struct", CompleteDefinition: true, Inner: []memberNode{call}}
	wrong := definition
	wrong.ID, wrong.PreviousDecl, wrong.Inner = "wrong_namespace", "", nil
	return []memberNode{ref, wrong, definition, {Kind: "NamespaceDecl", Name: "query", Inner: []memberNode{alias}}}
}

func encodeMembers(t *testing.T, roots []memberNode) []byte {
	t.Helper()
	var out bytes.Buffer
	for _, root := range roots {
		if err := json.NewEncoder(&out).Encode(root); err != nil {
			t.Fatal(err)
		}
	}
	return out.Bytes()
}

func selectedMembers(data []byte) (*ClassDeclaration, error) {
	ast, err := decodeMemberAST(data, "query")
	if err != nil {
		return nil, err
	}
	ref, err := ast.selectedRecord()
	if err != nil {
		return nil, err
	}
	definition, err := ast.definition(ref.ID)
	if err != nil {
		return nil, err
	}
	return publicCallables("example::Alias", definition)
}

func TestMemberDiscoveryRecordIdentity(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		roots := memberFixture()
		if duplicate {
			roots = append(roots, roots[2])
		}
		got, err := selectedMembers(encodeMembers(t, roots))
		if err != nil || got.Name != "example::Alias" || len(got.Callables) != 1 || got.Callables[0].Name != "read" {
			t.Fatal("forward/alias identity was confused with template arguments or another namespace", got, err)
		}
	}
	t.Run("unevaluated_argument", func(t *testing.T) {
		roots := memberFixture()
		alias := &roots[3].Inner[0]
		alias.Inner = append(alias.Inner, memberNode{Kind: "CallExpr", Inner: []memberNode{{Kind: "RecordType", Decl: &memberReference{ID: "wrong_argument", Kind: "CXXRecordDecl"}}}})
		if got, err := selectedMembers(encodeMembers(t, roots)); err != nil || got == nil {
			t.Fatal("unevaluated expression selected the argument's record", got, err)
		}
	})
	t.Run("lambda_public_operator", func(t *testing.T) {
		roots := memberFixture()
		roots[2].Name, roots[2].TagUsed = "", "class"
		roots[2].DefinitionData.IsLambda = true
		roots[3].Inner[0].Inner[1].Decl.Name = ""
		got, err := selectedMembers(encodeMembers(t, roots))
		if err != nil || got == nil || len(got.Callables) != 1 {
			t.Fatal("closure call operators must have public access", got, err)
		}
	})
}

func TestMemberDiscoveryMetadataValidation(t *testing.T) {
	mutations := map[string]func([]memberNode) []memberNode{
		"missing_probe":     func(n []memberNode) []memberNode { return n[:3] },
		"duplicate_probe":   func(n []memberNode) []memberNode { return append(n, n[3]) },
		"missing_alias":     func(n []memberNode) []memberNode { n[3].Inner = nil; return n },
		"duplicate_alias":   func(n []memberNode) []memberNode { n[3].Inner = append(n[3].Inner, n[3].Inner[0]); return n },
		"missing_reference": func(n []memberNode) []memberNode { n[3].Inner[0].Inner = nil; return n },
		"ambiguous_reference": func(n []memberNode) []memberNode {
			n[3].Inner[0].Inner = append(n[3].Inner[0].Inner, memberNode{Kind: "RecordType", Decl: &memberReference{ID: "another"}})
			return n
		},
		"missing_id":            func(n []memberNode) []memberNode { n[3].Inner[0].Inner[1].Decl.ID = ""; return n },
		"wrong_reference_kind":  func(n []memberNode) []memberNode { n[3].Inner[0].Inner[1].Decl.Kind = "RecordDecl"; return n },
		"missing_definition":    func(n []memberNode) []memberNode { n[2].CompleteDefinition = false; return n },
		"ambiguous_definition":  func(n []memberNode) []memberNode { d := n[2]; d.ID = "second"; return append(n, d) },
		"cyclic_declarations":   func(n []memberNode) []memberNode { n[0].PreviousDecl = "definition"; return n },
		"conflicting_duplicate": func(n []memberNode) []memberNode { d := n[2]; d.Name = "Other"; return append(n, d) },
		"union_definition":      func(n []memberNode) []memberNode { n[2].TagUsed = "union"; return n },
		"invalid_access": func(n []memberNode) []memberNode {
			n[2].Inner = append([]memberNode{{Kind: "AccessSpecDecl", Access: "invalid"}}, n[2].Inner...)
			return n
		},
		"missing_name":           func(n []memberNode) []memberNode { n[2].Inner[0].Name = ""; return n },
		"missing_type":           func(n []memberNode) []memberNode { n[2].Inner[0].Type.QualType = ""; return n },
		"missing_parameter_type": func(n []memberNode) []memberNode { n[2].Inner[0].Inner = []memberNode{{Kind: "ParmVarDecl"}}; return n },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			if got, err := selectedMembers(encodeMembers(t, mutate(memberFixture()))); err == nil || got != nil {
				t.Fatal("malformed member metadata accepted", got, err)
			}
		})
	}
	for _, data := range [][]byte{nil, []byte("invalid"), append(encodeMembers(t, memberFixture()), []byte("bad trailing data")...)} {
		if got, err := selectedMembers(data); err == nil || got != nil {
			t.Fatal(got, err)
		}
	}
}

func TestMemberDiscoveryASTBudgets(t *testing.T) {
	for name, data := range map[string][]byte{
		"bytes":      bytes.Repeat([]byte{' '}, maxDiscoveryAST+1),
		"depth":      []byte(strings.Repeat(`{"inner":[`, 258) + `{}` + strings.Repeat(`]}`, 258)),
		"json_depth": []byte(strings.Repeat(`{"ignored":[`, 1024) + `{}` + strings.Repeat(`]}`, 1024)),
		"nodes":      []byte(`{"inner":[` + strings.Repeat(`{},`, 150000) + `{}]}`),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := decodeMemberAST(data, "query"); err == nil || got != nil || !strings.Contains(err.Error(), "budget") {
				t.Fatal(got, err)
			}
		})
	}
	t.Run("declaration_references", func(t *testing.T) {
		roots := memberFixture()
		for i := 0; i < 100; i++ {
			roots = append(roots, memberNode{Kind: "DeclRefExpr", ReferencedDecl: &memberReference{Kind: "FunctionDecl", Name: fmt.Sprintf("factory%d", i)}})
		}
		ast, err := decodeMemberAST(encodeMembers(t, roots), "query")
		if err != nil || ast == nil || len(ast.references) != maxAnonymousMemberQueries {
			t.Fatal("reference collection was not bounded", ast, err)
		}
	})
}

func TestMemberDiscoveryPublicAccess(t *testing.T) {
	roots := memberFixture()
	d := &roots[2]
	d.TagUsed = "class"
	call := d.Inner[0]
	implicit := call
	implicit.IsImplicit = true
	nested := memberNode{Kind: "CXXRecordDecl", Inner: []memberNode{call}}
	d.Inner = []memberNode{call, {Kind: "AccessSpecDecl", Access: "public"}, implicit, nested, call, {Kind: "AccessSpecDecl", Access: "protected"}, call, {Kind: "AccessSpecDecl", Access: "private"}, call}
	got, err := selectedMembers(encodeMembers(t, roots))
	if err != nil || len(got.Callables) != 1 {
		t.Fatal("implicit/nested/non-public member leaked", got, err)
	}
}
