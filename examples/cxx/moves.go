package cxxexample

import (
	"fmt"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// MoveClasses selects exact pointer/lvalue/rvalue argument and result types.
// Native moves keep their source handles; destroy each owned source/destination
// explicitly and keep owners alive while using borrowed reference results.
func MoveClasses() []cxx.Class {
	argument := func(kind cxx.ObjectKind, cv int) cxx.ObjectType {
		return cxx.ObjectType{Name: "moves::Box", Kind: kind, Const: cv&1 != 0, Volatile: cv&2 != 0}
	}
	object := argument(cxx.ObjectRValueReference, 0)
	integer := abi.Signature{Result: abi.I32, Args: []abi.Type{abi.Pointer}}
	box := cxx.Class{Name: "moves::Box", Delete: "move_delete",
		Constructors: []cxx.Constructor{
			{Symbol: "move_new", Args: []abi.Type{abi.I32}},
			{Symbol: "move_construct", Args: []abi.Type{abi.Pointer}, ObjectArgs: []cxx.ObjectArg{{Index: 0, Type: object}}},
		},
		Methods: []cxx.Method{
			{Name: "live", Symbol: "move_live", Static: true, Signature: abi.Signature{Result: abi.I32}},
			{Name: "read", Symbol: "move_read", Const: true, Volatile: true, Signature: abi.Signature{Result: abi.I32}},
			{Name: "consume", Symbol: "move_consume", Static: true, Signature: integer, ObjectArgs: []cxx.ObjectArg{{Index: 0, Type: object}}},
		}}
	for i, kind := range []cxx.ObjectKind{cxx.ObjectPointer, cxx.ObjectReference, cxx.ObjectRValueReference} {
		for cv := 0; cv < 4; cv++ {
			box.Methods = append(box.Methods, cxx.Method{Name: "probe", Symbol: fmt.Sprintf("move_probe%d", i*4+cv), Static: true,
				Signature: integer, ObjectArgs: []cxx.ObjectArg{{Index: 0, Type: argument(kind, cv)}}})
		}
	}
	for cv := 0; cv < 4; cv++ {
		result := argument(cxx.ObjectRValueReference, cv)
		box.Methods = append(box.Methods, cxx.Method{
			Name: []string{"self", "const_self", "volatile_self", "cv_self"}[cv], Symbol: fmt.Sprintf("move_self%d", cv),
			Const: cv&1 != 0, Volatile: cv&2 != 0, Signature: abi.Signature{Result: abi.Pointer}, ObjectResult: &result,
		})
		pointer := argument(cxx.ObjectPointer, cv)
		box.Methods = append(box.Methods, cxx.Method{
			Name: []string{"pointer", "const_pointer", "volatile_pointer", "cv_pointer"}[cv], Symbol: fmt.Sprintf("move_pointer%d", cv), Static: true,
			Signature: abi.Signature{Result: abi.Pointer, Args: []abi.Type{abi.Pointer}}, ObjectResult: &pointer, ObjectArgs: []cxx.ObjectArg{{Index: 0, Type: pointer}},
		})
	}
	number, row := cxx.ObjectType{Name: "moves::Number", Kind: cxx.ObjectRValueReference}, cxx.ObjectType{Name: "moves::Row", Kind: cxx.ObjectRValueReference}
	box.Methods = append(box.Methods,
		cxx.Method{Name: "number", Symbol: "move_number", Static: true, Signature: abi.Signature{Result: abi.Pointer, Args: []abi.Type{abi.Pointer}},
			ObjectArgs: []cxx.ObjectArg{{Index: 0, Type: number}}, ObjectResult: &number},
		cxx.Method{Name: "row", Symbol: "move_row", Signature: abi.Signature{Result: abi.Pointer}, ObjectResult: &row},
		cxx.Method{Name: "sum", Symbol: "move_sum", Static: true, Signature: integer, ObjectArgs: []cxx.ObjectArg{{Index: 0, Type: row}}},
	)
	return []cxx.Class{box}
}
