//go:build cgo

package abi

import (
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestLayoutOwnershipConventionsAndLimits(t *testing.T) {
	desc := TypeDesc{Type: Struct, Fields: []Field{{Name: "a", Type: TypeDesc{Type: I32}}, {Name: "b", Type: TypeDesc{Type: I8}}}}
	want, err := LayoutOf(desc, CDecl)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := LayoutOf(desc, Default)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("independent layout: %+v, %v; want %+v", got, err, want)
				return
			}
			got.Offsets[0] = 999
		}()
	}
	wg.Wait()
	for _, convention := range []Convention{StdCall, FastCall, ThisCall} {
		got, err := LayoutOf(desc, convention)
		if Available() && runtime.GOOS == "windows" && runtime.GOARCH == "386" {
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("Windows 386 convention %d: %+v, %v", convention, got, err)
			}
		} else if err == nil {
			t.Fatal("accepted unavailable convention", convention)
		}
	}
	large := TypeDesc{Type: Array, Len: 10000, Elem: &TypeDesc{Type: U64}}
	if _, err := LayoutOf(large, Default); err == nil || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatal("accepted oversized native layout:", err)
	}
	// Querying a pointer must not allocate the large annotated pointee.
	pointer, err := LayoutOf(TypeDesc{Type: Pointer, Elem: &large}, Default)
	if err != nil || pointer.Size == 0 || pointer.Offsets != nil {
		t.Fatalf("pointer layout: %+v, %v", pointer, err)
	}
	if _, err := LayoutOf(desc, Default); err != nil {
		t.Fatal("valid query after a failed construction:", err)
	}
}
