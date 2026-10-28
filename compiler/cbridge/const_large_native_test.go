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
		// Large frames must probe Windows' guard pages. Use the compiler's own
		// probe objects; unrelated builtins may have unqualified COMDAT forms.
		output, err := exec.Command(compiler, append(flags(), "-print-libgcc-file-name")...).CombinedOutput()
		if err != nil {
			t.Fatalf("compiler runtime archive: %v\n%s", err, output)
		}
		path := strings.TrimSpace(string(output))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("compiler runtime archive %q: %v", path, err)
		}
		members, err := exec.Command(tool(t, "llvm-ar"), "t", path).CombinedOutput()
		if err != nil {
			t.Fatalf("compiler runtime members: %v\n%s", err, members)
		}
		for _, member := range strings.Fields(string(members)) {
			if !strings.Contains(strings.ToLower(member), "chkstk") {
				continue
			}
			data, err := exec.Command(tool(t, "llvm-ar"), "p", path, member).Output()
			if err != nil {
				t.Fatal("compiler stack probe", member, err)
			}
			object := filepath.Join(t.TempDir(), filepath.Base(member))
			if err := os.WriteFile(object, data, 0600); err != nil {
				t.Fatal(err)
			}
			runtimeArchives = append(runtimeArchives, object)
		}
		if len(runtimeArchives) == 0 {
			t.Fatal("compiler runtime contains no stack-probe objects", path)
		}
	}
	// The full storage boundary is independent of optimizer behavior. Smaller
	// nested/block/remainder initializers run at both O0 and O2 in the main fixture;
	// GCC's optimization of 65,536 separate initializer loads is very expensive.
	testNativeFacadeOptimizations(t, compiler, h, f, "testdata/const_large.c", check, []string{"-O0"}, runtimeArchives)
}
