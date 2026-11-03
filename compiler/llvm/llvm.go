// Package llvm optionally compiles LLVM IR/bitcode to native object files using
// the external llc tool. It does not add an LLVM dependency to the loader.
package llvm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	dylib "github.com/ramblelabs/dylib-go"
)

const maxInputSize = 256 << 20
const maxDiagnosticSize = 32 << 10

// Options selects the installed LLVM compiler and temporary storage. LLVM's
// reader must support the producer's IR/bitcode version. No target override is
// passed: llc preserves the module target and data layout. Target-less modules
// use llc's default target; Session.Load still checks host compatibility.
type Options struct {
	Compiler string // llc executable, default "llc"; never a shell command.
	TempDir  string // Parent for owned temporary files, default OS temporary dir.
}

// Object owns a compiled object file. Load Path into a session before Close;
// the session snapshots native object bytes and can outlive this artifact.
// Keep directory-dependent imports available through dylib.Options.LibraryPaths.
// Path must not be read concurrently with Close. Do not copy Object.
type Object struct {
	Path string
	Info dylib.Info
	// SourceDirectories lists unique absolute input directories in input order.
	// Use them explicitly with dylib.Options.LibraryPaths for original dependencies.
	SourceDirectories []string
	storage           *artifactStorage
}

type artifactStorage struct {
	mu  sync.Mutex
	dir string
}

// Close deletes compiler input/output storage. It does not unload a session
// that has already loaded the object. Close is idempotent and retries failures.
func (o *Object) Close() error {
	if o == nil {
		return nil
	}
	return o.storage.close()
}

func (a *artifactStorage) close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dir == "" {
		return nil
	}
	if err := os.RemoveAll(a.dir); err != nil {
		return err
	}
	a.dir = ""
	return nil
}

// Compile snapshots a regular IR/bitcode file and emits PIC native object code.
// It does not infer C signatures, link language runtimes, or execute the input.
// The normal loader's target, relocation and runtime checks still apply.
// On error, a non-nil result owns failed cleanup only: retry Close rather than
// loading its Path or Info as a valid compiled object.
func Compile(ctx context.Context, path string, opts Options) (result *Object, err error) {
	if ctx == nil {
		return nil, fmt.Errorf("llvm: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	source, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	stat, err := source.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > maxInputSize {
		return nil, fmt.Errorf("llvm: expected regular input of at most %d bytes", maxInputSize)
	}
	dir, err := makeTempDir(opts.TempDir, "dylib-go-llvm-")
	if err != nil {
		return nil, err
	}
	object := &Object{storage: &artifactStorage{dir: dir}, Path: filepath.Join(dir, "output.o"), SourceDirectories: sourceDirectories([]string{path})}
	defer func() {
		if err != nil {
			if cleanup := object.Close(); cleanup != nil {
				result = object
				err = errors.Join(err, fmt.Errorf("llvm: temporary cleanup: %w", cleanup))
			}
		}
	}()
	input := filepath.Join(dir, "input")
	snapshot, err := os.Create(input)
	if err != nil {
		return nil, err
	}
	size, copyErr := io.Copy(snapshot, io.LimitReader(source, maxInputSize+1))
	closeErr := snapshot.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if size > maxInputSize {
		return nil, fmt.Errorf("llvm: input grew beyond %d bytes", maxInputSize)
	}
	tool := opts.Compiler
	if tool == "" {
		tool = "llc"
	}
	cmd := exec.CommandContext(ctx, tool, "-filetype=obj", "-relocation-model=pic", "-o", object.Path, input)
	var diagnostics limitedDiagnostics
	cmd.Stdout, cmd.Stderr = &diagnostics, &diagnostics
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("llvm: %s: %w\n%s", tool, err, diagnostics.String())
	}
	object.Info, err = dylib.Inspect(object.Path)
	if err != nil {
		return nil, fmt.Errorf("llvm: invalid compiler output: %w", err)
	}
	if object.Info.Kind != "object" {
		return nil, fmt.Errorf("llvm: compiler output is %s, expected object", object.Info.Kind)
	}
	return object, nil
}

func sourceDirectories(paths []string) []string {
	var directories []string
	seen := make(map[string]bool)
	for _, path := range paths {
		directory := filepath.Dir(path)
		if !seen[directory] {
			seen[directory] = true
			directories = append(directories, directory)
		}
	}
	return directories
}

func makeTempDir(parent, prefix string) (string, error) {
	if parent == "" {
		parent = os.TempDir()
	}
	parent, err := filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, prefix)
}

type limitedDiagnostics struct {
	buffer    bytes.Buffer
	truncated bool
}

func (d *limitedDiagnostics) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := maxDiagnosticSize - d.buffer.Len(); n > remaining {
		p, d.truncated = p[:remaining], true
	}
	d.buffer.Write(p)
	return n, nil
}

func (d *limitedDiagnostics) String() string {
	if d.truncated {
		return d.buffer.String() + "\n[diagnostics truncated]"
	}
	return d.buffer.String()
}
