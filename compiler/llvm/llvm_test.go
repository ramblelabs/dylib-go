package llvm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A real subprocess lets cancellation and output validation exercise the same
// process/file ownership path as llc on Windows, macOS and Linux.
func init() {
	switch os.Getenv("DYLIB_LLVM_COMPILER_HELPER") {
	case "cleanup-lock":
		for i, arg := range os.Args {
			if arg == "-o" && i+1 < len(os.Args) {
				output, err := filepath.Abs(os.Args[i+1])
				if err != nil {
					os.Exit(10)
				}
				blocked := filepath.Join(filepath.Dir(output), "blocked")
				if os.MkdirAll(blocked, 0700) != nil || os.WriteFile(filepath.Join(blocked, "locked"), []byte("owned"), 0600) != nil {
					os.Exit(11)
				}
				marker := os.Getenv("DYLIB_LLVM_CLEANUP_MARKER")
				if os.WriteFile(marker+".tmp", []byte(blocked), 0600) != nil || os.Rename(marker+".tmp", marker) != nil {
					os.Exit(12)
				}
				deadline := time.Now().Add(20 * time.Second)
				for time.Now().Before(deadline) {
					if _, err := os.Stat(os.Getenv("DYLIB_LLVM_CLEANUP_RELEASE")); err == nil {
						os.Exit(3)
					}
					time.Sleep(10 * time.Millisecond)
				}
				os.Exit(13)
			}
		}
		os.Exit(14)
	case "wait":
		if marker := os.Getenv("DYLIB_LLVM_WAIT_MARKER"); marker != "" {
			if err := os.WriteFile(marker, []byte("started"), 0600); err != nil {
				os.Exit(1)
			}
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	case "archive":
		for i, arg := range os.Args {
			if arg == "-o" && i+1 < len(os.Args) {
				if err := os.WriteFile(os.Args[i+1], []byte("!<arch>\n"), 0600); err != nil {
					os.Exit(1)
				}
				os.Exit(0)
			}
		}
		os.Exit(1)
	case "diagnostics":
		data := bytes.Repeat([]byte("x"), maxDiagnosticSize+1)
		os.Stdout.Write(data)
		os.Stderr.Write(data)
		os.Exit(1)
	}
}

func tool(t *testing.T, variable, fallback string) string {
	t.Helper()
	name := os.Getenv(variable)
	if name == "" {
		name = fallback
	}
	path, err := exec.LookPath(name)
	if err != nil {
		if os.Getenv("DYLIB_TEST_REQUIRE_LLVM") == "1" {
			t.Fatal(err)
		}
		t.Skipf("optional LLVM tool unavailable: %v", err)
	}
	return path
}

func TestCompilerDiagnosticsAcrossPipes(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DYLIB_LLVM_COMPILER_HELPER", "diagnostics")
	input := writeIR(t, t.TempDir(), "x86_64-unknown-linux-gnu")
	if _, err := Compile(context.Background(), input, Options{Compiler: executable}); err == nil || !strings.Contains(err.Error(), "[diagnostics truncated]") || len(err.Error()) > maxDiagnosticSize+4096 {
		t.Fatal("unbounded subprocess diagnostics:", err)
	}
}

func writeIR(t *testing.T, dir, triple string) string {
	t.Helper()
	path := filepath.Join(dir, "input with spaces.ll")
	data := fmt.Sprintf("target triple = %q\ndefine i32 @add(i32 %%a, i32 %%b) {\n %%sum = add i32 %%a, %%b\n ret i32 %%sum\n}\n", triple)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

var objectTargets = []struct{ triple, format, arch, os string }{
	// ELF OSABI_NONE does not uniquely identify an OS in object metadata.
	{"x86_64-unknown-linux-gnu", "ELF", "amd64", ""},
	{"aarch64-unknown-linux-gnu", "ELF", "arm64", ""},
	{"i386-unknown-linux-gnu", "ELF", "386", ""},
	{"x86_64-apple-macosx11", "Mach-O", "amd64", "darwin"},
	{"arm64-apple-macosx11", "Mach-O", "arm64", "darwin"},
	{"x86_64-pc-windows-msvc", "COFF", "amd64", "windows"},
	{"aarch64-pc-windows-msvc", "COFF", "arm64", "windows"},
	{"i686-pc-windows-msvc", "COFF", "386", "windows"},
}

func TestCompilePreservesTargetsAndClose(t *testing.T) {
	llc := tool(t, "DYLIB_LLC", "llc")
	for _, target := range objectTargets {
		t.Run(target.triple, func(t *testing.T) {
			dir := t.TempDir()
			input := writeIR(t, dir, target.triple)
			object, err := Compile(context.Background(), input, Options{Compiler: llc, TempDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer object.Close()
			if object.Info.Kind != "object" || object.Info.Format != target.format || object.Info.Arch != target.arch || object.Info.OS != target.os {
				t.Fatalf("module was retargeted: %+v", object.Info)
			}
			if err := object.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(object.Path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("compiled object remains after Close: %v", err)
			}
			if err := object.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(input); err != nil {
				t.Fatal("Close removed caller input:", err)
			}
		})
	}
}

func TestCompileFailureCancellationAndInputLimits(t *testing.T) {
	dir := t.TempDir()
	input := writeIR(t, dir, "x86_64-unknown-linux-gnu")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Compile(ctx, input, Options{TempDir: dir}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := Compile(nil, input, Options{}); err == nil {
		t.Fatal("nil context accepted")
	}
	for _, path := range []string{dir, filepath.Join(dir, "missing")} {
		if _, err := Compile(context.Background(), path, Options{TempDir: dir}); err == nil {
			t.Fatal("nonregular/missing input accepted")
		}
	}
	missingTool := filepath.Join(dir, "no compiler")
	if _, err := Compile(context.Background(), input, Options{Compiler: missingTool, TempDir: dir}); err == nil {
		t.Fatal("missing compiler accepted")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 || files[0].Name() != filepath.Base(input) {
		t.Fatalf("failed/canceled compilation leaked temporary files: %v %v", files, err)
	}
	file, err := os.Create(filepath.Join(dir, "oversized.ll"))
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate(maxInputSize + 1)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(context.Background(), file.Name(), Options{TempDir: dir}); err == nil || !strings.Contains(err.Error(), "regular input") {
		t.Fatalf("oversized input: %v", err)
	}
}

func TestCompileMalformedInputCleanup(t *testing.T) {
	llc := tool(t, "DYLIB_LLC", "llc")
	dir := t.TempDir()
	input := filepath.Join(dir, "invalid.bc")
	if err := os.WriteFile(input, []byte("not LLVM IR or bitcode"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(context.Background(), input, Options{Compiler: llc, TempDir: dir}); err == nil || !strings.Contains(err.Error(), "llvm:") {
		t.Fatalf("malformed module: %v", err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("compiler error leaked temporary files: %v %v", files, err)
	}
}

func TestCompilerDiagnosticsAreBounded(t *testing.T) {
	var output limitedDiagnostics
	data := bytes.Repeat([]byte("x"), maxDiagnosticSize+100)
	if n, err := output.Write(data); n != len(data) || err != nil {
		t.Fatal(n, err)
	}
	output.Write(data)
	if output.buffer.Len() != maxDiagnosticSize || !strings.HasSuffix(output.String(), "[diagnostics truncated]") {
		t.Fatal("compiler diagnostics were not bounded")
	}
}

func TestRunningCompilerCancellationAndInvalidOutput(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"wait", "archive"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			input := writeIR(t, dir, "x86_64-unknown-linux-gnu")
			t.Setenv("DYLIB_LLVM_COMPILER_HELPER", mode)
			ctx := context.Background()
			if mode == "wait" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			_, err := Compile(ctx, input, Options{Compiler: executable, TempDir: dir})
			if mode == "wait" && !errors.Is(err, context.DeadlineExceeded) || mode == "archive" && (err == nil || !strings.Contains(err.Error(), "expected object")) {
				t.Fatalf("compiler %s: %v", mode, err)
			}
			files, readErr := os.ReadDir(dir)
			if readErr != nil || len(files) != 1 {
				t.Fatalf("compiler %s leaked temporary files: %v %v", mode, files, readErr)
			}
		})
	}
}
