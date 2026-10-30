// Package ar decodes archive containers shared by the native loader and
// optional compiler helpers. It does not parse or execute member objects.
package ar

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Entry offsets identify headers, including GNU thin references to
// members of external regular archives. Thin object contents are not inline.
type Entry struct {
	Name           string
	Data           []byte
	Offset, Origin uint64
	Source         string // Absolute external path, populated by Snapshot.
	DisplayName    string // Thin reference including selected proxy member name.
}

// Select returns the requested ordinary members in caller order. Indices refer
// to Decode's entries, excluding symbol indexes and long-name tables. Duplicate
// member names remain distinct; duplicate indices and empty sets are rejected.
func Select(entries []Entry, indices []int) ([]Entry, error) {
	if len(indices) == 0 || len(indices) > len(entries) {
		return nil, fmt.Errorf("invalid archive member selection size %d", len(indices))
	}
	seen := make(map[int]bool, len(indices))
	selected := make([]Entry, 0, len(indices))
	for _, index := range indices {
		if index < 0 || index >= len(entries) || seen[index] {
			return nil, fmt.Errorf("invalid or duplicate archive member index %d", index)
		}
		seen[index] = true
		selected = append(selected, entries[index])
	}
	return selected, nil
}

// Decode validates GNU/SysV, COFF and BSD headers before external I/O.
// Entry data borrows b; symbol indexes and name tables are omitted.
// maxMembers == 0 leaves the member count unlimited within maxSize.
func Decode(b []byte, thin bool, maxSize, maxMembers int) ([]Entry, error) {
	magic := "!<arch>\n"
	if thin {
		magic = "!<thin>\n"
	}
	if maxSize < 0 || len(b) > maxSize || !bytes.HasPrefix(b, []byte(magic)) {
		return nil, fmt.Errorf("expected %s archive of at most %d bytes", strings.TrimSpace(magic), maxSize)
	}
	var names []byte
	var entries []Entry
	for pos := 8; pos < len(b); {
		if len(b)-pos < 60 || string(b[pos+58:pos+60]) != "`\n" {
			return nil, fmt.Errorf("bad archive header at %d", pos)
		}
		offset := uint64(pos)
		h := b[pos : pos+60]
		n, err := strconv.ParseUint(strings.TrimSpace(string(h[48:58])), 10, 64)
		if err != nil || n > uint64(maxSize) {
			return nil, fmt.Errorf("invalid archive member size at %d", pos)
		}
		mn := strings.TrimSpace(string(h[:16]))
		stored := !thin || mn == "//" || mn == "/" || mn == "/SYM64/" || strings.HasPrefix(mn, "__.SYMDEF")
		pos += 60
		var member []byte
		if stored {
			if n > uint64(len(b)-pos) {
				return nil, fmt.Errorf("invalid archive member size at %d", offset)
			}
			member = b[pos : pos+int(n)]
			pos += int(n)
			if pos&1 != 0 {
				if pos >= len(b) {
					return nil, fmt.Errorf("missing archive padding")
				}
				pos++
			}
		}
		var origin uint64
		switch {
		case mn == "//":
			names = member
			continue
		case mn == "/" || mn == "/SYM64/" || strings.HasPrefix(mn, "__.SYMDEF"):
			continue
		case strings.HasPrefix(mn, "#1/"):
			if thin {
				return nil, fmt.Errorf("BSD extended names in thin archives are unsupported")
			}
			l, e := strconv.Atoi(mn[3:])
			if e != nil || l < 0 || l > len(member) {
				return nil, fmt.Errorf("invalid BSD archive name")
			}
			mn = strings.TrimRight(string(member[:l]), "\x00")
			member = member[l:]
			if strings.HasPrefix(mn, "__.SYMDEF") {
				continue
			}
		case strings.HasPrefix(mn, "/"):
			// GNU ar 2.42 can leave a slash at the end of its padded name
			// field ("/0             /"). It terminates the reference.
			reference := strings.TrimSpace(strings.TrimSuffix(mn[1:], "/"))
			if thin && strings.Contains(reference, ":") {
				parts := strings.Split(reference, ":")
				if len(parts) != 2 {
					return nil, fmt.Errorf("invalid thin archive member offset")
				}
				origin, err = strconv.ParseUint(parts[1], 10, 64)
				if err != nil || origin < 8 || origin&1 != 0 {
					return nil, fmt.Errorf("invalid thin archive member offset")
				}
				reference = parts[0]
			}
			o, e := strconv.Atoi(reference)
			if e != nil || o < 0 || o >= len(names) {
				return nil, fmt.Errorf("invalid archive name offset %q (table size %d)", reference, len(names))
			}
			// GNU long names end with /\n; COFF long names end with NUL.
			// Both use the // member and /decimal-offset references.
			end := bytes.IndexAny(names[o:], "\n\x00")
			if end < 0 {
				return nil, fmt.Errorf("unterminated archive name")
			}
			mn = string(names[o : o+end])
			if names[o+end] == '\n' {
				mn = strings.TrimSuffix(mn, "/")
			}
		default:
			mn = strings.TrimSuffix(mn, "/")
		}
		if mn == "" {
			return nil, fmt.Errorf("empty archive member name")
		}
		if strings.IndexByte(mn, 0) >= 0 {
			return nil, fmt.Errorf("NUL in archive member name")
		}
		if maxMembers > 0 && len(entries) >= maxMembers {
			return nil, fmt.Errorf("archive exceeds %d object members", maxMembers)
		}
		entries = append(entries, Entry{Name: mn, Data: member, Offset: offset, Origin: origin})
	}
	return entries, nil
}
