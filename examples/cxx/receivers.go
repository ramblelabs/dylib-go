package cxxexample

import (
	"fmt"

	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cxx"
)

// ReceiverClasses selects all twelve cv/ref combinations. An && call changes
// only the expression's value category; the native method controls object state
// and the application keeps its handle until explicit destruction.
func ReceiverClasses() []cxx.Class {
	one := abi.Signature{Result: abi.I32, Args: []abi.Type{abi.I32}}
	read := abi.Signature{Result: abi.I32}
	receiver := cxx.Class{Name: "receivers::Receiver", Delete: "receiver_delete",
		Constructors: []cxx.Constructor{{Symbol: "receiver_new", Args: []abi.Type{abi.I32}}},
		Bases:        []cxx.Base{{Name: "receivers::Base", Symbol: "receiver_base"}}}
	for ref := cxx.RefNone; ref <= cxx.RefRValue; ref++ {
		for cv := 0; cv < 4; cv++ {
			name := "score"
			if ref == cxx.RefNone {
				name = "plain"
			}
			receiver.Methods = append(receiver.Methods, cxx.Method{
				Name: name, Symbol: fmt.Sprintf("receiver_call%d", int(ref)*4+cv),
				Signature: one, Const: cv&1 != 0, Volatile: cv&2 != 0, Ref: ref,
			})
		}
	}
	receiver.Methods = append(receiver.Methods,
		cxx.Method{Name: "mark", Symbol: "receiver_mark", Signature: abi.Signature{Result: abi.Void, Args: []abi.Type{abi.I32}}, Volatile: true, Ref: cxx.RefRValue},
		cxx.Method{Name: "read", Symbol: "receiver_read", Signature: read, Const: true, Volatile: true, Ref: cxx.RefLValue},
		cxx.Method{Name: "fail", Symbol: "receiver_fail", Signature: read, Ref: cxx.RefRValue},
		cxx.Method{Name: "take", Symbol: "receiver_take", Signature: abi.Signature{Result: abi.Pointer}, Ref: cxx.RefRValue,
			ObjectResult: &cxx.ObjectType{Name: "receivers::Result", Kind: cxx.ObjectValue}},
		cxx.Method{Name: "operator()", Symbol: "receiver_operator", Signature: one, Volatile: true, Ref: cxx.RefRValue},
	)
	return []cxx.Class{receiver,
		{Name: "receivers::Result", Delete: "result_delete", Methods: []cxx.Method{{Name: "read", Symbol: "result_read", Signature: read, Const: true}}},
		{Name: "receivers::Base", Methods: []cxx.Method{{Name: "virtual_score", Symbol: "base_virtual", Signature: one, Const: true, Volatile: true, Ref: cxx.RefRValue}}},
	}
}
