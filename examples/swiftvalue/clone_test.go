//go:build cgo && darwin && (amd64 || arm64)

package swiftvalue

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abibridge"
	builder "github.com/ramblelabs/dylib-go/compiler/abibridge"
)

func TestNativeSwiftValueExample(t *testing.T) {
	facade := os.Getenv("DYLIB_ABIBRIDGE_FACADE")
	if facade == "" {
		source, ffi := os.Getenv("DYLIB_ABIBRIDGE_SOURCE"), os.Getenv("DYLIB_ABIBRIDGE_LIBFFI_SOURCE")
		if source == "" || ffi == "" {
			if os.Getenv("DYLIB_TEST_REQUIRE_ABIBRIDGE") == "1" {
				t.Fatal("required pinned ABIBridge sources missing")
			}
			t.Skip("optional ABIBridge facade/source not configured")
		}
		library, err := builder.Build(context.Background(), builder.Options{Source: source, LibffiSource: ffi})
		if err != nil {
			t.Fatal(err)
		}
		defer library.Close()
		facade = library.Path
	}
	sdk, err := exec.Command("xcrun", "--sdk", "macosx", "--show-sdk-path").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, optimization := range []string{"-Onone", "-O"} {
		t.Run(optimization, func(t *testing.T) {
			library := filepath.Join(t.TempDir(), "value.dylib")
			arch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[runtime.GOARCH]
			module := fmt.Sprintf("SwiftValueExample_%x", sha256.Sum256([]byte(library)))
			command := exec.Command("xcrun", "swiftc", "-sdk", strings.TrimSpace(string(sdk)), "-target", arch+"-apple-macosx15.4", optimization, "-module-name", module, "-emit-library", "testdata/value.swift", "-o", library)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
			s := dylib.New(dylib.Options{KeepLibraries: true})
			defer s.Close()
			if err := s.Load(library); err != nil {
				t.Fatal(err)
			}
			resolve := func(name string) *dylib.Symbol {
				symbol, err := s.Resolve(name)
				if err != nil {
					t.Fatal(err)
				}
				return symbol
			}
			// Each subtest owns its runtime: retiring it must precede s.Close.
			r, err := abibridge.Open(facade)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			kind, err := r.PrepareValueType(resolve("fixture_value_example_metadata"))
			if err != nil {
				t.Fatal(err)
			}
			initCall, err := r.Prepare(abibridge.Signature{Args: []abibridge.Type{{Kind: abibridge.Pointer}, {Kind: abibridge.Int64}}})
			if err != nil {
				t.Fatal(err)
			}
			readCall, err := r.Prepare(abibridge.Signature{Result: abibridge.Type{Kind: abibridge.Int64}, Args: []abibridge.Type{{Kind: abibridge.Pointer}}})
			if err != nil {
				t.Fatal(err)
			}
			initialize, read := resolve("fixture_value_example_initialize"), resolve("fixture_value_example_read")
			word := func(value uint64) []byte {
				data := make([]byte, 8)
				binary.LittleEndian.PutUint64(data, value)
				return data
			}
			var original uintptr
			uses := 0
			err = CloneAndUse(kind, func(address uintptr) error {
				original = address
				_, err := initCall.Invoke(initialize, word(uint64(address)), word(42))
				return err
			}, func(address uintptr) error {
				if address == original {
					return fmt.Errorf("clone reused original storage")
				}
				result, err := readCall.Invoke(read, word(uint64(address)))
				if err != nil {
					return err
				}
				if got := binary.LittleEndian.Uint64(result); got != 642 {
					return fmt.Errorf("Swift value = %d, want 642", got)
				}
				uses++
				return nil
			})
			if err != nil || uses != 1 {
				t.Fatal(err, uses)
			}
			input, err := kind.New(func(address uintptr) error {
				_, err := initCall.Invoke(initialize, word(uint64(address)), word(42))
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			shape := abibridge.Type{Kind: abibridge.Storage, Size: 24, Alignment: 8,
				Components: []abibridge.Component{{Kind: abibridge.Uint64}, {Kind: abibridge.Uint64, Offset: 8}, {Kind: abibridge.Int64, Offset: 16}}, ValueType: kind}
			output, err := Transform(r, resolve("fixture_value_example_transform"), shape, input)
			if err != nil {
				t.Fatal(err)
			}
			input.Close()
			err = output.WithAddress(func(address uintptr) error {
				result, err := readCall.Invoke(read, word(uint64(address)))
				if err == nil && binary.LittleEndian.Uint64(result) != 654 {
					return fmt.Errorf("managed result: %v", result)
				}
				return err
			})
			output.Close()
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
