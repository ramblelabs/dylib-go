package moduleexample

import (
	"context"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/compiler/llvm"
	"github.com/ramblelabs/dylib-go/module"
)

// OpenLLVM compiles each selected IR/bitcode input before loading its graph.
// The graph owns temporary compiler files until all native sessions close.
// The installed llc must understand the inputs and preserve their host target.
// Original dependency directories remain explicit in options.LibraryPaths.
func OpenLLVM(ctx context.Context, base string, manifest module.Manifest, options dylib.Options, compiler llvm.Options) (*module.Graph, error) {
	return module.OpenCompiled(ctx, base, manifest, options, func(ctx context.Context, path string) (module.InputArtifact, error) {
		object, err := llvm.Compile(ctx, path, compiler)
		if object == nil {
			return module.InputArtifact{}, err
		}
		return module.InputArtifact{Path: object.Path, Owner: object}, err
	})
}
