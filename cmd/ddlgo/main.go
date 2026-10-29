// ddlgo inspects binary files or calls entries with caller-supplied C signatures.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/ramblelabs/dylib-go/abi"
	"io"
	"os"

	dylib "github.com/ramblelabs/dylib-go"
)

func run(args []string, output io.Writer) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: ddlgo inspect FILE | ddlgo call [OPTIONS] 'func NAME(TYPES)RESULT' VALUES... FILE... | ddlgo call [OPTIONS] 'NAME(VALUE:TYPE, ...)RESULT' FILE... | ddlgo call [OPTIONS] SYMBOL A B FILE...")
	}
	switch args[0] {
	case "inspect":
		if len(args) != 2 {
			return fmt.Errorf("inspect requires one file")
		}
		i, e := dylib.Inspect(args[1])
		if e != nil {
			return e
		}
		enc := json.NewEncoder(output)
		enc.SetIndent("", "  ")
		return enc.Encode(i)
	case "call":
		fs := flag.NewFlagSet("call", flag.ContinueOnError)
		fs.SetOutput(output)
		process := fs.Bool("process", false, "resolve exported host symbols")
		keep := fs.Bool("keep-libraries", false, "retain runtime-bearing OS libraries until process exit")
		emulatedTLS := fs.Bool("emulated-tls", false, "enable image-owned Clang emulated TLS")
		nativeTLS := fs.Bool("native-tls", false, "enable supported image-owned Unix native TLS models")
		delayImports := fs.Bool("eager-delay-imports", false, "bind recognized raw COFF delay imports at Link")
		convention := fs.String("abi", "default", "C calling convention: default, cdecl, stdcall, fastcall, thiscall, win64, sysv64")
		variadicFrom := fs.Int("variadic-from", -1, "number of fixed parameters for a concrete variadic call shape")
		linkageName := fs.String("symbol", "", "exact linkage name, overriding the Go declaration name")
		if e := fs.Parse(args[1:]); e != nil {
			return e
		}
		input, err := parseCallInput(fs.Args())
		if err != nil {
			return err
		}
		conventions := map[string]abi.Convention{"default": abi.Default, "cdecl": abi.CDecl, "stdcall": abi.StdCall, "fastcall": abi.FastCall, "thiscall": abi.ThisCall, "win64": abi.Win64, "sysv64": abi.SysV64}
		selected, ok := conventions[*convention]
		if !ok {
			return fmt.Errorf("unknown calling convention %q", *convention)
		}
		input.Signature.Convention = selected
		if *variadicFrom < -1 {
			return fmt.Errorf("variadic-from must be -1 or a positive fixed parameter count")
		}
		if *variadicFrom != -1 {
			input.Signature.Variadic = true
			input.Signature.FixedArgs = *variadicFrom
		}
		if *linkageName != "" {
			input.Name = *linkageName
		}
		if selected != abi.Default || input.Signature.Variadic {
			input.legacy = false
		}
		if err := input.Signature.Validate(); err != nil {
			return err
		}
		mode := dylib.DelayImportsDisabled
		if *delayImports {
			mode = dylib.DelayImportsEager
		}
		return executeCall(input, dylib.Options{ProcessSymbols: *process, KeepLibraries: *keep, EmulatedTLS: *emulatedTLS, NativeTLS: *nativeTLS, DelayImports: mode}, output)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
func main() {
	if e := run(os.Args[1:], os.Stdout); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
