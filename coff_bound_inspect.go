package dylib

import (
	"fmt"
	"strings"

	"github.com/ramblelabs/dylib-go/peinfo"
)

// InspectCOFFBoundImportDirectory reads auxiliary bound-import metadata from a
// raw COFF object. root is a unique, defined, nonweak symbol, with the same
// linker-name normalization as Inspect. size explicitly bounds the complete directory,
// including its relative-name strings, starting at that symbol. Its section must
// be file-backed and relocation-free. Regular COFF and bigobj are accepted.
//
// This opens no DLLs and executes no code. It infers neither a section naming
// convention nor the directory's extent. It does not bind imports or validate
// cached addresses. Use peinfo.Inspect for a complete PE image; archives must be
// explicitly extracted before calling this function.
func InspectCOFFBoundImportDirectory(path, root string, size uint64) ([]peinfo.BoundImport, error) {
	if root == "" || strings.IndexByte(root, 0) >= 0 || size == 0 || size > maxFile {
		return nil, fmt.Errorf("COFF bound directory requires a symbol and a nonzero bounded size")
	}
	b, err := readFile(path)
	if err != nil {
		return nil, err
	}
	f, err := parseCOFFWithImports(path, b, false)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.obj == nil || f.info.Kind != "object" || f.info.Format != "COFF" {
		return nil, fmt.Errorf("%s: bound directory requires a raw COFF object", path)
	}
	o := f.obj
	var found *symbol
	for i := range o.symbols {
		s := &o.symbols[i]
		if s.name == root {
			if found != nil {
				return nil, fmt.Errorf("%s: ambiguous bound directory symbol %q", path, root)
			}
			found = s
		}
	}
	if found == nil || found.weak || found.alias != nil || found.section <= 0 || found.section >= len(o.sections) || o.sections[found.section] == nil {
		return nil, fmt.Errorf("%s: bound directory symbol %q must define file-backed storage", path, root)
	}
	data := o.sections[found.section].data
	if found.value > uint64(len(data)) || size > uint64(len(data))-found.value {
		return nil, fmt.Errorf("%s: bound directory exceeds file-backed storage", path)
	}
	for _, r := range o.relocs {
		if r.section == found.section {
			return nil, fmt.Errorf("%s: bound directory section must be relocation-free", path)
		}
	}
	out, err := peinfo.InspectBoundDirectory(data[found.value : found.value+size])
	if err != nil {
		return nil, fmt.Errorf("%s: COFF bound directory %q: %w", path, root, err)
	}
	return out, nil
}
