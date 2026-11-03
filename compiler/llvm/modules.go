package llvm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const maxModules = 256

// MergeOptions selects tools and storage for explicit module merging. Linker
// defaults to llvm-link; it must be compatible with the compiler and producers.
type MergeOptions struct {
	Options
	Linker string // llvm-link executable; never a shell command.
}

// CompileModules snapshots and eagerly merges text IR/raw/wrapped bitcode modules,
// then compiles them into one owned native Object. Every module must declare the
// same explicit target triple and data layout. No target override, archive member
// selection, language runtime linking, or ABI translation is performed.
// Inputs and normalized IR each have a combined 256 MiB limit, with 1–256 modules.
// On error, a non-nil result owns the whole merge tree for Close retry only;
// its Path and Info do not describe a valid compiled object.
func CompileModules(ctx context.Context, paths []string, opts MergeOptions) (result *Object, err error) {
	if ctx == nil {
		return nil, fmt.Errorf("llvm: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(paths) == 0 || len(paths) > maxModules {
		return nil, fmt.Errorf("llvm: expected 1–%d explicit modules", maxModules)
	}
	dir, err := makeTempDir(opts.TempDir, "dylib-go-llvm-merge-")
	if err != nil {
		return nil, err
	}
	storage := &artifactStorage{dir: dir}
	defer func() {
		if err != nil {
			if cleanup := storage.close(); cleanup != nil {
				result = &Object{storage: storage}
				err = errors.Join(err, fmt.Errorf("llvm: temporary cleanup: %w", cleanup))
			}
		}
	}()
	inputs, sources, err := stageModules(ctx, paths, dir, maxInputSize)
	if err != nil {
		return nil, err
	}
	linker := opts.Linker
	if linker == "" {
		linker = "llvm-link"
	}
	linker, err = exec.LookPath(linker)
	if err != nil {
		return nil, err
	}
	// Resolve relative executable paths before setting the owned working directory.
	linker, err = filepath.Abs(linker)
	if err != nil {
		return nil, err
	}
	remaining := int64(maxInputSize)
	var target moduleTarget
	normalized := make([]string, len(inputs))
	for i, input := range inputs {
		normalized[i] = fmt.Sprintf("module-%03d.ll", i)
		// Let LLVM validate and print each module independently before any
		// cross-module link. llvm-link otherwise only warns on target mismatches.
		if err := runModuleLinker(ctx, linker, dir, normalized[i], input); err != nil {
			return nil, fmt.Errorf("llvm: module %d (%q): %w", i, paths[i], err)
		}
		data, err := readRegular(filepath.Join(dir, normalized[i]), remaining)
		if err != nil {
			return nil, err
		}
		remaining -= int64(len(data))
		current, err := canonicalModuleTarget(data)
		if err != nil {
			return nil, fmt.Errorf("llvm: module %d (%q): %w", i, paths[i], err)
		}
		if i == 0 {
			target = current
		} else if current != target {
			return nil, fmt.Errorf("llvm: module %d (%q): target triple/data layout mismatch: %s / %s, expected %s / %s", i, paths[i], current.triple, current.layout, target.triple, target.layout)
		}
	}
	const merged = "merged.ll"
	if err := runModuleLinker(ctx, linker, dir, merged, normalized...); err != nil {
		return nil, err
	}
	data, err := readRegular(filepath.Join(dir, merged), maxInputSize)
	if err != nil {
		return nil, err
	}
	mergedTarget, err := canonicalModuleTarget(data)
	if err != nil {
		return nil, err
	}
	if mergedTarget != target {
		return nil, fmt.Errorf("llvm: merged module changed target triple/data layout")
	}
	object, err := Compile(ctx, filepath.Join(dir, merged), Options{Compiler: opts.Compiler, TempDir: dir})
	if err != nil {
		return nil, err
	}
	// The compiled object's directory is nested inside merge storage. Transfer
	// ownership of the whole tree so Close also removes snapshots/linker output.
	object.storage = storage
	object.SourceDirectories = sourceDirectories(sources)
	return object, nil
}

func stageModules(ctx context.Context, paths []string, dir string, remaining int64) ([]string, []string, error) {
	inputs, sources := make([]string, len(paths)), make([]string, len(paths))
	for i, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		path, err := filepath.Abs(path)
		if err != nil {
			return nil, nil, err
		}
		data, err := readRegular(path, remaining)
		if err != nil {
			return nil, nil, err
		}
		if bytes.HasPrefix(data, []byte("!<arch>\n")) || bytes.HasPrefix(data, []byte("!<thin>\n")) {
			return nil, nil, fmt.Errorf("llvm: module %d (%q): expected module, not archive", i, path)
		}
		remaining -= int64(len(data))
		inputs[i], sources[i] = fmt.Sprintf("input-%03d", i), path
		if err := os.WriteFile(filepath.Join(dir, inputs[i]), data, 0600); err != nil {
			return nil, nil, err
		}
	}
	return inputs, sources, nil
}

func runModuleLinker(ctx context.Context, linker, dir, output string, inputs ...string) error {
	// Indexed relative names bound the command line even on Windows; caller
	// paths (including option-like names) never become linker arguments.
	args := append([]string{"-S", "-o", output}, inputs...)
	cmd := exec.CommandContext(ctx, linker, args...)
	cmd.Dir = dir
	var diagnostics limitedDiagnostics
	cmd.Stdout, cmd.Stderr = &diagnostics, &diagnostics
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("llvm: %s: %w\n%s", linker, err, diagnostics.String())
	}
	return ctx.Err()
}

type moduleTarget struct{ triple, layout string }

// Read only canonical headers printed by LLVM, never arbitrary input syntax.
// Keeping LLVM's quoted strings makes equality exact, including escaping.
func canonicalModuleTarget(data []byte) (moduleTarget, error) {
	var target moduleTarget
	for len(data) != 0 {
		line, rest, _ := bytes.Cut(data, []byte{'\n'})
		data = rest
		for _, header := range []struct {
			prefix string
			value  *string
		}{{"target triple = ", &target.triple}, {"target datalayout = ", &target.layout}} {
			if !bytes.HasPrefix(line, []byte(header.prefix)) {
				continue
			}
			value := bytes.TrimSpace(line[len(header.prefix):])
			if len(value) <= 2 || value[0] != '"' || value[len(value)-1] != '"' || *header.value != "" {
				return moduleTarget{}, fmt.Errorf("invalid canonical target header")
			}
			*header.value = string(value)
		}
	}
	if target.triple == "" || target.layout == "" {
		return moduleTarget{}, fmt.Errorf("explicit target triple and data layout are required for module merging")
	}
	return target, nil
}
