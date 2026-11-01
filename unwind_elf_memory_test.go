package dylib

import (
	"errors"
	"testing"
)

func TestELFUnwindImageAddressRanges(t *testing.T) {
	for _, tc := range []struct {
		name       string
		base, size uint64
		good       bool
	}{
		{"zero", 0, 4096, false},
		{"empty", 4096, 0, false},
		{"low", 4096, 4096, true},
		{"boundary", 1 << 32, 4096, false},
		{"above", 1<<32 + 4096, 65536, true},
		{"ends_at_boundary", 1<<32 - 4096, 4096, true},
		{"crosses_boundary", 1<<32 - 4096, 4097, false},
		{"large", 4096, 1 << 32, false},
		{"higher_boundary", 0x7f9b00000000, 65536, false},
		{"observed_catch", 0x7f9b00005000, 65536, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := gccFrameMappingSafe(tc.base, tc.size); got != tc.good {
				t.Fatal(got, tc)
			}
		})
	}
}

func TestELFUnwindAllocationOwnership(t *testing.T) {
	problem := errors.New("allocation failed")
	for _, mode := range []string{"ordinary", "retry", "exhausted", "failed"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			allocate := func(n int) ([]byte, error) {
				calls++
				if mode == "failed" && calls == 3 {
					return nil, problem
				}
				return []byte{byte(calls)}, nil
			}
			var accept func(uint64, uint64) bool
			if mode != "ordinary" {
				accept = func(uint64, uint64) bool { return mode == "retry" && calls == 3 }
			}
			memory, reserved, err := reserveImageMemory(1, allocate, accept)
			want := map[string]int{"ordinary": 0, "retry": 2, "exhausted": 8, "failed": 2}[mode]
			if len(reserved) != want || (err == nil) != (mode == "ordinary" || mode == "retry") {
				t.Fatal(calls, reserved, err)
			}
			if err == nil && (len(memory) != 1 || int(memory[0]) != calls) {
				t.Fatal("accepted mapping changed", memory, calls)
			}
			if mode == "failed" && !errors.Is(err, problem) {
				t.Fatal(err)
			}
			im := &image{reservedMemory: reserved}
			freed := 0
			free := func(b []byte) error {
				if int(b[0]) != want-freed {
					t.Fatal("mapping freed out of order or twice", b, freed)
				}
				freed++
				return nil
			}
			if want != 0 {
				if err := im.freeReservedMemory(func([]byte) error { return problem }); !errors.Is(err, problem) || len(im.reservedMemory) != want {
					t.Fatal("failed cleanup lost mapping ownership", err, im.reservedMemory)
				}
			}
			if want > 1 {
				err := im.freeReservedMemory(func(b []byte) error {
					if freed == 1 {
						return problem
					}
					return free(b)
				})
				if !errors.Is(err, problem) || freed != 1 || len(im.reservedMemory) != want-1 {
					t.Fatal("partial cleanup was replayed or lost", err, freed, im.reservedMemory)
				}
			}
			if err := im.freeReservedMemory(free); err != nil || len(im.reservedMemory) != 0 || freed != want {
				t.Fatal(err, im.reservedMemory, freed)
			}
			if err := im.freeReservedMemory(free); err != nil || freed != want {
				t.Fatal("cleanup repeated", err, freed)
			}
		})
	}
}
