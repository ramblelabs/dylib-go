//go:build cgo && (linux || darwin || windows)

package cbridge_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	dylib "github.com/ramblelabs/dylib-go"
	"github.com/ramblelabs/dylib-go/abi"
	"github.com/ramblelabs/dylib-go/compiler/cbridge"
	"github.com/ramblelabs/dylib-go/compiler/clang"
	"github.com/ramblelabs/dylib-go/examples/headerbridge"
)

func TestNativeHeaderConstLargeArray(t *testing.T) {
	compiler := clangTool(t)
	testNativeConstLargeArray(t, compiler, compiler)
}

func testNativeConstLargeArray(t *testing.T, parser, compiler string) {
	t.Helper()
	header, err := filepath.Abs("testdata/const_large.h")
	if err != nil {
		t.Fatal(err)
	}
	h, err := clang.Parse(context.Background(), header, clang.Options{Compiler: parser, Functions: []string{"const_large"}, NominalRecords: true, Flags: flags()})
	if err != nil {
		t.Fatal(err)
	}
	f, err := cbridge.Generate(filepath.ToSlash(header), h, []cbridge.Function{{Name: "const_large", Symbol: "bridge_const_large"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.CSource) > 256<<10 {
		t.Fatal("large-array initializer source grew with repeated access paths", len(f.CSource))
	}
	check := func(t *testing.T, session *dylib.Session, _ *clang.Header, facade *cbridge.Facade) {
		t.Helper()
		binding := facade.Functions[0]
		value := abi.Zero(binding.Signature().ArgumentType(0))
		var expected int32
		for i := range value.Aggregate.Fields[0].Aggregate.Fields {
			v := uint8(i % 251)
			value.Aggregate.Fields[0].Aggregate.Fields[i] = abi.Uint8(v)
			expected += int32(v)
		}
		got, err := headerbridge.Call(session, binding, value)
		if err != nil || got != abi.Int32(expected) {
			t.Fatal("const 64 KiB array", got, expected, err)
		}
	}
	var runtimeArchives []string
	if runtime.GOOS == "windows" {
		// Large frames must probe Windows' guard pages. Resolve the compiler's
		// own builtins archive lazily, exactly as other raw-object dependencies.
		output, err := exec.Command(compiler, append(flags(), "-print-libgcc-file-name")...).CombinedOutput()
		if err != nil {
			t.Fatalf("compiler runtime archive: %v\n%s", err, output)
		}
		path := strings.TrimSpace(string(output))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("compiler runtime archive %q: %v", path, err)
		}
		runtimeArchives = append(runtimeArchives, path)
	}
	// The full storage boundary is independent of optimizer behavior. Smaller
	// nested/block/remainder initializers run at both O0 and O2 in the main fixture;
	// GCC's optimization of 65,536 separate initializer loads is very expensive.
	testNativeFacadeOptimizations(t, compiler, h, f, "testdata/const_large.c", check, []string{"-O0"}, runtimeArchives)
}
