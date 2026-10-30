package dylib

import (
	"fmt"
	"path/filepath"
	"strings"
)

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
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.mutable(); err != nil {
		return err
	}
	files, absolute, err := readCOFFImportDirectory(paths)
	if err != nil {
		return err
	}
	for i, path := range absolute {
		if s.paths[path] {
			return fmt.Errorf("COFF import directory input already loaded: %s", path)
		}
		if !s.opts.RegisterUnwind {
			discardUnwind(files[i].obj)
		}
		if err := checkTarget(files[i].obj); err != nil {
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
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("COFF import directory requires object paths")
	}
	var files []*file
	var absolute []string
	var objects []*object
	seen := make(map[string]bool)
	remaining := int64(maxFile)
	for _, path := range paths {
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
		b, err := readFileLimit(abs, remaining)
		if err != nil {
			return nil, nil, err
		}
		remaining -= int64(len(b))
		f, err := parseCOFFWithImports(abs, b, false)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", abs, err)
		}
		if f.obj == nil || f.info.Kind != "object" || f.info.Format != "COFF" {
			return nil, nil, fmt.Errorf("COFF import directory requires raw COFF objects: %s", abs)
		}
		f.obj.directory = filepath.Dir(abs)
		files, absolute, objects = append(files, f), append(absolute, abs), append(objects, f.obj)
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
