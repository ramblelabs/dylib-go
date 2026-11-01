package dylib

import (
	"fmt"
	"strings"
)

// These are addresses of pointer variables, not snapshots of their contents.
// Their image, imported DLL or retained Symbol provider outlives native cleanup.
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
		if address, found, err := im.coffRetainedDelayHook(entry.name); found {
			if err != nil {
				return hooks, err
			}
			*entry.cell = address
			continue
		}
		d, ok := im.defs[entry.name]
		iat := false
		if !ok {
			d, ok = im.aliases[entry.name]
			if !ok {
				// DATA imports expose only an IAT spelling. Its resolved target
				// is the variable; the IAT itself contains another level of indirection.
				d, ok = im.defs["__imp_"+entry.name]
				if !ok {
					continue // Do not discover DLL/process exports implicitly.
				}
				iat = true
			}
			// Reuse ordinary weak selection and cycle checks, but restrict the
			// result to selected storage and explicitly retained definitions.
			selected := image{defs: im.defs, aliases: im.aliases, hooks: im.hooks}
			if im.lazy != nil {
				selected.external = func(name string) uintptr { return im.lazy.nativeDefinitions[name] }
			}
			resolved, offset, err := selected.resolveAddressDefinition(d.o, d.index)
			if err != nil {
				return hooks, fmt.Errorf("native delay hook %s: %w", entry.name, err)
			}
			if offset != 0 {
				return hooks, fmt.Errorf("%s must define image-owned pointer storage", entry.name)
			}
			d = resolved
		}
		symbol := d.sym()
		if symbol.section == 0 {
			if address, found, err := im.coffRetainedDelayHook(symbol.name); found {
				if err != nil {
					return hooks, err
				}
				*entry.cell = address
				continue
			}
		}
		if symbol.imported != nil {
			address, err := im.coffImportedDelayHook(d)
			if err != nil {
				return hooks, err
			}
			*entry.cell = address
			continue
		}
		if iat {
			return hooks, fmt.Errorf("native delay hook %s requires a typed data import", entry.name)
		}
		if symbol.tls {
			return hooks, fmt.Errorf("%s must define image-owned pointer storage", entry.name)
		}
		var offset, size uint64
		if symbol.section == -2 {
			allocation, ok := im.common[symbol.name]
			if !ok {
				return hooks, fmt.Errorf("missing native delay hook common storage %s", entry.name)
			}
			offset, size = allocation.offset, allocation.size
		} else {
			if symbol.section <= 0 || symbol.section >= len(d.o.sections) {
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

// Only explicit retained definitions are eligible. Unmanaged Define addresses
// cannot establish the cell/code lifetime required by a delayed callback.
func (im *image) coffRetainedDelayHook(name string) (uintptr, bool, error) {
	name = strings.TrimPrefix(name, "__imp_") // Match externalSymbol's COFF spelling.
	if im.lazy == nil || im.lazy.nativeDefinitions[name] == 0 {
		return 0, false, nil
	}
	address := im.lazy.nativeDefinitions[name]
	if im.lazy.retainedDefinitions[name] != address {
		return 0, true, fmt.Errorf("native delay hook %s requires a retained DefineSymbol provider", name)
	}
	return address, true, im.coffDelayHookAddress(name, address)
}

func (im *image) coffImportedDelayHook(d definition) (uintptr, error) {
	entry := d.sym().imported.entry
	if entry == nil || entry.delay {
		return 0, fmt.Errorf("native delay hook requires an ordinary data import")
	}
	data := false
	for _, info := range d.o.info.Imports {
		if info.DLL == entry.dll && info.Symbol == entry.public && info.Name == entry.name && info.Ordinal == entry.ordinal && (info.Kind == "data" || info.Kind == "const") {
			data = true
			break
		}
	}
	if !data {
		return 0, fmt.Errorf("native delay hook %s requires a data/const import", entry.public)
	}
	if address, found, err := im.coffRetainedDelayHook(entry.public); found {
		return address, err
	}
	address := im.imports[entry] // prepareImports retained the supplying DLL.
	return address, im.coffDelayHookAddress(entry.public, address)
}

func (im *image) coffDelayHookAddress(name string, address uintptr) error {
	if im.pointerSize != 4 && im.pointerSize != 8 || address == 0 || uint64(address)%im.pointerSize != 0 || im.pointerSize == 4 && uint64(address) > 0xffffffff {
		return fmt.Errorf("unaligned or missing native delay hook storage %s", name)
	}
	return nil
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
