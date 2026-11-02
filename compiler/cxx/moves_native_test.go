//go:build cgo && (linux || darwin || windows)

package cxx_test

import (
	"testing"

	"github.com/ramblelabs/dylib-go/compiler/cxx"
	cxxexample "github.com/ramblelabs/dylib-go/examples/cxx"
)

func generateMoves(header string) (*cxx.Facade, error) {
	return cxx.Generate(header, cxxexample.MoveClasses())
}

func TestNativeGeneratedCgoMoveObjectFacade(t *testing.T) {
	testGeneratedFacadeHeader(t, false, "testdata/moves.hpp", "testdata/moves_main.go", generateMoves)
}

func TestNativeGeneratedDirectLLGoMoveObjectFacade(t *testing.T) {
	if !llgoHost {
		t.Skip("the direct caller requires an llgo host")
	}
	testGeneratedFacadeHeader(t, true, "testdata/moves.hpp", "testdata/moves_main.go", generateMoves)
}
