package ar

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestSnapshotSelectedOrderAndUnreadMembers(t *testing.T) {
	data := thinFixture("one", "absent", "two", "one")
	reads := make(map[string]int)
	read := func(path string, limit int64) ([]byte, error) {
		name := filepath.Base(path)
		reads[name]++
		if name == "absent" {
			return nil, fmt.Errorf("unselected path opened")
		}
		return []byte(name), nil
	}
	got, err := SnapshotSelected(context.Background(), filepath.Join(t.TempDir(), "source.a"), data, []int{2, 3, 0}, 4096, 0, read)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"two", "one", "one"} {
		if got[i].Name != name || !bytes.Equal(got[i].Data, []byte(name)) || filepath.Base(got[i].Source) != name {
			t.Fatal("selected order or external identity lost", got)
		}
	}
	if len(reads) != 2 || reads["one"] != 1 || reads["two"] != 1 {
		t.Fatal("selected snapshots not reused", reads)
	}
	// Ordinary Snapshot must still read every member and report the missing one.
	if _, err := Snapshot(context.Background(), "source.a", data, 4096, 0, read); err == nil {
		t.Fatal("all-member behavior changed")
	}
}

func TestSnapshotSelectedRejectsBeforeIO(t *testing.T) {
	data := thinFixture("one", "two")
	for _, tc := range []struct {
		name    string
		indices []int
	}{
		{"nil", nil}, {"empty", []int{}}, {"negative", []int{-1}},
		{"outside", []int{2}}, {"duplicate", []int{1, 1}}, {"too_many", []int{0, 1, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			_, err := SnapshotSelected(context.Background(), "source.a", data, tc.indices, 4096, 0, func(string, int64) ([]byte, error) {
				reads++
				return []byte("x"), nil
			})
			if err == nil || reads != 0 {
				t.Fatal("invalid selection performed IO", err, reads)
			}
		})
	}
}

func TestSnapshotSelectedByteBudgets(t *testing.T) {
	data := thinFixture("one", "absent", "one", "two")
	read := func(_ string, limit int64) ([]byte, error) {
		if limit < 100 {
			return nil, fmt.Errorf("file exceeds limit %d", limit)
		}
		return bytes.Repeat([]byte{'x'}, 100), nil
	}
	if _, err := SnapshotSelected(context.Background(), "source.a", data, []int{0, 2}, len(data)+100, 0, read); err != nil {
		t.Fatal("shared source read twice", err)
	}
	if _, err := SnapshotSelected(context.Background(), "source.a", data, []int{0, 3}, len(data)+100, 0, read); err == nil {
		t.Fatal("unique external budget exceeded")
	}
	if _, err := SnapshotSelected(context.Background(), "source.a", data, []int{0, 2, 3}, len(data)+99, 0, read); err == nil {
		t.Fatal("external read budget exceeded")
	}
}
