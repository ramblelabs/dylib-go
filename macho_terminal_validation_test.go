package dylib

import (
	"debug/macho"
	"os"
	"testing"
)

func TestMachOTerminalValidation(t *testing.T) {
	for _, target := range []string{"x86_64-apple-macosx11", "arm64-apple-macosx11"} {
		for _, kind := range []string{"regular", "coalesced"} {
			for _, name := range []string{"no_subsections", "private_end", "weak_reference", "name_collision", "outside_marker", "fixup_at_end", "empty_section"} {
				t.Run(target+"/"+kind+"/"+name, func(t *testing.T) {
					form := 0
					if name == "name_collision" {
						form = 1
					}
					flags := machoTerminalFlags(kind, form, 0, "-O0")
					if name == "fixup_at_end" {
						flags = append(flags, "-DDYLIB_TERM_POISON")
					}
					_, b := machoCoalescedFixture(t, target, "macho_terminal.S", flags...)
					f := machoPointerMetadata(t, b)
					sec := f.Section("__term")
					marker, _ := machoAliasNlist(t, b, "term_marker")
					switch name {
					case "no_subsections":
						le.PutUint32(b[24:], le.Uint32(b[24:])&^uint32(macho.FlagSubsectionsViaSymbols))
					case "private_end":
						b[marker+4] &^= 1
					case "weak_reference":
						le.PutUint16(b[marker+6:], le.Uint16(b[marker+6:])&^0x80|0x40)
					case "name_collision":
						other, _ := machoAliasNlist(t, b, "term_zmarker")
						le.PutUint32(b[other:], le.Uint32(b[marker:]))
					case "outside_marker":
						le.PutUint64(b[marker+8:], sec.Addr+sec.Size+8)
					case "fixup_at_end":
						if len(sec.Relocs) == 0 {
							t.Fatal("missing boundary fixup producer")
						}
						le.PutUint32(b[sec.Reloff:], uint32(sec.Size))
					case "empty_section":
						path := machoCoalescedAssembly(t, target, ".section __DATA,__term,"+kind+"\n.globl _term_marker\n.weak_definition _term_marker\n_term_marker:\n.subsections_via_symbols\n")
						var err error
						b, err = os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
					}
					parsed, err := parse("terminal-boundary.o", b)
					bad := name == "outside_marker" || name == "fixup_at_end" || kind == "coalesced" && (name == "no_subsections" || name == "weak_reference" || name == "name_collision")
					if bad {
						if err == nil && len(parsed.info.Unsupported) == 0 {
							t.Fatal("unqualified terminal boundary accepted")
						}
						return
					}
					if err != nil || len(parsed.info.Unsupported) != 0 {
						t.Fatal("legacy boundary behavior changed", err, parsed)
					}
					want := 1
					if name == "no_subsections" || name == "empty_section" {
						want = 0
					}
					if len(parsed.obj.machoAtoms) != want {
						t.Fatal("unqualified end acquired an owner", parsed.obj.machoAtoms)
					}
				})
			}
		}
	}
}
