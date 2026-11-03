package cxx

import (
	"reflect"
	"testing"
)

func TestDirectBaseMetadataRecordIdentity(t *testing.T) {
	roots := memberFixture()
	base := memberBase{Access: "public", IsVirtual: true}
	base.Type.QualType = "ParentAlias"
	roots[2].Bases = []memberBase{base}
	wrong := memberBase{Access: "private"}
	wrong.Type.QualType = "Unrelated"
	roots[1].Bases = []memberBase{wrong}
	got, err := selectedMembers(encodeMembers(t, roots))
	want := []BaseDeclaration{{NativeType: "ParentAlias", Access: "public", Virtual: true}}
	if err != nil || got == nil || !reflect.DeepEqual(got.Bases, want) {
		t.Fatal("base metadata selected another same-name record", got, err)
	}
}

func TestDirectBaseMetadataValidation(t *testing.T) {
	for _, tc := range []struct{ name, access, native string }{
		{"missing_type", "public", ""},
		{"missing_access", "", "Base"},
		{"unknown_access", "invalid", "Base"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roots := memberFixture()
			base := memberBase{Access: tc.access}
			base.Type.QualType = tc.native
			roots[2].Bases = []memberBase{base}
			if got, err := selectedMembers(encodeMembers(t, roots)); err == nil || got != nil {
				t.Fatal("malformed base metadata accepted", got, err)
			}
		})
	}
}
