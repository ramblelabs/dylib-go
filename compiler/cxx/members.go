package cxx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// CallableKind distinguishes native declarations without interpreting their ABI.
type CallableKind string

const (
	ConstructorDeclaration CallableKind = "constructor"
	DestructorDeclaration  CallableKind = "destructor"
	MethodDeclaration      CallableKind = "method"
	ConversionDeclaration  CallableKind = "conversion"
)

// ParameterDeclaration preserves the compiler's native C++ spelling. NativeType
// is not a Go signature or a portable, independently resolvable type name.
type ParameterDeclaration struct {
	Name, NativeType string
	HasDefault       bool
}

// CallableDeclaration describes an explicitly declared public callable. Deleted
// declarations are reported for selection diagnostics, and cannot be invoked.
// NativeType preserves qualifiers, calling convention attributes and C++ types
// in the declaring class's context. It is not an abi.Signature.
type CallableDeclaration struct {
	Kind               CallableKind
	Name, NativeType   string
	Parameters         []ParameterDeclaration
	Static, Variadic   bool
	Deleted, Defaulted bool
}

// BaseDeclaration describes one direct base in base-specifier order. NativeType
// preserves the compiler's spelling in the selected class's context; it is not
// necessarily a qualified or independently accessible name. Access is the
// effective "public", "protected" or "private" inheritance access. Virtual
// describes this edge only. These fields do not prove an unambiguous conversion;
// use DiscoverBases or the native facade compiler after selecting usable names.
type BaseDeclaration struct {
	NativeType string
	Access     string
	Virtual    bool
}

// ClassDeclaration contains direct bases and public callables in declaration order.
// Name is the caller's selected name/alias, rather than a guessed canonical name.
type ClassDeclaration struct {
	Name      string
	Abstract  bool
	Bases     []BaseDeclaration
	Callables []CallableDeclaration
}

// DiscoverMembers queries one complete non-cv class name/alias using an optional
// Clang-compatible C++17 compiler. It excludes implicit, inherited, non-public
// and function-template declarations; named class-template specializations can
// be selected through aliases. Lambda aliases can be resolved through decltype
// factory/variable references, with at most eight declaration queries after the
// initial alias query. Direct bases include non-public edges for diagnostics,
// without inherited traversal or a promise of an unambiguous conversion.
// Native spellings are descriptive metadata only: the caller still chooses
// ABI signatures, ownership and C export names for Generate, whose native
// compiler checks the exact selected prototypes.
// Queries use the original header path and DiscoveryOptions target/flags, with
// the same IO and cancellation bounds as DiscoverBases. Errors return nil.
func DiscoverMembers(ctx context.Context, header, name string, opts DiscoveryOptions) (*ClassDeclaration, error) {
	record, err := discoverMemberDefinition(ctx, header, name, opts)
	if err != nil {
		return nil, err
	}
	return publicCallables(name, record)
}

// Both member catalogs locate the same complete definition, including aliases
// and anonymous closure references, without comparing IDs across invocations.
func discoverMemberDefinition(ctx context.Context, header, name string, opts DiscoveryOptions) (*memberNode, error) {
	if len(name) > maxDiscoveryName || !qualifiedName(name) {
		return nil, fmt.Errorf("cxx: invalid class name %q", name)
	}
	query, err := discoveryCommand(ctx, header, opts)
	if err != nil {
		return nil, err
	}
	source, namespace := memberProbe(name, "")
	data, err := query.run(ctx, source, namespace)
	if err != nil {
		return nil, err
	}
	first, err := decodeMemberAST(data, namespace)
	if err != nil {
		return nil, err
	}
	ref, err := first.selectedRecord()
	if err != nil {
		return nil, err
	}
	if ref.Name != "" && (!identifier(ref.Name) || len(ref.Name) > maxDiscoveryName) {
		return nil, fmt.Errorf("cxx: selected class requires a named record")
	}
	// Names and expression references select filters, not record identities.
	// Resolve the selected alias and definition together in each filtered query.
	// IDs from separate compiler processes must never be compared.
	filters := []string{ref.Name}
	anonymous := ref.Name == ""
	if anonymous {
		filters = first.references
	}
	seen := make(map[string]bool)
	for len(filters) != 0 && len(seen) < maxAnonymousMemberQueries {
		filter := filters[0]
		filters = filters[1:]
		if seen[filter] {
			continue
		}
		seen[filter] = true
		source, namespace = memberProbe(name, filter)
		data, err = query.run(ctx, source, filter)
		if err != nil {
			return nil, err
		}
		ast, err := decodeMemberAST(data, namespace)
		if err != nil {
			return nil, err
		}
		ref, err = ast.selectedRecord()
		if err != nil {
			return nil, err
		}
		definition, err := ast.definition(ref.ID)
		if anonymous && errors.Is(err, errMissingMemberDefinition) {
			filters = append(filters, ast.references...)
			continue
		}
		if err != nil {
			return nil, err
		}
		return definition, nil
	}
	return nil, fmt.Errorf("cxx: selected anonymous record has no definition within %d declaration queries", maxAnonymousMemberQueries)
}

func memberProbe(name, record string) (source, namespace string) {
	hash := sha256.Sum256([]byte(name))
	namespace = fmt.Sprintf("dylib_go_member_query_%s_%x", record, hash[:8])
	source = fmt.Sprintf("namespace %s {\nusing selected = ::%s;\nstatic_assert(__is_class(selected) && __is_convertible_to(selected *, void *) && sizeof(selected) > 0);\n}\n", namespace, name)
	return
}

type memberReference struct{ ID, Kind, Name string }

type memberBase struct {
	Access    string
	IsVirtual bool
	Type      struct{ QualType string }
}

type memberNode struct {
	DefaultArg                                    *memberReference `json:"defaultArg,omitempty"`
	IsParameterPack                               bool             `json:"isParameterPack,omitempty"`
	ReferencedDecl                                *memberReference
	OriginalNamespace, NominatedNamespace         *memberReference
	ID, Kind, Name, PreviousDecl, TagUsed, Access string
	StorageClass, ExplicitlyDefaulted, Init       string
	IsImplicit, CompleteDefinition                bool
	ExplicitlyDeleted, Variadic                   bool
	Type                                          struct{ QualType string }
	DefinitionData                                struct{ IsAbstract, IsLambda bool }
	Decl                                          *memberReference
	Bases                                         []memberBase
	Inner                                         []memberNode
}

type memberAST struct {
	unit       *memberNode
	probe      *memberNode
	records    map[string]*memberNode
	references []string
	namespaces []*memberNode
}

const (
	maxMemberDepth            = 256
	maxMemberNodes            = 150000
	maxAnonymousMemberQueries = 8
)

var errMissingMemberDefinition = errors.New("cxx: missing complete selected definition")

func decodeMemberAST(data []byte, namespace string) (*memberAST, error) {
	return decodeDeclarationAST(data, namespace, false)
}

func decodeHeaderAST(data []byte) (*memberAST, error) {
	return decodeDeclarationAST(data, "", true)
}

func decodeDeclarationAST(data []byte, namespace string, whole bool) (*memberAST, error) {
	if len(data) > maxDiscoveryAST {
		return nil, fmt.Errorf("cxx: member AST exceeds byte budget")
	}
	// Bound nesting before unmarshalling recursive nodes. Token reads the JSON
	// stream iteratively, including fields the typed decoder would otherwise skip.
	preflight := json.NewDecoder(bytes.NewReader(data))
	depth := 0
	for {
		token, err := preflight.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("cxx: invalid member AST: %w", err)
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{', '[':
				depth++
				if depth > 2*maxMemberDepth+64 { // Inner arrays/nodes, plus type/location fields.
					return nil, fmt.Errorf("cxx: member AST exceeds JSON depth budget")
				}
			case '}', ']':
				depth--
			}
		}
	}
	ast := &memberAST{records: make(map[string]*memberNode)}
	references := make(map[string]bool)
	decoder := json.NewDecoder(bytes.NewReader(data))
	nodes := 0
	var visit func(*memberNode, int) error
	visit = func(n *memberNode, depth int) error {
		nodes++
		if depth > maxMemberDepth || nodes > maxMemberNodes {
			return fmt.Errorf("cxx: member AST exceeds node/depth budget")
		}
		if !whole && n.Kind == "NamespaceDecl" && n.Name == namespace {
			if ast.probe != nil {
				return fmt.Errorf("cxx: duplicate member query namespace")
			}
			ast.probe = n
		}
		if whole && n.Kind == "TranslationUnitDecl" && depth != 0 {
			return fmt.Errorf("cxx: nested translation unit")
		}
		if n.Kind == "NamespaceDecl" {
			ast.namespaces = append(ast.namespaces, n)
		}
		// A decltype alias retains references to its factory or source variable.
		// Their filtered declarations expose anonymous closure definitions.
		filter := ""
		if r := n.ReferencedDecl; r != nil && (r.Kind == "FunctionDecl" || r.Kind == "VarDecl" || r.Kind == "CXXMethodDecl") {
			filter = r.Name
		} else if n.Kind == "MemberExpr" {
			filter = n.Name
		}
		if len(ast.references) < maxAnonymousMemberQueries && len(filter) <= maxDiscoveryName && identifier(filter) && !references[filter] {
			references[filter] = true
			ast.references = append(ast.references, filter)
		}
		if (n.Kind == "CXXRecordDecl" || n.Kind == "ClassTemplateSpecializationDecl") && n.ID != "" {
			if old := ast.records[n.ID]; old != nil {
				// Filtered dumps can repeat a specialization under its template
				// and as a root. Keep its one complete definition.
				if old.CompleteDefinition && n.CompleteDefinition {
					a, _ := json.Marshal(old)
					b, _ := json.Marshal(n)
					if !bytes.Equal(a, b) {
						return fmt.Errorf("cxx: conflicting record declarations")
					}
				}
				if !old.CompleteDefinition && n.CompleteDefinition {
					ast.records[n.ID] = n
				}
			} else {
				ast.records[n.ID] = n
			}
		}
		for i := range n.Inner {
			if err := visit(&n.Inner[i], depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for {
		n := new(memberNode)
		if err := decoder.Decode(n); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("cxx: invalid member AST: %w", err)
		}
		if whole {
			if ast.unit != nil || n.Kind != "TranslationUnitDecl" || n.ID == "" {
				return nil, fmt.Errorf("cxx: expected one identified translation unit")
			}
			ast.unit = n
		}
		if err := visit(n, 0); err != nil {
			return nil, err
		}
	}
	if whole {
		if ast.unit == nil {
			return nil, fmt.Errorf("cxx: missing translation unit")
		}
	} else if ast.probe == nil {
		return nil, fmt.Errorf("cxx: missing member query namespace")
	}
	return ast, nil
}

func (ast *memberAST) selectedRecord() (*memberReference, error) {
	var alias *memberNode
	for i := range ast.probe.Inner {
		n := &ast.probe.Inner[i]
		if n.Kind == "TypeAliasDecl" && n.Name == "selected" {
			if alias != nil {
				return nil, fmt.Errorf("cxx: duplicate selected class alias")
			}
			alias = n
		}
	}
	var ref *memberReference
	var walk func(*memberNode) error
	walk = func(n *memberNode) error {
		// Box<Argument> contains Argument's RecordType as well as Box's.
		// decltype expressions likewise contain argument/receiver records.
		if n.Kind == "TemplateArgument" || strings.HasSuffix(n.Kind, "Expr") {
			return nil
		}
		if n.Kind == "RecordType" && n.Decl != nil {
			if ref != nil && ref.ID != n.Decl.ID {
				return fmt.Errorf("cxx: ambiguous selected record")
			}
			ref = n.Decl
			return nil
		}
		for i := range n.Inner {
			if err := walk(&n.Inner[i]); err != nil {
				return err
			}
		}
		return nil
	}
	if alias != nil {
		if err := walk(alias); err != nil {
			return nil, err
		}
	}
	if ref == nil || ref.ID == "" || (ref.Kind != "CXXRecordDecl" && ref.Kind != "ClassTemplateSpecializationDecl") {
		return nil, fmt.Errorf("cxx: missing selected record reference")
	}
	return ref, nil
}

func (ast *memberAST) definition(id string) (*memberNode, error) {
	successors := make(map[string][]string)
	for key, record := range ast.records {
		if record.PreviousDecl != "" {
			successors[record.PreviousDecl] = append(successors[record.PreviousDecl], key)
		}
	}
	var result *memberNode
	seen := make(map[string]bool)
	queue := []string{id}
	for len(queue) != 0 {
		key := queue[0]
		queue = queue[1:]
		if seen[key] {
			return nil, fmt.Errorf("cxx: cyclic record redeclaration")
		}
		seen[key] = true
		if record := ast.records[key]; record != nil && record.CompleteDefinition {
			if result != nil {
				return nil, fmt.Errorf("cxx: ambiguous selected definition")
			}
			result = record
		}
		queue = append(queue, successors[key]...)
	}
	if result == nil {
		return nil, errMissingMemberDefinition
	}
	return result, nil
}

func publicCallables(name string, record *memberNode) (*ClassDeclaration, error) {
	nodes, err := publicMemberDeclarations(record)
	if err != nil {
		return nil, err
	}
	result := &ClassDeclaration{Name: name, Abstract: record.DefinitionData.IsAbstract}
	for _, base := range record.Bases {
		if base.Type.QualType == "" || base.Access != "public" && base.Access != "protected" && base.Access != "private" {
			return nil, fmt.Errorf("cxx: missing native base type or invalid inheritance access")
		}
		result.Bases = append(result.Bases, BaseDeclaration{NativeType: base.Type.QualType, Access: base.Access, Virtual: base.IsVirtual})
	}
	for _, n := range nodes {
		call, ok, err := callableDeclaration(n)
		if err != nil {
			return nil, err
		}
		if ok {
			result.Callables = append(result.Callables, call)
		}
	}
	return result, nil
}

func publicMemberDeclarations(record *memberNode) ([]memberNode, error) {
	access := "private"
	switch record.TagUsed {
	case "struct":
		access = "public"
	case "class":
		if record.DefinitionData.IsLambda {
			access = "public"
		}
	default:
		return nil, fmt.Errorf("cxx: selected definition is not a class/struct")
	}
	var nodes []memberNode
	for _, n := range record.Inner {
		if n.Kind == "AccessSpecDecl" {
			switch n.Access {
			case "public", "protected", "private":
				access = n.Access
			default:
				return nil, fmt.Errorf("cxx: invalid member access")
			}
			continue
		}
		if access == "public" && !n.IsImplicit {
			nodes = append(nodes, n)
		}
	}
	return nodes, nil
}

func callableDeclaration(n memberNode) (CallableDeclaration, bool, error) {
	var kind CallableKind
	switch n.Kind {
	case "CXXConstructorDecl":
		kind = ConstructorDeclaration
	case "CXXDestructorDecl":
		kind = DestructorDeclaration
	case "CXXMethodDecl":
		kind = MethodDeclaration
	case "CXXConversionDecl":
		kind = ConversionDeclaration
	default:
		return CallableDeclaration{}, false, nil
	}
	if n.Name == "" || n.Type.QualType == "" {
		return CallableDeclaration{}, false, fmt.Errorf("cxx: missing callable name or native type")
	}
	call := CallableDeclaration{Kind: kind, Name: n.Name, NativeType: n.Type.QualType,
		Static: n.StorageClass == "static", Variadic: n.Variadic,
		Deleted: n.ExplicitlyDeleted || n.ExplicitlyDefaulted == "deleted", Defaulted: n.ExplicitlyDefaulted != ""}
	for _, p := range n.Inner {
		if p.Kind == "ParmVarDecl" {
			if p.Type.QualType == "" {
				return CallableDeclaration{}, false, fmt.Errorf("cxx: missing native parameter type")
			}
			call.Parameters = append(call.Parameters, ParameterDeclaration{Name: p.Name, NativeType: p.Type.QualType, HasDefault: p.Init != ""})
		}
	}
	return call, true, nil
}
