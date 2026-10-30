package dylib

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ramblelabs/dylib-go/internal/ar"
)

// COFFImportSource identifies a raw object or an explicitly selected archive
// input to one bounded ordinary COFF import directory.
type COFFImportSource struct {
	Path string
	// Members is nil for a raw object. A non-nil slice selects regular or thin
	// archive members by zero-based position, excluding index/name-table entries.
	// It must be nonempty with distinct indices. Names may be duplicated.
	Members []int
}

// LoadCOFFImportDirectory stages an explicitly bounded, ordered set of raw COFF
// objects forming one complete ordinary import directory. Unlike Load, it can
// join metadata with no relocation connecting its owners, such as a separate
// .idata$3 terminator. Paths must be distinct and not previously loaded.
//
// All objects are validated and staged together, in path order. Archives and
// shared libraries are not accepted. Ordinary sections and their dependencies
// retain normal Link behavior. Failure leaves the session unchanged and opens
// no DLL. DLL search starts from each IAT owner's directory after LibraryPaths.
func (s *Session) LoadCOFFImportDirectory(paths ...string) error {
	return s.LoadCOFFImportDirectoryFrom(coffImportSources(paths)...)
}

// LoadCOFFImportDirectoryFrom stages an ordered, explicit selection of raw
// objects and archive members forming one complete ordinary import directory.
// Sources follow argument order, and each Members slice controls member order.
// All selected members become roots; unselected members are neither parsed nor
// available for later lazy extraction. Each source path is consumed as a whole,
// so subsequent Load calls for that path are idempotent, as for raw inputs.
// Stage other ordinary dependencies separately. Validation failure stages
// nothing and opens no DLL. Thin imports search from the external IAT owner.
func (s *Session) LoadCOFFImportDirectoryFrom(sources ...COFFImportSource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.mutable(); err != nil {
		return err
	}
	files, absolute, err := readCOFFImportDirectorySources(sources)
	if err != nil {
		return err
	}
	for _, path := range absolute {
		if s.paths[path] {
			return fmt.Errorf("COFF import directory input already loaded: %s", path)
		}
	}
	for _, file := range files {
		if !s.opts.RegisterUnwind {
			discardUnwind(file.obj)
		}
		if err := checkTarget(file.obj); err != nil {
			return err
		}
	}
	s.files = append(s.files, files...)
	for _, path := range absolute {
		s.paths[path] = true
	}
	return nil
}

// Reading and composing has no session or native side effects. Keep original
// per-file snapshots until the entire bounded directory validates successfully.
func readCOFFImportDirectory(paths []string) ([]*file, []string, error) {
	return readCOFFImportDirectorySources(coffImportSources(paths))
}

func coffImportSources(paths []string) []COFFImportSource {
	sources := make([]COFFImportSource, len(paths))
	for i, path := range paths {
		sources[i].Path = path
	}
	return sources
}

func readCOFFImportDirectorySources(sources []COFFImportSource) ([]*file, []string, error) {
	if len(sources) == 0 {
		return nil, nil, fmt.Errorf("COFF import directory requires object paths")
	}
	var files []*file
	var absolute []string
	var objects []*object
	seen := make(map[string]bool)
	remaining := int64(maxFile)
	for _, source := range sources {
		path := source.Path
		if strings.IndexByte(path, 0) >= 0 {
			return nil, nil, fmt.Errorf("NUL in path")
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, nil, err
		}
		if seen[abs] {
			return nil, nil, fmt.Errorf("duplicate COFF import directory input: %s", abs)
		}
		seen[abs] = true
		available := remaining
		b, err := readFileLimit(abs, available)
		if err != nil {
			return nil, nil, err
		}
		remaining -= int64(len(b))
		entries := []ar.Entry{{Data: b}}
		if source.Members != nil {
			if bytes.HasPrefix(b, []byte("!<thin>\n")) {
				entries, err = ar.SnapshotSelected(context.Background(), abs, b, source.Members, int(available), 0, func(path string, limit int64) ([]byte, error) {
					if limit > remaining {
						limit = remaining
					}
					data, err := readFileLimit(path, limit)
					if err == nil {
						remaining -= int64(len(data))
					}
					return data, err
				})
			} else {
				entries, err = archiveEntries(b, false)
				if err == nil {
					entries, err = ar.Select(entries, source.Members)
				}
			}
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", abs, err)
			}
		}
		for i, entry := range entries {
			name, directory := abs, filepath.Dir(abs)
			if source.Members != nil {
				member := entry.DisplayName
				if member == "" {
					member = entry.Name
				}
				name = fmt.Sprintf("%s[%d](%s)", abs, source.Members[i], member)
			}
			if entry.Source != "" {
				directory = filepath.Dir(entry.Source)
			}
			f, err := parseCOFFWithImports(name, entry.Data, false)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", name, err)
			}
			if f.obj == nil || f.info.Kind != "object" || f.info.Format != "COFF" {
				return nil, nil, fmt.Errorf("COFF import directory requires raw COFF objects: %s", name)
			}
			f.obj.directory = directory
			files, objects = append(files, f), append(objects, f.obj)
		}
		absolute = append(absolute, abs)
	}
	graph := newCOFFImportGraph(objects)
	converted, ok := graph.composeDirectory(objects)
	if !ok {
		return nil, nil, fmt.Errorf("invalid or incomplete bounded COFF import directory")
	}
	for i, o := range converted {
		files[i].obj = o
		files[i].info = o.info
	}
	return files, absolute, nil
}
