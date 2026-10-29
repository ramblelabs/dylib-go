package dylib

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ramblelabs/dylib-go/internal/coffname"
	"github.com/ramblelabs/dylib-go/internal/native"
)

type coffImport struct {
	dll, name, public string
	ordinal           uint16 // Nonzero only for an ordinal import.
	delay             bool
}

type coffImportSymbol struct {
	entry    *coffImport
	indirect bool // __imp_ and CONST public names describe an IAT slot.
}

func parseCOFFImport(name string, b []byte) (*file, error) {
	if len(b) < 20 || le.Uint32(b) != 0xffff0000 || le.Uint16(b[4:]) != 0 || uint64(le.Uint32(b[12:])) != uint64(len(b)-20) {
		return nil, fmt.Errorf("invalid COFF short import header or size")
	}
	flags := le.Uint16(b[18:])
	kind, nameType := flags&3, (flags>>2)&7
	if flags>>5 != 0 || kind > 2 || nameType > 4 {
		return nil, fmt.Errorf("invalid COFF import type or name policy")
	}
	count := 3
	if nameType == 4 {
		count = 4
	}
	var parts [3][]byte
	remaining := b[20:]
	for i := 0; i < count-1; i++ {
		end := bytes.IndexByte(remaining, 0)
		if end < 0 {
			return nil, fmt.Errorf("unterminated COFF import name")
		}
		parts[i], remaining = remaining[:end], remaining[end+1:]
	}
	if len(remaining) != 0 || len(parts[0]) == 0 || len(parts[1]) == 0 {
		return nil, fmt.Errorf("invalid COFF import names")
	}
	public, dll := string(parts[0]), string(parts[1])
	var err error
	dll, err = coffDLLName(dll)
	if err != nil {
		return nil, err
	}
	exported := public
	if nameType == 2 || nameType == 3 {
		if strings.ContainsAny(exported[:1], "?@_") {
			exported = exported[1:]
		}
		if nameType == 3 {
			if i := strings.IndexByte(exported, '@'); i >= 0 {
				exported = exported[:i]
			}
		}
	} else if nameType == 4 {
		exported = string(parts[2])
	}
	entry := &coffImport{dll: dll, name: exported, public: public}
	if nameType == 0 {
		entry.name, entry.ordinal = "", le.Uint16(b[16:])
		if entry.ordinal == 0 {
			return nil, fmt.Errorf("invalid zero import ordinal")
		}
	} else if exported == "" {
		return nil, fmt.Errorf("empty DLL export name")
	}
	o := &object{info: Info{Name: name, Format: "COFF", Kind: "object", OS: "windows", Bits: 64}, timestamp: le.Uint32(b[8:])}
	switch le.Uint16(b[6:]) {
	case 0x8664:
		o.info.Arch = "amd64"
	case 0xaa64:
		o.info.Arch = "arm64"
	case 0x14c:
		o.info.Arch, o.info.Bits = "386", 32
		public = coffname.NormalizeI386(public)
	default:
		o.info.Arch = fmt.Sprintf("machine-%x", le.Uint16(b[6:]))
		o.info.Bits = 0
		o.unsupported("COFF import target")
	}
	entry.public = public
	o.info.Imports = []ImportInfo{{DLL: dll, Symbol: public, Name: entry.name, Ordinal: entry.ordinal, Kind: []string{"code", "data", "const"}[kind]}}
	o.symbols = []symbol{{name: "__imp_" + public, section: -4, global: true, imported: &coffImportSymbol{entry: entry, indirect: true}}}
	if kind != 1 { // CODE has a callable name; CONST has another IAT alias.
		o.symbols = append(o.symbols, symbol{name: public, section: -4, global: true, imported: &coffImportSymbol{entry: entry, indirect: kind == 2}})
	}
	return finish(o), nil
}

func coffDLLName(dll string) (string, error) {
	if dll == "" || strings.ContainsAny(dll, "/\\:\x00") || dll == "." || dll == ".." {
		return "", fmt.Errorf("COFF DLL name must be a file name; configure LibraryPaths for directories")
	}
	if filepath.Ext(dll) == "" {
		dll += ".dll" // Match LoadLibrary's default extension and handle reuse.
	}
	return dll, nil
}

// Imported DLLs are retained like explicitly loaded OS libraries, including
// after a failed object link. No partial raw image is published; dependencies
// remain available for retry and are released by Close unless KeepLibraries.
func (s *Session) prepareImports(objects []*object) (map[*coffImport]uintptr, error) {
	addresses := make(map[*coffImport]uintptr)
	for _, o := range objects {
		for _, v := range o.symbols {
			if v.imported == nil {
				continue
			}
			entry := v.imported.entry
			if addresses[entry] != 0 || runtimeHelper(entry.public) {
				continue
			}
			if p := s.defined[entry.public]; p != 0 {
				addresses[entry] = p
				continue
			}
			key := strings.ToLower(entry.dll)
			h := s.dllHandles[key]
			if h == 0 {
				path, err := importDLLPath(entry.dll, o.directory, s.opts.LibraryPaths)
				if err != nil {
					return nil, err
				}
				h, err = s.openLibrary(path)
				if err != nil {
					return nil, fmt.Errorf("import DLL %s: %w", entry.dll, err)
				}
				if s.dllHandles == nil {
					s.dllHandles = make(map[string]uintptr)
				}
				s.dllHandles[key] = h
				s.libs = append(s.libs, h)
				if s.closed {
					return nil, ErrClosed
				}
			}
			var p uintptr
			if entry.ordinal != 0 {
				p = native.LookupOrdinal(h, entry.ordinal)
			} else {
				p = native.Lookup(h, entry.name)
			}
			if p == 0 {
				return nil, fmt.Errorf("DLL %s does not export %s (name=%q, ordinal=%d)", entry.dll, entry.public, entry.name, entry.ordinal)
			}
			addresses[entry] = p
		}
	}
	return addresses, nil
}

func importDLLPath(name, origin string, paths []string) (string, error) {
	for _, dir := range append(append([]string(nil), paths...), origin) {
		if dir == "" {
			continue
		}
		if strings.IndexByte(dir, 0) >= 0 {
			return "", fmt.Errorf("NUL in LibraryPaths")
		}
		candidate := filepath.Join(dir, name)
		st, err := os.Stat(candidate)
		if err == nil && st.Mode().IsRegular() {
			return filepath.Abs(candidate)
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return name, nil // Let the native loader search its system paths.
}
