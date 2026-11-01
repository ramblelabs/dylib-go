package cxx

import (
	"reflect"
	"testing"
)

func TestClassDiscoveryNamespaceIdentity(t *testing.T) {
	chosen := memberNode{ID: "chosen", Kind: "NamespaceDecl", Name: "catalog", Inner: []memberNode{{Kind: "CXXRecordDecl", Name: "First", TagUsed: "struct"}}}
	reopened := memberNode{ID: "reopened", Kind: "NamespaceDecl", Name: "catalog", OriginalNamespace: &memberReference{ID: "chosen", Kind: "NamespaceDecl"}, Inner: []memberNode{{Kind: "TypeAliasDecl", Name: "Alias"}}}
	decoy := chosen
	decoy.ID, decoy.Inner = "decoy", []memberNode{{Kind: "CXXRecordDecl", Name: "Wrong", TagUsed: "struct"}}
	probe := memberNode{Kind: "NamespaceDecl", Name: "query", Inner: []memberNode{{Kind: "UsingDirectiveDecl", NominatedNamespace: &memberReference{ID: "chosen", Kind: "NamespaceDecl", Name: "catalog"}}}}
	ast, err := decodeMemberAST(encodeMembers(t, []memberNode{chosen, reopened, decoy, probe}), "query")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := ast.selectedNamespace()
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := ast.classCandidates("alias", ref.ID)
	if err != nil || !found || !reflect.DeepEqual(got, []string{"alias::First", "alias::Alias"}) {
		t.Fatal("namespace identity/redeclarations", got, found, err)
	}
	for _, node := range []memberNode{
		{},
		{Inner: []memberNode{{Kind: "UsingDirectiveDecl"}}},
		{Inner: []memberNode{{Kind: "UsingDirectiveDecl", NominatedNamespace: &memberReference{ID: "chosen", Kind: "CXXRecordDecl"}}}},
		{Inner: append(probe.Inner, probe.Inner...)},
	} {
		if got, err := (&memberAST{probe: &node}).selectedNamespace(); err == nil || got != nil {
			t.Fatal("malformed namespace identity", got, err)
		}
	}
}
