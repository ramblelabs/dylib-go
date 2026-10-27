// Package abibridge builds the optional native Swift invocation facade from
// explicitly supplied, pinned ABIBridge and ZDLibffi source checkouts.
package abibridge

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/ramblelabs/dylib-go/compiler/internal/clangcmd"
)

const SourceCommit = "4dfbda22afb9a1492e1c5c99933a9defbc5437a4"
const LibffiCommit = "e9737c755088a4a3d2ba03f2ca5bfeadff9c09b2"

//go:embed native/bridge.cpp
var facade string

// Options supplies local source checkouts and optional tool paths. Build never
// downloads dependencies, edits their sources, or changes the selected Xcode.
// Architecture defaults to the host; cross compilation does not qualify execution.
type Options struct {
	Source       string
	LibffiSource string
	Swift        string
	Compiler     string
	Architecture string
}

// Library owns its completed facade and temporary build directory. Keep it
// until the abibridge.Runtime loaded from Path has been closed.
type Library struct {
	Path string
	dir  string
	mu   sync.Mutex
}

func (l *Library) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dir == "" {
		return nil
	}
	if err := os.RemoveAll(l.dir); err != nil {
		return err
	}
	l.dir = ""
	return nil
}

func tool(ctx context.Context, name string, args ...string) (string, error) {
	out, err := clangcmd.Run(ctx, name, args, "", 1<<20, false)
	return strings.TrimSpace(string(out)), err
}

func checkout(ctx context.Context, path, commit string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("abibridge: both pinned source directories are required")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	head, err := tool(ctx, "git", "-C", path, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if head != commit {
		return "", fmt.Errorf("abibridge: %s is at %s; expected %s", path, head, commit)
	}
	status, err := tool(ctx, "git", "-C", path, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	if status != "" {
		return "", fmt.Errorf("abibridge: source checkout has local changes: %s", path)
	}
	return path, nil
}

func objects(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".o") {
			paths = append(paths, path)
		}
		return err
	})
	if err == nil && len(paths) == 0 {
		err = fmt.Errorf("abibridge: no native objects in %s", root)
	}
	sort.Strings(paths)
	return paths, err
}

// Build compiles upstream native invocation code and its libffi dependency. The
// private SwiftPM manifest uses tools 5.9 because no upstream Swift runtime or
// UI code is needed. Upstream source and dependency manifests remain unchanged.
// The facade supports macOS 15.4+; deployment flags do not prove older runtimes.
func Build(ctx context.Context, options Options) (_ *Library, err error) {
	arch := options.Architecture
	if arch == "" {
		arch = runtime.GOARCH
	}
	triple := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[arch]
	if runtime.GOOS != "darwin" || triple == "" {
		return nil, fmt.Errorf("abibridge: building requires macOS amd64/arm64")
	}
	source, err := checkout(ctx, options.Source, SourceCommit)
	if err != nil {
		return nil, err
	}
	ffi, err := checkout(ctx, options.LibffiSource, LibffiCommit)
	if err != nil {
		return nil, err
	}
	// Swift string literals accept quoted paths without control characters.
	for _, path := range []string{source, ffi} {
		for _, c := range path {
			if c < 32 || c == 127 {
				return nil, fmt.Errorf("abibridge: source paths contain control characters")
			}
		}
	}
	swift := options.Swift
	if swift == "" {
		swift, err = tool(ctx, "xcrun", "--find", "swift")
		if err != nil {
			return nil, err
		}
	}
	compiler := options.Compiler
	if compiler == "" {
		compiler, err = tool(ctx, "xcrun", "--find", "clang++")
		if err != nil {
			return nil, err
		}
	}
	sdk, err := tool(ctx, "xcrun", "--sdk", "macosx", "--show-sdk-path")
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "dylib-abibridge-*")
	if err != nil {
		return nil, err
	}
	library := &Library{dir: dir, Path: filepath.Join(dir, "libdylib_abibridge.dylib")}
	defer func() {
		if err != nil {
			library.Close()
		}
	}()
	if err = os.Symlink(filepath.Join(source, "Sources", "ABIBridgeCore"), filepath.Join(dir, "Sources")); err != nil {
		return nil, err
	}
	// Hooking and image-management sources use newer SDK fields and are outside
	// this facade's API. Compile only its native invocation dependencies.
	manifest := `// swift-tools-version: 5.9
import PackageDescription
let package = Package(name: "DylibBridgeCore", platforms: [.macOS("15.4")], dependencies: [
    .package(name: "ZDLibffi", path: ` + strconv.Quote(ffi) + `)
], targets: [.target(name: "ABIBridgeCore", dependencies: [.product(name: "ZDLibffi", package: "ZDLibffi")],
    path: "Sources", exclude: ["SwiftDemangling"],
    sources: ["Invocation.cpp", "NativeFailure.cpp", "SwiftInvocation.cpp", "SwiftCall.S", "SwiftValueWitness.cpp"], publicHeadersPath: "include",
    cxxSettings: [.headerSearchPath("include"), .headerSearchPath("SwiftDemangling/Support"), .headerSearchPath("SwiftDemangling/Upstream/include")])], cxxLanguageStandard: .cxx20)
`
	if err = os.WriteFile(filepath.Join(dir, "Package.swift"), []byte(manifest), 0600); err != nil {
		return nil, err
	}
	args := []string{"build", "--package-path", dir, "--build-system", "native", "--triple", triple + "-apple-macosx15.4", "-c", "release"}
	for _, target := range []string{"ABIBridgeCore", "ZDLibffi"} {
		if _, err = tool(ctx, swift, append(append([]string(nil), args...), "--target", target)...); err != nil {
			return nil, err
		}
	}
	bin, err := tool(ctx, swift, append(args, "--show-bin-path")...)
	if err != nil {
		return nil, err
	}
	core, err := objects(filepath.Join(bin, "ABIBridgeCore.build"))
	if err != nil {
		return nil, err
	}
	ffiObjects, err := objects(filepath.Join(bin, "ZDLibffi.build"))
	if err != nil {
		return nil, err
	}
	archive := filepath.Join(dir, "native-core.a")
	if _, err = tool(ctx, "ar", append([]string{"crs", archive}, append(core, ffiObjects...)...)...); err != nil {
		return nil, err
	}
	cpp := filepath.Join(dir, "bridge.cpp")
	if err = os.WriteFile(cpp, []byte(facade), 0600); err != nil {
		return nil, err
	}
	link := []string{"-std=c++20", "-O2", "-dynamiclib", "-target", triple + "-apple-macosx15.4", "-isysroot", sdk,
		"-I" + filepath.Join(source, "Sources", "ABIBridgeCore", "include"), cpp, archive,
		"-L" + filepath.Join(filepath.Dir(filepath.Dir(swift)), "lib", "swift", "macosx"),
		"-lswiftCore", "-lswift_Concurrency", "-framework", "Foundation", "-Wl,-rpath,/usr/lib/swift", "-Wl,-dead_strip"}
	for _, name := range []string{"version", "prepare", "invoke", "release", "value_type", "value_type_release", "value_new", "value_pointer", "value_commit", "value_release", "invoke_values"} {
		link = append(link, "-Wl,-exported_symbol,_dylib_abibridge_"+name)
	}
	_, err = tool(ctx, compiler, append(link, "-o", library.Path)...)
	if err != nil {
		return nil, err
	}
	return library, nil
}
