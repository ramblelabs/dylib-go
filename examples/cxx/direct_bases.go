package cxxexample

import (
	"context"
	"fmt"
	"strings"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// DiscoverDirectBases first obtains the selected class's direct base names.
// This fixture's names resolve inside direct_bases; other APIs may need explicit
// aliases for contextual, private or template spellings. ABI and ownership stay
// application choices, and DiscoverBases checks conversion eligibility.
func DiscoverDirectBases(ctx context.Context, header string, opts cxx.DiscoveryOptions) (*cxx.Facade, error) {
	const prefix = "direct_bases::"
	declaration, err := cxx.DiscoverMembers(ctx, header, prefix+"Alias", opts)
	if err != nil {
		return nil, err
	}
	names := []string{declaration.Name}
	for _, base := range declaration.Bases {
		if base.Access != "public" {
			continue
		}
		name := base.NativeType
		if !strings.HasPrefix(name, prefix) {
			name = prefix + name
		}
		names = append(names, name)
	}
	relations, err := cxx.DiscoverBases(ctx, header, names, opts)
	if err != nil {
		return nil, err
	}
	owner := cxx.Class{Name: declaration.Name, Delete: "direct_delete", Constructors: []cxx.Constructor{{Symbol: "direct_new", Args: []abi.Type{abi.I32}}}, Methods: []cxx.Method{{Name: "live", Symbol: "direct_live", Static: true, Signature: abi.Signature{Result: abi.I32}}}}
	for _, relation := range relations {
		if relation.Derived == owner.Name {
			owner.Bases = append(owner.Bases, cxx.Base{Name: relation.Base, Symbol: "direct_" + strings.ToLower(strings.TrimPrefix(relation.Base, prefix))})
		}
	}
	if len(owner.Bases) != 3 {
		return nil, fmt.Errorf("expected three public unambiguous direct bases")
	}
	return cxx.Generate(header, []cxx.Class{owner,
		{Name: prefix + "Counter", Methods: []cxx.Method{{Name: "score", Symbol: "direct_score", Const: true, Signature: abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}}}},
		{Name: prefix + "Virtual", Methods: []cxx.Method{{Name: "read", Symbol: "direct_read", Const: true, Signature: abi.Signature{Result: abi.I32}}}},
	})
}
