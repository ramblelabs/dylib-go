package module

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	dylib "github.com/ramblelabs/dylib-go"
)

// InputArtifact names one loadable compiler result. Owner, when non-nil,
// transfers to the graph even when the compiler also returns an error. It must
// be fresh for this invocation. Its Close must support retry after failure.
// Path must be absolute on success. An unchanged native input may return its
// original path with no owner. Original dependency directories still belong in
// dylib.Options.LibraryPaths; temporary outputs do not choose library providers.
type InputArtifact struct {
	Path  string
	Owner io.Closer
}

// InputCompiler optionally compiles one absolute input path. It runs once per
// selected file, in provider order, before native loading starts. It may pass
// native files through unchanged. It owns resources it has not returned.
// Report failures through errors; panic/Goexit propagate after best-effort
// cleanup of earlier artifacts. No compiler or language is selected implicitly.
type InputCompiler func(context.Context, string) (InputArtifact, error)

// OpenCompiled validates a manifest, prepares only its selected inputs, then
// opens the resulting graph. The explicit compiler may compile source, IR or
// archives, or pass native inputs through. Selected manifest records are copied
// before callbacks; native files and options must stay valid during the call.
//
// Context controls preparation and the transition to Open, not the OS loader or
// native initialization. A preparation error opens no native images. Successful
// graphs close all native sessions before removing compiler artifacts. Failed
// cleanup returns a non-nil retired graph whose Close must be retried, as with
// Open. Native initialization effects still cannot be undone automatically.
func OpenCompiled(ctx context.Context, base string, manifest Manifest, options dylib.Options, compile InputCompiler) (*Graph, error) {
	if ctx == nil || compile == nil {
		return nil, fmt.Errorf("module: context and input compiler are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	order, err := manifest.order()
	if err != nil {
		return nil, err
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	plan := Manifest{Format: manifest.Format, Roots: append([]string(nil), manifest.Roots...)}
	for _, index := range order {
		mod := manifest.Modules[index]
		mod.Files = append([]string(nil), mod.Files...)
		mod.Exports = append([]string(nil), mod.Exports...)
		mod.Imports = append([]Import(nil), mod.Imports...)
		for i, path := range mod.Files {
			if !filepath.IsAbs(path) {
				mod.Files[i] = filepath.Join(base, path)
			}
		}
		plan.Modules = append(plan.Modules, mod)
	}
	// Before Open, this retired owner only contains preparation resources. Keep
	// its native sessions last so reverse cleanup always unloads them first.
	prepared := &Graph{retired: true}
	for mi := range plan.Modules {
		mod := &plan.Modules[mi]
		for fi, path := range mod.Files {
			if err := ctx.Err(); err != nil {
				return prepared.loadFailure(mod.Name, err)
			}
			artifact, err := callInputCompiler(ctx, path, compile, prepared)
			if artifact.Owner != nil {
				prepared.order = append(prepared.order, artifact.Owner)
			}
			if err == nil && !filepath.IsAbs(artifact.Path) {
				err = fmt.Errorf("compiler must return an absolute loadable path")
			}
			err = errors.Join(err, ctx.Err())
			if err != nil {
				return prepared.loadFailure(mod.Name, fmt.Errorf("compile %q: %w", path, err))
			}
			mod.Files[fi] = artifact.Path
		}
	}
	if err := ctx.Err(); err != nil {
		return prepared.loadFailure("preparation", err)
	}
	graph, err := Open(base, plan, options)
	if graph == nil {
		return prepared.loadFailure("load compiled graph", err)
	}
	graph.order = append(prepared.order, graph.order...)
	return graph, err
}

func callInputCompiler(ctx context.Context, path string, compile InputCompiler, prepared *Graph) (InputArtifact, error) {
	returned := false
	defer func() {
		if !returned {
			prepared.Close()
		}
	}()
	artifact, err := compile(ctx, path)
	returned = true
	return artifact, err
}
