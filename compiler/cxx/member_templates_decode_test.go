package cxx

import "testing"

func memberTemplateFixture() *memberNode {
	n := templateFixture().namespaces[0].Inner[0]
	for i := range n.Inner {
		if n.Inner[i].Kind == "FunctionDecl" {
			n.Inner[i].Kind = "CXXMethodDecl"
		}
	}
	return &memberNode{Kind: "CXXRecordDecl", TagUsed: "struct", Inner: []memberNode{n}}
}

func TestMemberTemplateMetadataSelection(t *testing.T) {
	for _, name := range []string{"struct", "class", "public", "protected", "private", "lambda", "implicit", "nested", "ordinary", "duplicate", "overload", "constructor", "conversion", "static", "variadic", "deleted", "parameter_default"} {
		t.Run(name, func(t *testing.T) {
			r := memberTemplateFixture()
			n := &r.Inner[0]
			switch name {
			case "class":
				r.TagUsed = "class"
			case "public", "protected", "private":
				r.Inner = append([]memberNode{{Kind: "AccessSpecDecl", Access: name}}, r.Inner...)
			case "lambda":
				r.TagUsed = "class"
				r.DefinitionData.IsLambda = true
			case "implicit":
				n.IsImplicit = true
			case "nested":
				r.Inner = []memberNode{{Kind: "CXXRecordDecl", Inner: r.Inner}}
			case "ordinary":
				r.Inner = []memberNode{n.Inner[2]}
			case "duplicate":
				r.Inner = append(r.Inner, *n)
			case "overload":
				other := *n
				other.ID = "overload"
				r.Inner = append(r.Inner, other)
			case "constructor":
				n.Inner[2].Kind = "CXXConstructorDecl"
			case "conversion":
				n.Name = "operator type-parameter-0-0"
				n.Inner[2].Name = n.Name
				n.Inner[2].Kind = "CXXConversionDecl"
			case "static":
				n.Inner[2].StorageClass = "static"
			case "variadic":
				n.Inner[2].Variadic = true
			case "deleted":
				n.Inner[2].ExplicitlyDeleted = true
			case "parameter_default":
				n.Inner[2].Inner[0].Init = "c"
			}
			got, err := publicMemberTemplates(r)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			switch name {
			case "class", "protected", "private", "implicit", "nested", "ordinary":
				want = 0
			case "overload":
				want = 2
			}
			if len(got) != want {
				t.Fatal(got, want)
			}
			if want == 0 {
				return
			}
			d := got[0]
			if len(d.Parameters) != 2 || d.Callable.NativeType != "T (T)" || len(d.Callable.Parameters) != 1 {
				t.Fatal(d)
			}
			if name == "constructor" && d.Callable.Kind != ConstructorDeclaration || name == "conversion" && d.Callable.Kind != ConversionDeclaration || name == "static" && !d.Callable.Static || name == "variadic" && !d.Callable.Variadic || name == "deleted" && !d.Callable.Deleted || name == "parameter_default" && !d.Callable.Parameters[0].HasDefault {
				t.Fatal(d)
			}
		})
	}
}

func TestMemberTemplateMetadataValidation(t *testing.T) {
	for name, patch := range map[string]func(*memberNode){
		"tag":                  func(r *memberNode) { r.TagUsed = "union" },
		"access":               func(r *memberNode) { r.Inner = append(r.Inner, memberNode{Kind: "AccessSpecDecl", Access: "wrong"}) },
		"missing_id":           func(r *memberNode) { r.Inner[0].ID = "" },
		"missing_primary":      func(r *memberNode) { r.Inner[0].Inner = r.Inner[0].Inner[:2] },
		"specialization_first": func(r *memberNode) { n := &r.Inner[0]; n.Inner[2], n.Inner[3] = n.Inner[3], n.Inner[2] },
		"wrong_kind":           func(r *memberNode) { n := &r.Inner[0]; n.Inner = n.Inner[:3]; n.Inner[2].Kind = "CXXDestructorDecl" },
		"primary_id":           func(r *memberNode) { r.Inner[0].Inner[2].ID = "" },
		"primary_name":         func(r *memberNode) { r.Inner[0].Inner[2].Name = "wrong" },
		"primary_implicit":     func(r *memberNode) { r.Inner[0].Inner[2].IsImplicit = true },
		"primary_type":         func(r *memberNode) { r.Inner[0].Inner[2].Type.QualType = "" },
		"parameter_type":       func(r *memberNode) { r.Inner[0].Inner[2].Inner[0].Type.QualType = "" },
		"template_parameters":  func(r *memberNode) { r.Inner[0].Inner = r.Inner[0].Inner[2:] },
		"template_value_type":  func(r *memberNode) { r.Inner[0].Inner[1].Type.QualType = "" },
		"conflicting_identity": func(r *memberNode) {
			n := r.Inner[0]
			n.Inner = append([]memberNode(nil), n.Inner...)
			n.Inner[2].StorageClass = "static"
			r.Inner = append(r.Inner, n)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := memberTemplateFixture()
			patch(r)
			if v, err := publicMemberTemplates(r); err == nil || v != nil {
				t.Fatal(v, err)
			}
		})
	}
}

func TestTemplateInventedParameterNames(t *testing.T) {
	for _, tc := range []struct {
		name, kind            string
		implicit, pack, valid bool
		want                  string
	}{
		{"value:auto", "TemplateTypeParmDecl", true, false, true, ""},
		{"values:auto", "TemplateTypeParmDecl", true, true, true, ""},
		{"T", "TemplateTypeParmDecl", false, false, true, "T"},
		{"", "TemplateTypeParmDecl", true, false, true, ""},
		{"value:auto", "TemplateTypeParmDecl", false, false, false, ""},
		{"value:auto", "NonTypeTemplateParmDecl", true, false, false, ""},
		{"auto:1", "TemplateTypeParmDecl", true, false, true, ""},
		{"bad;", "TemplateTypeParmDecl", false, false, false, ""},
	} {
		t.Run(tc.name+"/"+tc.kind+"/"+map[bool]string{true: "implicit", false: "explicit"}[tc.implicit], func(t *testing.T) {
			n := memberNode{Kind: tc.kind, Name: tc.name, IsImplicit: tc.implicit, IsParameterPack: tc.pack}
			n.Type.QualType = "int"
			count := 0
			got, err := templateParameters([]memberNode{n}, &count)
			if !tc.valid {
				if err == nil || got != nil {
					t.Fatal(got, err)
				}
				return
			}
			if err != nil || len(got) != 1 || got[0].Name != tc.want || got[0].Pack != tc.pack {
				t.Fatal(got, err)
			}
		})
	}
}
