package dylib

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"
)

func TestCOFFLazyEntryAssembler(t *testing.T) {
	for _, target := range []struct{ arch, triple string }{
		{"amd64", "x86_64-pc-windows-msvc"},
		{"arm64", "aarch64-pc-windows-msvc"},
		{"386", "i686-pc-windows-msvc"},
	} {
		for _, wide := range []bool{false, true} {
			name := target.arch + "/ordinary"
			if wide {
				if target.arch == "386" || unsafe.Sizeof(uintptr(0)) != 8 {
					continue
				}
				name = target.arch + "/wide_addresses"
			}
			t.Run(name, func(t *testing.T) {
				cell, context, helper := uintptr(0x23456780), uintptr(0x34567898), uintptr(0x456789a8)
				source, err := os.ReadFile(filepath.Join("testdata", "cofflazy", target.arch+".S"))
				if err != nil {
					t.Fatal(err)
				}
				assembly := strings.ReplaceAll(string(source), "\r\n", "\n")
				if wide {
					// Spell the independently assembled halfwords explicitly, so
					// the reference cannot share the emitter's shift arithmetic.
					values := []uint64{0x123456789abcdef0, 0xfedcba9876543210, 0x13579bdf2468ace0}
					cell, context, helper = uintptr(values[0]), uintptr(values[1]), uintptr(values[2])
					if target.arch == "amd64" {
						assembly = strings.NewReplacer("0x23456780", "0x123456789abcdef0", "0x34567898", "0xfedcba9876543210", "0x456789a8", "0x13579bdf2468ace0").Replace(assembly)
					} else {
						assembly = strings.NewReplacer(
							"movz x16,#0x6780\nmovk x16,#0x2345,lsl #16\nmovk x16,#0,lsl #32\nmovk x16,#0,lsl #48", "movz x16,#0xdef0\nmovk x16,#0x9abc,lsl #16\nmovk x16,#0x5678,lsl #32\nmovk x16,#0x1234,lsl #48",
							"movz x0,#0x7898\nmovk x0,#0x3456,lsl #16\nmovk x0,#0,lsl #32\nmovk x0,#0,lsl #48", "movz x0,#0x3210\nmovk x0,#0x7654,lsl #16\nmovk x0,#0xba98,lsl #32\nmovk x0,#0xfedc,lsl #48",
							"movz x16,#0x89a8\nmovk x16,#0x4567,lsl #16\nmovk x16,#0,lsl #32\nmovk x16,#0,lsl #48", "movz x16,#0xace0\nmovk x16,#0x2468,lsl #16\nmovk x16,#0x9bdf,lsl #32\nmovk x16,#0x1357,lsl #48",
						).Replace(assembly)
					}
				}
				_, object := coffAssembly(t, target.triple, assembly)
				var code, unwind []byte
				var slow uint32
				for _, section := range object.sections {
					if section != nil && section.name == ".text" {
						code = section.data
					} else if section != nil && section.name == ".xdata" {
						unwind = section.data
					}
				}
				for _, symbol := range object.symbols {
					if symbol.name == "lazy_slow" {
						slow = uint32(symbol.value)
					}
				}
				got, err := makeCOFFLazyEntry(target.arch, cell, context, helper)
				if err != nil || slow == 0 || !bytes.Equal(got.code, code) || !bytes.Equal(got.unwind, unwind) || got.slow != slow {
					t.Fatalf("entry differs from assembler: slow=%d/%d code=%x/%x unwind=%x/%x: %v", got.slow, slow, got.code, code, got.unwind, unwind, err)
				}
				clear(got.code)
				clear(got.unwind)
				again, err := makeCOFFLazyEntry(target.arch, cell, context, helper)
				if err != nil || !bytes.Equal(again.code, code) || !bytes.Equal(again.unwind, unwind) {
					t.Fatal("entry generation shared mutable instruction/metadata storage", err)
				}
			})
		}
	}
}

func TestCOFFLazyEntryValidation(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "386", "riscv64"} {
		for _, tc := range []struct {
			name                  string
			cell, context, helper uintptr
		}{
			{"zero_cell", 0, 16, 16},
			{"zero_context", 16, 0, 16},
			{"zero_helper", 16, 16, 0},
			{"unaligned_cell", 17, 16, 16},
			{"unaligned_context", 16, 17, 16},
		} {
			t.Run(arch+"/"+tc.name, func(t *testing.T) {
				got, err := makeCOFFLazyEntry(arch, tc.cell, tc.context, tc.helper)
				if err == nil || len(got.code) != 0 || len(got.unwind) != 0 {
					t.Fatal("invalid entry address accepted", got, err)
				}
			})
		}
	}
	if got, err := makeCOFFLazyEntry("arm64", 16, 16, 17); err == nil || len(got.code) != 0 {
		t.Fatal("unaligned ARM64 instruction pointer accepted", err)
	}
	if got, err := makeCOFFLazyEntry("riscv64", 16, 16, 16); err == nil || len(got.code) != 0 {
		t.Fatal("unknown instruction backend accepted", err)
	}
	if unsafe.Sizeof(uintptr(0)) == 8 {
		wide := uint64(1) << 32
		for _, values := range [][3]uintptr{{uintptr(wide), 16, 16}, {16, uintptr(wide), 16}, {16, 16, uintptr(wide)}} {
			if got, err := makeCOFFLazyEntry("386", values[0], values[1], values[2]); err == nil || len(got.code) != 0 {
				t.Fatal("i386 address truncated", err)
			}
		}
	}
}
