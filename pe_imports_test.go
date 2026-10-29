package dylib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/ramblelabs/dylib-go/peinfo"
)

func checkPEImportMetadata(t *testing.T, f peImportFixture, shape string) peinfo.Imports {
	t.Helper()
	before := append([]byte(nil), f.data...)
	got, err := peinfo.Inspect(f.data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, f.data) {
		t.Fatal("PE inspection changed source bytes")
	}
	fileInfo, err := Inspect(f.file)
	if err != nil || fileInfo.PEImports == nil || !reflect.DeepEqual(*fileInfo.PEImports, got) {
		t.Fatal("generic Inspect did not expose PE metadata", fileInfo, err)
	}
	if len(got.Directories) == 0 {
		t.Fatal("ordinary kernel32 imports absent")
	}
	delay := shape == "delay" || shape == "delay_bound" || shape == "legacy"
	symbols := map[string][]peinfo.Symbol{}
	if delay {
		if len(got.Delay) != 2 {
			t.Fatal("delay directories", got)
		}
		for _, d := range got.Delay {
			writable := false
			for _, section := range f.sections {
				if d.AddressRVA >= section.VirtualAddress && uint64(d.AddressRVA-section.VirtualAddress) < uint64(section.Size) && section.Characteristics&0x80000000 != 0 {
					writable = true
				}
			}
			if !writable {
				t.Fatal("delay helper fixture requires a writable IAT", d)
			}
			symbols[d.DLL] = d.Symbols
			want := uint32(1)
			if shape == "legacy" {
				want = 0
			}
			if d.Attributes != want || d.ModuleHandleRVA == 0 || d.UnloadAddressRVA == 0 || len(d.UnloadAddresses) != len(d.Symbols) {
				t.Fatal("delay attributes, ownership or unload metadata lost", d)
			}
			for i, s := range d.Symbols {
				if s.Address != d.UnloadAddresses[i] {
					t.Fatal("linker unload table differs from original IAT", d)
				}
			}
			if shape == "delay_bound" {
				group := 0
				if d.DLL == "pe-second.dll" {
					group = 1
				}
				if d.TimeDateStamp != uint32(group+1) || d.BoundAddressRVA == 0 || len(d.BoundAddresses) != len(d.Symbols) {
					t.Fatal("bound delay metadata", d)
				}
				target := "x86_64"
				if f.width == 4 {
					target = "i686"
				}
				for i, v := range d.BoundAddresses {
					if v != directoryCachedAddress(target, group, i) {
						t.Fatal("bound cache changed", d)
					}
				}
			} else if d.BoundAddressRVA != 0 || len(d.BoundAddresses) != 0 {
				t.Fatal("unexpected bound delay addresses", d)
			}
		}
	} else {
		if len(got.Delay) != 0 {
			t.Fatal("normal imports became delayed", got)
		}
		for _, d := range got.Directories {
			if !d.LookupAvailable {
				t.Fatal("normal lookup unavailable", d)
			}
			symbols[d.DLL] = d.Symbols
		}
	}
	first, second := symbols["pe-first.dll"], symbols["pe-second.dll"]
	if len(first) != 1 || first[0].Name != "first" || first[0].Ordinal != 0 || len(second) != 2 {
		t.Fatal("DLL-qualified identities", symbols)
	}
	names := map[string]bool{}
	for _, s := range second {
		if s.Ordinal != 0 {
			names[fmt.Sprintf("#%d", s.Ordinal)] = true
		} else {
			names[s.Name] = true
		}
	}
	if !names["second"] || !names["#7"] {
		t.Fatal("named/ordinal imports lost", second)
	}
	if shape == "bound" {
		if len(got.Bound) != 2 || got.Bound[0].DLL != "pe-first.dll" || got.Bound[0].TimeDateStamp != 1 || len(got.Bound[0].Forwarders) != 2 || got.Bound[1].DLL != "pe-second.dll" || got.Bound[1].TimeDateStamp != 0xffffffff {
			t.Fatal("bound directory", got.Bound)
		}
		if got.Bound[0].Forwarders[0].DLL != "kernel32.dll" || got.Bound[0].Forwarders[0].TimeDateStamp != 2 || got.Bound[0].Forwarders[1].DLL != "pe-second.dll" || got.Bound[0].Forwarders[1].TimeDateStamp != 3 {
			t.Fatal("bound forwarders", got.Bound)
		}
	} else if len(got.Bound) != 0 {
		t.Fatal("unexpected bound directory", got.Bound)
	}
	encoded, err := json.Marshal(fileInfo)
	if err != nil || !bytes.Contains(encoded, []byte(`"pe_imports"`)) {
		t.Fatal("PE metadata JSON", err)
	}
	return got
}

func checkLLVMPEImports(t *testing.T, f peImportFixture, got peinfo.Imports) {
	t.Helper()
	output, err := exec.Command("llvm-readobj", "--coff-imports", f.file).CombinedOutput()
	if err != nil {
		t.Fatalf("independent LLVM PE reader: %v\n%s", err, output)
	}
	text := strings.ToLower(string(output))
	for _, d := range got.Directories {
		for _, field := range []string{"name: " + d.DLL, fmt.Sprintf("importlookuptablerva: 0x%x", d.LookupRVA), fmt.Sprintf("importaddresstablerva: 0x%x", d.AddressRVA)} {
			if !strings.Contains(text, strings.ToLower(field)) {
				t.Fatal("LLVM disagrees with regular import metadata", field, string(output))
			}
		}
		for _, s := range d.Symbols {
			symbol := fmt.Sprintf("symbol: %s (%d)", s.Name, s.Hint)
			if s.Ordinal != 0 {
				symbol = fmt.Sprintf("symbol:  (%d)", s.Ordinal)
			}
			if !strings.Contains(text, strings.ToLower(symbol)) {
				t.Fatal("LLVM regular lookup identity", symbol, string(output))
			}
		}
	}
	if strings.Count(text, "delayimport {") != len(got.Delay) {
		t.Fatal("LLVM delay directory count", string(output))
	}
	for _, d := range got.Delay {
		for _, field := range []string{"name: " + d.DLL, fmt.Sprintf("attributes: 0x%x", d.Attributes), fmt.Sprintf("modulehandle: 0x%x", d.ModuleHandleRVA), fmt.Sprintf("importaddresstable: 0x%x", d.AddressRVA), fmt.Sprintf("importnametable: 0x%x", d.NameTableRVA), fmt.Sprintf("bounddelayimporttable: 0x%x", d.BoundAddressRVA), fmt.Sprintf("unloaddelayimporttable: 0x%x", d.UnloadAddressRVA)} {
			if !strings.Contains(text, strings.ToLower(field)) {
				t.Fatal("LLVM disagrees with delay metadata", field, string(output))
			}
		}
		for _, s := range d.Symbols {
			symbol := fmt.Sprintf("symbol: %s (%d)", s.Name, s.Hint)
			if s.Ordinal != 0 {
				symbol = fmt.Sprintf("symbol:  (%d)", s.Ordinal)
			}
			if !strings.Contains(text, strings.ToLower(symbol)) || !strings.Contains(text, fmt.Sprintf("address: 0x%x", s.Address)) {
				t.Fatal("LLVM delay lookup identity/address", s, string(output))
			}
		}
	}
	if len(got.Delay) != 0 && got.Delay[0].Attributes&1 != 0 {
		relocations, err := exec.Command("llvm-readobj", "--coff-basereloc", f.file).CombinedOutput()
		if err != nil {
			t.Fatalf("independent PE base relocations: %v\n%s", err, relocations)
		}
		kind := "dir64"
		if f.width == 4 {
			kind = "highlow"
		}
		text := strings.ToLower(string(relocations))
		for _, d := range got.Delay {
			for i := range d.UnloadAddresses {
				pattern := fmt.Sprintf(`type:\s+%s\s+address:\s+0x%x\b`, kind, uint64(d.UnloadAddressRVA)+uint64(i)*uint64(f.width))
				if !regexp.MustCompile(pattern).MatchString(text) {
					t.Fatal("unload pointer lacks native image relocation", pattern, text)
				}
			}
		}
	}
}

func TestPEImportMetadataTargets(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc", "x86_64-w64-windows-gnu", "aarch64-w64-windows-gnu", "i686-w64-windows-gnu"} {
		for _, shape := range []string{"normal", "bound", "delay", "delay_bound", "legacy"} {
			if shape == "legacy" && !strings.HasPrefix(target, "i686") {
				continue
			}
			t.Run(target+"/"+shape, func(t *testing.T) {
				f := buildPEImportFixture(t, target, "-O2", shape)
				got := checkPEImportMetadata(t, f, shape)
				if shape != "legacy" {
					checkLLVMPEImports(t, f, got)
				}
				// Results own their strings and tables after the caller changes its bytes.
				snapshot, _ := json.Marshal(got)
				clear(f.data)
				after, _ := json.Marshal(got)
				if !bytes.Equal(snapshot, after) {
					t.Fatal("metadata retained input storage")
				}
			})
		}
	}
}

func TestNativePEImportMetadata(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native PE execution requires Windows")
	}
	needNative(t)
	target := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH] + "-pc-windows-msvc"
	for _, shape := range []string{"normal", "bound", "delay", "delay_bound", "legacy"} {
		if shape == "legacy" && runtime.GOARCH != "386" {
			continue
		}
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(shape+"/"+opt, func(t *testing.T) {
				f := buildPEImportFixture(t, target, opt, shape)
				checkPEImportMetadata(t, f, shape)
				// These providers live beside the control DLL. The compiled delay helper
				// locates that directory independently of the Go decoder and session search.
				dir := strings.TrimSuffix(f.file, "control.dll")
				for i, name := range []string{"first", "second"} {
					source := dir + name + ".c"
					body := fmt.Sprintf("int %s(int a,int b){return a+b+%d;}", name, i*1000)
					if err := os.WriteFile(source, []byte(body), 0600); err != nil {
						t.Fatal(err)
					}
					object := compile(t, source, source+".obj", opt)
					def := dir + name + ".def"
					if err := os.WriteFile(def, []byte("EXPORTS\n"+name+" @7\n"), 0600); err != nil {
						t.Fatal(err)
					}
					command(t, "lld-link", "/dll", "/noentry", "/nodefaultlib", "/def:"+def, "/out:"+dir+"pe-"+name+".dll", object)
				}
				s := New(Options{})
				defer s.Close()
				delayed := shape == "delay" || shape == "delay_bound" || shape == "legacy"
				if !delayed {
					load(t, s, dir+"pe-first.dll", dir+"pe-second.dll")
				}
				load(t, s, f.file)
				defer func() { _, _ = callInt32(s, "unload", 0, 0) }()
				call(t, s, "observed", 0, 0, 0)
				call(t, s, "invoke", 20, 22, 2126)
				if delayed {
					call(t, s, "observed", 0, 0, 3)
					call(t, s, "invoke", 20, 22, 2126)
					call(t, s, "observed", 0, 0, 3)
					call(t, s, "unload", 0, 0, 2)
					call(t, s, "invoke", 20, 22, 2126)
					call(t, s, "observed", 0, 0, 6)
					call(t, s, "unload", 0, 0, 2)
				} else {
					call(t, s, "observed", 0, 0, 0)
				}
			})
		}
	}
}
