package peinfo

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func emptyImage(payload int) []byte {
	b := make([]byte, 512+payload)
	copy(b, "MZ")
	little.PutUint32(b[60:], 128)
	copy(b[128:], "PE\x00\x00")
	little.PutUint16(b[132:], 0x8664)
	little.PutUint16(b[134:], 1)
	little.PutUint16(b[148:], 240)
	little.PutUint16(b[150:], 0x2022)
	h := b[152:]
	little.PutUint16(h, 0x20b)
	little.PutUint64(h[24:], 0x180000000)
	little.PutUint32(h[32:], 4096)
	little.PutUint32(h[36:], 512)
	little.PutUint32(h[56:], uint32(4096+payload))
	little.PutUint32(h[60:], 512)
	little.PutUint32(h[108:], 16)
	s := b[392:]
	copy(s, ".rdata")
	little.PutUint32(s[8:], uint32(payload))
	little.PutUint32(s[12:], 4096)
	little.PutUint32(s[16:], uint32(payload))
	little.PutUint32(s[20:], 512)
	little.PutUint32(s[36:], 0x40000040)
	return b
}
func TestEmptyImage(t *testing.T) {
	data := emptyImage(512)
	got, err := Inspect(data)
	if err != nil || !reflect.DeepEqual(got, Imports{}) {
		t.Fatal(got, err)
	}
	for _, index := range []int{1, 11, 13} {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			b := append([]byte(nil), data...)
			d := b[152+112+index*8:]
			little.PutUint32(d, 4096)
			little.PutUint32(d[4:], uint32(map[int]int{1: 20, 11: 8, 13: 32}[index]))
			got, err := Inspect(b)
			if err != nil || !reflect.DeepEqual(got, Imports{}) {
				t.Fatal(got, err)
			}
		})
	}
}
func TestImportBudget(t *testing.T) {
	data := emptyImage(64 + (maxEntries+1)*8)
	d := data[152+112+8:]
	little.PutUint32(d, 4096)
	little.PutUint32(d[4:], 40)
	entry := data[512:]
	little.PutUint32(entry[4:], 1)
	little.PutUint32(entry[12:], 4096+40)
	little.PutUint32(entry[16:], 4096+64)
	copy(entry[40:], "cached.dll\x00")
	for i := 0; i < maxEntries; i++ {
		little.PutUint64(entry[64+i*8:], 0xfedc000000000001)
	}
	got, err := Inspect(data)
	if err == nil || !strings.Contains(err.Error(), "budget") || !reflect.DeepEqual(got, Imports{}) {
		t.Fatal("record budget or atomic failure lost", err)
	}
}

func TestBoundRelativeName(t *testing.T) {
	data := emptyImage(512)
	d := data[152+112+11*8:]
	little.PutUint32(d, 4096)
	little.PutUint32(d[4:], 16)
	// Offsets are relative to the complete directory, including its records.
	// This valid short name shares the descriptor's timestamp bytes.
	copy(data[512:], "abc\x00")
	got, err := Inspect(data)
	if err != nil || len(got.Bound) != 1 || got.Bound[0].DLL != "abc" || got.Bound[0].TimeDateStamp != 0x00636261 {
		t.Fatal(got, err)
	}
}
func FuzzInspect(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("MZ"))
	f.Add(emptyImage(512))
	for _, index := range []int{1, 11, 13} {
		data := emptyImage(512)
		d := data[152+112+index*8:]
		little.PutUint32(d, 4096)
		little.PutUint32(d[4:], uint32(map[int]int{1: 20, 11: 8, 13: 32}[index]))
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		before := append([]byte(nil), data...)
		got, err := Inspect(data)
		if !bytes.Equal(before, data) {
			t.Fatal("decoder changed input")
		}
		if err != nil && !reflect.DeepEqual(got, Imports{}) {
			t.Fatal("decoder returned partial metadata")
		}
	})
}
