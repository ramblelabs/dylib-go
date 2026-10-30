package ar

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
)

// External paths are read once, so their proxy references see the same snapshot.
// Bound both file reads and decoded object bytes: repeated references must not
// turn a small thin container into unbounded parsing/allocation work.
type thinSource struct {
	data                []byte
	entries             map[uint64]Entry
	maxSize, maxMembers int
}

type thinReader struct {
	remaining           int64
	sources             map[string]*thinSource
	readFile            func(string, int64) ([]byte, error)
	maxSize, maxMembers int
}

func (r *thinReader) read(path string) (*thinSource, error) {
	if source := r.sources[path]; source != nil {
		return source, nil
	}
	b, err := r.readFile(path, r.remaining)
	if err != nil {
		return nil, fmt.Errorf("thin archive external member: %w", err)
	}
	r.remaining -= int64(len(b))
	source := &thinSource{data: b, maxSize: r.maxSize, maxMembers: r.maxMembers}
	r.sources[path] = source
	return source, nil
}

func (s *thinSource) member(offset uint64) (Entry, error) {
	if s.entries == nil {
		if !bytes.HasPrefix(s.data, []byte("!<arch>\n")) {
			return Entry{}, fmt.Errorf("thin member offset requires an external regular archive")
		}
		entries, err := Decode(s.data, false, s.maxSize, s.maxMembers)
		if err != nil {
			return Entry{}, err
		}
		s.entries = make(map[uint64]Entry, len(entries))
		for _, entry := range entries {
			s.entries[entry.Offset] = entry
		}
	}
	entry, ok := s.entries[offset]
	if !ok {
		return Entry{}, fmt.Errorf("thin archive offset %d is not an object member header", offset)
	}
	return entry, nil
}

// Snapshot validates a complete thin container and reads referenced files once.
// It resolves regular archive proxies by header offset and retains source
// paths for dependency origins. readFile must enforce its byte limit before
// allocation. Unique external reads and decoded member bytes each have a bound.
// It does not parse native objects or compile bitcode.
func Snapshot(ctx context.Context, name string, b []byte, maxSize, maxMembers int, readFile func(string, int64) ([]byte, error)) ([]Entry, error) {
	return snapshot(ctx, name, b, nil, maxSize, maxMembers, readFile)
}

// SnapshotSelected validates the whole container but only reads files required
// by the requested references, in caller order. Unselected missing
// files and proxies are not opened. Snapshot retains its all-member behavior.
func SnapshotSelected(ctx context.Context, name string, b []byte, indices []int, maxSize, maxMembers int, readFile func(string, int64) ([]byte, error)) ([]Entry, error) {
	if len(indices) == 0 {
		return nil, fmt.Errorf("empty thin archive member selection")
	}
	return snapshot(ctx, name, b, indices, maxSize, maxMembers, readFile)
}

func snapshot(ctx context.Context, name string, b []byte, indices []int, maxSize, maxMembers int, readFile func(string, int64) ([]byte, error)) ([]Entry, error) {
	if ctx == nil || readFile == nil {
		return nil, fmt.Errorf("thin archive requires a context and file reader")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := Decode(b, true, maxSize, maxMembers)
	if err != nil {
		return nil, err
	}
	if indices != nil {
		entries, err = Select(entries, indices)
		if err != nil {
			return nil, err
		}
	}
	r := thinReader{remaining: int64(maxSize - len(b)), sources: make(map[string]*thinSource), readFile: readFile, maxSize: maxSize, maxMembers: maxMembers}
	remainingObjects := maxSize
	for i := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry := &entries[i]
		path := filepath.FromSlash(entry.Name)
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(name), path)
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		source, err := r.read(path)
		if err != nil {
			return nil, err
		}
		entry.Data, entry.Source, entry.DisplayName = source.data, path, entry.Name
		if entry.Origin != 0 {
			inner, err := source.member(entry.Origin)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			entry.Data = inner.Data
			entry.DisplayName = fmt.Sprintf("%s:%d(%s)", entry.Name, entry.Origin, inner.Name)
		}
		if bytes.HasPrefix(entry.Data, []byte("!<arch>\n")) || bytes.HasPrefix(entry.Data, []byte("!<thin>\n")) {
			return nil, fmt.Errorf("%s(%s): nested archives are unsupported", name, entry.DisplayName)
		}
		if len(entry.Data) > remainingObjects {
			return nil, fmt.Errorf("thin archive decoded object bytes exceed %d", maxSize)
		}
		remainingObjects -= len(entry.Data)
	}
	return entries, nil
}
