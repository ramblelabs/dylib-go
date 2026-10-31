package dylib

import "fmt"

// These are addresses of pointer variables, not snapshots of their contents.
// Native code can initialize them before first use, including in CRT startup.
type coffNativeDelayHooks struct{ notify, failure uintptr }

type coffNativeDelayHookCall func(request, variable uintptr, event uint32, module, address uintptr, nativeError uint32, transient bool) uintptr

func (im *image) coffNativeDelayHooks() (coffNativeDelayHooks, error) {
	var hooks coffNativeDelayHooks
	if im.pointerSize != 4 && im.pointerSize != 8 {
		return hooks, fmt.Errorf("invalid native delay hook pointer size")
	}
	for _, entry := range []struct {
		name string
		cell *uintptr
	}{{"__pfnDliNotifyHook2", &hooks.notify}, {"__pfnDliFailureHook2", &hooks.failure}} {
		d, ok := im.defs[entry.name]
		if !ok {
			if _, alias := im.aliases[entry.name]; alias {
				return hooks, fmt.Errorf("%s must define image-owned pointer storage", entry.name)
			}
			continue // Do not search process/DLL exports for a data variable.
		}
		symbol := d.sym()
		var offset, size uint64
		if symbol.section == -2 {
			allocation, ok := im.common[symbol.name]
			if !ok {
				return hooks, fmt.Errorf("missing native delay hook common storage %s", entry.name)
			}
			offset, size = allocation.offset, allocation.size
		} else {
			if symbol.section <= 0 || symbol.section >= len(d.o.sections) || symbol.tls {
				return hooks, fmt.Errorf("%s must define image-owned pointer storage", entry.name)
			}
			section := d.o.sections[symbol.section]
			if section == nil || section.exec || section.nativeTLS != 0 || section.lifecycle != 0 || section.startup || symbol.value > section.size {
				return hooks, fmt.Errorf("invalid native delay hook storage %s", entry.name)
			}
			offset, size = section.offset+symbol.value, section.size-symbol.value
		}
		if size < im.pointerSize || offset%im.pointerSize != 0 {
			return hooks, fmt.Errorf("unaligned or truncated native delay hook storage %s", entry.name)
		}
		*entry.cell = im.base + uintptr(offset)
	}
	return hooks, nil
}

// Go hooks have priority. An empty Go result falls through to the corresponding
// SDK hook. End notifications always reach both hooks; neither transfers a ref.
func (native coffNativeDelayHooks) bind(request uintptr, hooks coffLazyHooks, call coffNativeDelayHookCall) coffLazyHooks {
	wrap := func(goHook func(coffLazyEvent, coffLazyInfo) coffLazyResult, variable uintptr) func(coffLazyEvent, coffLazyInfo) coffLazyResult {
		if variable == 0 {
			return goHook
		}
		return func(event coffLazyEvent, info coffLazyInfo) coffLazyResult {
			if goHook != nil {
				result := goHook(event, info)
				if event != coffLazyEnd && (result.module != 0 || result.address != 0 || result.owned) {
					return result
				}
			}
			var fallback uint32
			if event == coffLazyLoadFailure {
				fallback = coffDelayMissingModule
			} else if event == coffLazyLookupFailure {
				fallback = coffDelayMissingProcedure
			}
			value := call(request, variable, uint32(event), info.module, info.address, nativeDelayError(info.err, fallback), info.transient)
			switch event {
			case coffLazyBeforeLoad, coffLazyLoadFailure:
				return coffLazyResult{module: value, owned: value != 0}
			case coffLazyStart:
				// SDK Start bypasses the helper; it must run again next time.
				return coffLazyResult{address: value, transient: value != 0}
			case coffLazyBeforeLookup, coffLazyLookupFailure:
				return coffLazyResult{address: value}
			}
			return coffLazyResult{}
		}
	}
	return coffLazyHooks{notify: wrap(hooks.notify, native.notify), failure: wrap(hooks.failure, native.failure)}
}
