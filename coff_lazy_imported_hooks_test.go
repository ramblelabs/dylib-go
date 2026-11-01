package dylib

import (
	"fmt"
	"testing"
)

func TestCOFFNativeDelayHookImportedStorage(t *testing.T) {
	for _, machine := range []uint16{0x8664, 0xaa64, 0x14c} {
		for _, kind := range []uint16{0, 1, 2} {
			for _, shape := range []string{"direct", "weak", "missing", "unaligned", "delay", "untyped", "retained", "unmanaged"} {
				t.Run(fmt.Sprintf("%x/%d/%s", machine, kind, shape), func(t *testing.T) {
					name, public, policy := "__pfnDliNotifyHook2", "__pfnDliNotifyHook2", uint16(1)
					if machine == 0x14c {
						public, policy = "_"+public, 2
					}
					f, err := parse("hook-import.obj", shortImport(machine, kind, policy, public, "hooks.dll", "", 0))
					if err != nil {
						t.Fatal(err)
					}
					o := f.obj
					entry := o.symbols[0].imported.entry
					defs, err := definitions([]*object{o})
					if err != nil {
						t.Fatal(err)
					}
					im := &image{base: 4096, pointerSize: uint64(o.info.Bits / 8), defs: defs, imports: map[*coffImport]uintptr{entry: 0x5000}, external: func(string) uintptr { t.Fatal("unexpected export discovery"); return 0 }}
					good, want := kind != 0, uintptr(0x5000)
					switch shape {
					case "weak":
						// The selected IAT name can also be a weak fallback target.
						delete(defs, name)
						alias := &object{info: Info{Format: "COFF"}, symbols: []symbol{{name: name, global: true, alias: &weakAlias{target: 1, search: 3}}, {name: "__imp_" + name, global: true}}}
						im.aliases = map[string]definition{name: {alias, 0}}
					case "missing":
						delete(im.imports, entry)
						good = false
					case "unaligned":
						im.imports[entry]++
						good = false
					case "delay":
						entry.delay, good = true, false
					case "untyped":
						o.info.Imports, good = nil, false
					case "retained", "unmanaged":
						im.lazy = &coffLazyImage{nativeDefinitions: map[string]uintptr{name: 0x6000}}
						want, good = 0x6000, shape == "retained"
						if good {
							im.lazy.retainedDefinitions = map[string]uintptr{name: want}
						}
					}
					hooks, err := im.coffNativeDelayHooks()
					if good {
						if err != nil || hooks.notify != want || hooks.failure != 0 {
							t.Fatal("expected variable address, not an IAT slot or its contents", hooks, want, err)
						}
					} else if err == nil {
						t.Fatal("accepted invalid/unowned import", hooks)
					}
				})
			}
		}
	}
}

func TestCOFFNativeDelayHookRetainedStorage(t *testing.T) {
	for _, width := range []uint64{4, 8} {
		for _, name := range []string{"__pfnDliNotifyHook2", "__pfnDliFailureHook2"} {
			for _, shape := range []string{"explicit", "weak_target", "weak_iat_target", "unmanaged", "stale", "unaligned", "overflow", "untyped_iat"} {
				t.Run(fmt.Sprintf("%d/%s/%s", width, name, shape), func(t *testing.T) {
					address, good := uint64(0x5000), true
					if shape == "overflow" {
						address = 1 << 32
						if uint64(^uintptr(0)) < address {
							t.Skip("address does not fit this host")
						}
						good = width == 8
					}
					if shape == "unaligned" {
						address++
						good = false
					}
					key := name
					im := &image{pointerSize: width, defs: map[string]definition{}, lazy: &coffLazyImage{nativeDefinitions: map[string]uintptr{}, retainedDefinitions: map[string]uintptr{}}}
					if shape == "weak_target" || shape == "weak_iat_target" {
						key = "storage"
						target := key
						if shape == "weak_iat_target" {
							target = "__imp_" + key
						}
						o := &object{info: Info{Format: "COFF"}, symbols: []symbol{{name: name, global: true, alias: &weakAlias{target: 1, search: 3}}, {name: target, global: true}}}
						im.aliases = map[string]definition{name: {o, 0}}
					}
					im.lazy.nativeDefinitions[key], im.lazy.retainedDefinitions[key] = uintptr(address), uintptr(address)
					switch shape {
					case "unmanaged":
						delete(im.lazy.retainedDefinitions, key)
						good = false
					case "stale":
						im.lazy.retainedDefinitions[key] += uintptr(width)
						good = false
					case "untyped_iat":
						im.lazy = nil
						o := &object{symbols: []symbol{{name: "__imp_" + name, section: 1, global: true}}, sections: []*section{nil, {offset: 64, size: width}}}
						im.defs["__imp_"+name] = definition{o, 0}
						good = false
					}
					hooks, err := im.coffNativeDelayHooks()
					if !good {
						if err == nil {
							t.Fatal("accepted invalid/unretained storage", hooks)
						}
						return
					}
					want := coffNativeDelayHooks{notify: uintptr(address)}
					if name == "__pfnDliFailureHook2" {
						want = coffNativeDelayHooks{failure: uintptr(address)}
					}
					if err != nil || hooks != want {
						t.Fatal(hooks, want, err)
					}
				})
			}
		}
	}
}
