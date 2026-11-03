package llvm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/internal/ar"
)

const maxArchiveMembers = 4096

// Archive owns a native archive generated from bitcode/native object members.
// Load Path before Close; session snapshots outlive the compiler files. Member
// names and order are retained, including duplicate names. The native loader
// rebuilds its symbol index and selects members on demand at Link.
type Archive struct {
	Path string
	Info dylib.Info
	// SourceDirectories lists unique original dependency directories in member
	// order, adding the input container's directory if absent. Set
	// dylib.Options.LibraryPaths explicitly; compilation does not copy DLLs or
	// choose among conflicting DLLs with the same basename. The paths outlive
	// compilation storage.
	SourceDirectories []string
	storage           *artifactStorage
}

// Close removes owned compiler files without unloading a session. It is
// idempotent and retries failures. Do not read Path concurrently with Close.
func (a *Archive) Close() error {
	if a == nil {
		return nil
	}
	return a.storage.close()
}

// CompileArchive converts a GNU/COFF/BSD or GNU thin archive into a native ar
// container. Each bitcode member is compiled independently using Compile;
// relocatable native members are preserved. Modules are never merged, so
// unused members' unresolved symbols and initializers remain unselected.
// Thin inputs snapshot external objects and regular archive proxies. Nested
// archives and text IR members are not accepted. Use SourceDirectories with
// dylib.Options.LibraryPaths on the destination session for original dependencies.
// The input/output limits are 256 MiB with at most 4096 object members.
// On error, a non-nil result owns failed cleanup only: retry Close rather than
// loading its Path or Info as a valid compiled archive.
func CompileArchive(ctx context.Context, path string, opts Options) (result *Archive, err error) {
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
	data, err := readRegular(path, maxInputSize)
	if err != nil {
		return nil, err
	}
	var entries []ar.Entry
	if bytes.HasPrefix(data, []byte("!<thin>\n")) {
		entries, err = ar.Snapshot(ctx, path, data, maxInputSize, maxArchiveMembers, readRegular)
	} else {
		entries, err = ar.Decode(data, false, maxInputSize, maxArchiveMembers)
	}
	if err != nil {
		return nil, fmt.Errorf("llvm: archive: %w", err)
	}
	dir, err := makeTempDir(opts.TempDir, "dylib-go-llvm-ar-")
	if err != nil {
		return nil, err
	}
	archive := &Archive{Path: filepath.Join(dir, "output.a"), storage: &artifactStorage{dir: dir}}
	var sources []string
	for _, entry := range entries {
		if entry.Source != "" {
			sources = append(sources, entry.Source)
		}
	}
	archive.SourceDirectories = sourceDirectories(append(sources, path))
	defer func() {
		if err != nil {
			if cleanup := archive.Close(); cleanup != nil {
				result = archive
				err = errors.Join(err, fmt.Errorf("llvm: temporary cleanup: %w", cleanup))
			}
		}
	}()
	output, err := os.Create(archive.Path)
	if err != nil {
		return nil, err
	}
	defer output.Close() // Close before removing storage on failure, including Windows.
	writer := archiveWriter{output: output, remaining: maxInputSize}
	if err := writer.write([]byte("!<arch>\n")); err != nil {
		return nil, err
	}
	for i, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.DisplayName != "" {
			entry.Name = entry.DisplayName
		}
		// An archive name is metadata, never an extraction path. Indexes keep
		// duplicate names distinct and cannot escape the owned directory.
		input := filepath.Join(dir, fmt.Sprintf("member-%06d", i))
		if err := os.WriteFile(input, entry.Data, 0600); err != nil {
			return nil, err
		}
		contents := entry.Data
		if bytes.HasPrefix(contents, []byte("!<arch>\n")) || bytes.HasPrefix(contents, []byte("!<thin>\n")) {
			return nil, fmt.Errorf("llvm: archive member %d (%q): nested archives are unsupported", i, entry.Name)
		}
		if bitcode(contents) {
			member, err := Compile(ctx, input, Options{Compiler: opts.Compiler, TempDir: dir})
			if err != nil {
				return nil, fmt.Errorf("llvm: archive member %d (%q): %w", i, entry.Name, err)
			}
			contents, err = readRegular(member.Path, writer.remaining)
			cleanup := member.Close()
			if err != nil || cleanup != nil {
				return nil, errors.Join(err, cleanup)
			}
		} else {
			info, err := dylib.Inspect(input)
			if err != nil {
				return nil, fmt.Errorf("llvm: archive member %d (%q): %w", i, entry.Name, err)
			}
			if info.Kind != "object" {
				return nil, fmt.Errorf("llvm: archive member %d (%q) is %s, expected object or bitcode", i, entry.Name, info.Kind)
			}
		}
		if err := writer.member(entry.Name, contents); err != nil {
			return nil, err
		}
		if err := os.Remove(input); err != nil {
			return nil, err
		}
	}
	if err := output.Close(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	archive.Info, err = dylib.Inspect(archive.Path)
	if err != nil {
		return nil, fmt.Errorf("llvm: invalid compiled archive: %w", err)
	}
	return archive, nil
}

func bitcode(data []byte) bool {
	return bytes.HasPrefix(data, []byte("BC\xc0\xde")) || bytes.HasPrefix(data, []byte{0xde, 0xc0, 0x17, 0x0b})
}

func readRegular(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > limit {
		return nil, fmt.Errorf("llvm: %s: expected regular input of at most %d bytes", path, limit)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("llvm: %s: input grew beyond %d bytes", path, limit)
	}
	return data, nil
}

// BSD extended names encode arbitrary member names without truncation or path
// interpretation. No stale bitcode symbol index is copied: the loader indexes
// native member symbols itself. External linkers may require ranlib afterward.
type archiveWriter struct {
	output    io.Writer
	remaining int64
}

func (w *archiveWriter) write(data []byte) error {
	if int64(len(data)) > w.remaining {
		return fmt.Errorf("llvm: compiled archive exceeds output limit")
	}
	n, err := w.output.Write(data)
	w.remaining -= int64(n)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}

func (w *archiveWriter) member(name string, data []byte) error {
	size := int64(len(name)) + int64(len(data))
	if size+60+(size&1) > w.remaining {
		return fmt.Errorf("llvm: compiled archive exceeds output limit")
	}
	header := fmt.Sprintf("%-16s%-12s%-6s%-6s%-8s%-10d`\n", fmt.Sprintf("#1/%d", len(name)), "0", "0", "0", "644", size)
	for _, part := range [][]byte{[]byte(header), []byte(name), data} {
		if err := w.write(part); err != nil {
			return err
		}
	}
	if size&1 != 0 {
		return w.write([]byte{'\n'})
	}
	return nil
}
