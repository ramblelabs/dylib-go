package peinfo

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func boundDirectoryFixture() []byte {
	b := make([]byte, 24)
	little.PutUint32(b, 0x12345678)
	little.PutUint16(b[4:], 24)
	little.PutUint16(b[6:], 1)
	little.PutUint32(b[8:], 0xffffffff)
	little.PutUint16(b[12:], 34)
	little.PutUint16(b[14:], 7)
	return append(b, []byte("first.dll\x00second.dll\x00")...)
}

func TestInspectBoundDirectory(t *testing.T) {
	b := boundDirectoryFixture()
	before := append([]byte(nil), b...)
	want := []BoundImport{{DLL: "first.dll", TimeDateStamp: 0x12345678, Forwarders: []BoundForwarder{{DLL: "second.dll", TimeDateStamp: 0xffffffff, Reserved: 7}}}}
	got, err := InspectBoundDirectory(b)
	if err != nil || !reflect.DeepEqual(got, want) || !bytes.Equal(b, before) {
		t.Fatal(got, err)
	}
	clear(b)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("metadata retained input storage", got)
	}
	for _, empty := range [][]byte{nil, {}, make([]byte, 8)} {
		if got, err := InspectBoundDirectory(empty); got != nil || err != nil {
			t.Fatal("absent directory", got, err)
		}
	}
}

func TestInspectBoundDirectoryValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{"truncated_descriptor", func(b []byte) []byte { return b[:7] }},
		{"truncated_forwarder", func(b []byte) []byte { return b[:15] }},
		{"missing_terminator", func(b []byte) []byte { return b[:16] }},
		{"outside_name", func(b []byte) []byte { little.PutUint16(b[4:], uint16(len(b))); return b }},
		{"empty_name", func(b []byte) []byte { b[24] = 0; return b }},
		{"unterminated_forwarder_name", func(b []byte) []byte { return b[:len(b)-1] }},
		{"excess_forwarders", func(b []byte) []byte { little.PutUint16(b[6:], 0xffff); return b }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.edit(boundDirectoryFixture())
			before := append([]byte(nil), b...)
			if got, err := InspectBoundDirectory(b); err == nil || got != nil || !bytes.Equal(before, b) {
				t.Fatal("malformed directory accepted or partial output/input mutation", got, err)
			}
		})
	}
}

func TestBoundDirectorySharedBudget(t *testing.T) {
	r := &reader{entries: maxEntries - 1, names: maxInput}
	if got, err := r.boundDirectory(boundDirectoryFixture()); got != nil || err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatal("bound decoder reset the PE-wide record budget", got, err)
	}
	r = &reader{names: 9}
	if got, err := r.boundDirectory(boundDirectoryFixture()); got != nil || err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatal("bound decoder reset the PE-wide name budget", got, err)
	}
}

func FuzzInspectBoundDirectory(f *testing.F) {
	f.Add([]byte{})
	f.Add(make([]byte, 8))
	f.Add(boundDirectoryFixture())
	f.Fuzz(func(t *testing.T, data []byte) {
		before := append([]byte(nil), data...)
		got, err := InspectBoundDirectory(data)
		if !bytes.Equal(before, data) || err != nil && got != nil {
			t.Fatal("input mutation or partial output", err)
		}
	})
}
