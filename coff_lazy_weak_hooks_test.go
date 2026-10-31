package dylib

import (
	"fmt"
	"strings"
	"testing"
)

func TestCOFFNativeDelayHookWeakStorage(t *testing.T) {
	for _, width := range []uint64{4, 8} {
		for _, search := range []uint32{1, 2, 3} {
			for _, kind := range []string{"data", "chain", "strong", "common", "cycle", "missing", "provided", "provided_target", "code", "tls", "common_tls", "import", "absolute", "truncated", "misaligned", "offset", "invalid_target"} {
				t.Run(fmt.Sprintf("%d/%d/%s", width, search, kind), func(t *testing.T) {
					name := "__pfnDliNotifyHook2"
					sec := &section{offset: 64, size: 32, align: width, write: true}
					o := &object{info: Info{Format: "COFF"}, sections: []*section{nil, sec}, symbols: []symbol{
						{name: name, global: true, alias: &weakAlias{target: 2, search: search}},
						{name: "middle", global: true, alias: &weakAlias{target: 2, search: search}},
						{name: "storage", global: true, section: 1},
					}}
					im := &image{base: 4096, pointerSize: width, defs: map[string]definition{"storage": {o, 2}}, aliases: map[string]definition{name: {o, 0}, "middle": {o, 1}}, external: func(string) uintptr { t.Fatal("native hook selection searched external symbols"); return 0 }}
					want, good := im.base+64, false
					switch kind {
					case "data":
						good = true
					case "chain":
						o.symbols[0].alias.target = 1
						good = true
					case "strong":
						strong := &object{sections: []*section{nil, {offset: 128, size: width}}, symbols: []symbol{{name: name, global: true, section: 1}}}
						im.defs[name] = definition{strong, 0}
						// A real definition wins even over a cyclic weak fallback.
						o.symbols[0].alias.target = 0
						want, good = im.base+128, true
					case "common", "common_tls":
						o.symbols[2].section = -2
						im.common = map[string]commonAllocation{"storage": {offset: 192, size: width}}
						want, good = im.base+192, true
						if kind == "common_tls" {
							o.symbols[2].tls, good = true, false
						}
					case "cycle":
						o.symbols[0].alias.target, o.symbols[1].alias.target = 1, 0
					case "missing", "provided_target":
						delete(im.defs, "storage")
						o.symbols[2].section = 0
						if kind == "provided_target" {
							im.hooks = map[string]uintptr{"storage": 12288}
						}
					case "provided":
						im.hooks = map[string]uintptr{name: 12288}
					case "code":
						sec.exec = true
					case "tls":
						o.symbols[2].tls = true
					case "import":
						o.symbols[2].section = -4
					case "absolute":
						o.symbols[2].section = -1
					case "truncated":
						sec.size = width - 1
					case "misaligned":
						o.symbols[2].value = 1
					case "offset":
						o.symbols[0].alias.target = 1
						o.symbols[1].forward = &symbolForward{target: 2, offset: width}
					case "invalid_target":
						o.symbols[0].alias.target = len(o.symbols)
					}
					hooks, err := im.coffNativeDelayHooks()
					if good {
						if err != nil || hooks.notify != want || hooks.failure != 0 {
							t.Fatal("owned weak hook storage", hooks, err, want)
						}
					} else if err == nil {
						t.Fatal("accepted unowned or invalid weak hook storage", kind, hooks)
					}
				})
			}
		}
	}
}

func TestCOFFNativeDelayHookWeakRecords(t *testing.T) {
	for _, target := range []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc", "i686-pc-windows-msvc"} {
		for _, shape := range []string{"weak", "weak_chain"} {
			for _, search := range []uint32{1, 2, 3} {
				t.Run(fmt.Sprintf("%s/%s/%d", target, shape, search), func(t *testing.T) {
					data, _ := coffAssembly(t, target, nativeDelayHookAliases(target, shape))
					for _, name := range []string{"__pfnDliNotifyHook2", "__pfnDliFailureHook2"} {
						le.PutUint32(data[weakAuxOffset(t, data, name)+4:], search)
					}
					f, err := parse("weak-hooks.obj", data)
					if err != nil {
						t.Fatal(err)
					}
					o := f.obj
					// The same producer records used by native Windows tests bind
					// selected storage, without loading or executing a foreign target.
					width := uint64(o.info.Bits / 8)
					o.sections = append(o.sections, &section{offset: 128, size: 2 * width})
					for i := range o.symbols {
						if o.symbols[i].name == "hook_notify_storage" || o.symbols[i].name == "hook_failure_storage" {
							o.symbols[i].section = len(o.sections) - 1
							if o.symbols[i].name == "hook_failure_storage" {
								o.symbols[i].value = width
							}
						}
					}
					defs, err := definitions([]*object{o})
					if err != nil {
						t.Fatal(err)
					}
					aliases, err := weakAliases([]*object{o})
					if err != nil {
						t.Fatal(err)
					}
					im := &image{base: 4096, pointerSize: width, defs: defs, aliases: aliases}
					hooks, err := im.coffNativeDelayHooks()
					if err != nil || hooks.notify != im.base+128 || hooks.failure != im.base+128+uintptr(width) {
						t.Fatal("compiler weak records", hooks, err)
					}
				})
			}
		}
	}
}

// The assembler emits ordinary COFF weak external records. Prefixes reuse
// COFF's native i386 spelling; the loader applies its existing normalization.
func nativeDelayHookAliases(target, kind string) string {
	prefix, word, alignment := "", ".quad", 3
	if strings.HasPrefix(target, "i686") {
		prefix, word, alignment = "_", ".long", 2
	}
	text := fmt.Sprintf(".data\n.p2align %d\n.globl %shook_alias_root\n%shook_alias_root:\n%s 0\n", alignment, prefix, prefix, word)
	for _, pair := range [][2]string{{"__pfnDliNotifyHook2", "hook_notify_storage"}, {"__pfnDliFailureHook2", "hook_failure_storage"}} {
		name, fallback := prefix+pair[0], prefix+pair[1]
		if kind == "weak_chain" {
			middle := prefix + "middle_" + pair[1]
			text += fmt.Sprintf(".weak %s\n.set %s,%s\n", middle, middle, fallback)
			fallback = middle
		}
		text += fmt.Sprintf(".weak %s\n.set %s,%s\n", name, name, fallback)
	}
	return text
}
