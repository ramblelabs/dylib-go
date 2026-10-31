package dylib

import (
	"errors"
	"reflect"
	"syscall"
	"testing"
)

func TestCOFFNativeDelayHookStorage(t *testing.T) {
	for _, width := range []uint64{4, 8} {
		for _, kind := range []string{"data", "readonly", "bss", "common", "absent", "code", "tls", "lifecycle", "startup", "absolute", "import", "missing_section", "truncated", "misaligned", "outside", "missing_common", "small_common", "weak_alias"} {
			t.Run(string(rune('0'+width))+"/"+kind, func(t *testing.T) {
				name := "__pfnDliNotifyHook2"
				sec := &section{size: 32, offset: 64, align: width, write: true, data: make([]byte, 32)}
				o := &object{symbols: []symbol{{name: name, section: 1, global: true}}, sections: []*section{nil, sec}}
				im := &image{base: 4096, pointerSize: width, defs: map[string]definition{name: {o, 0}}, common: map[string]commonAllocation{name: {offset: 128, size: width}}}
				want := im.base + 64
				good := false
				switch kind {
				case "data":
					good = true
				case "readonly":
					sec.write = false
					good = true
				case "bss":
					sec.data = nil
					good = true
				case "common":
					o.symbols[0].section = -2
					want = im.base + 128
					good = true
				case "absent":
					delete(im.defs, name)
					want = 0
					good = true
				case "code":
					sec.exec = true
				case "tls":
					sec.nativeTLS = 0x100
				case "lifecycle":
					sec.lifecycle = 1
				case "startup":
					sec.startup = true
				case "absolute":
					o.symbols[0].section = -1
				case "import":
					o.symbols[0].section = -4
				case "missing_section":
					o.sections[1] = nil
				case "truncated":
					o.symbols[0].value = 32 - width + 1
				case "misaligned":
					o.symbols[0].value = 1
				case "outside":
					o.symbols[0].value = 33
				case "missing_common":
					o.symbols[0].section = -2
					delete(im.common, name)
				case "small_common":
					o.symbols[0].section = -2
					im.common[name] = commonAllocation{offset: 128, size: width - 1}
				case "weak_alias":
					delete(im.defs, name)
					im.aliases = map[string]definition{name: {o, 0}}
				}
				hooks, err := im.coffNativeDelayHooks()
				if good {
					if err != nil || hooks.notify != want || hooks.failure != 0 {
						t.Fatal(hooks, err, want)
					}
				} else if err == nil {
					t.Fatal("accepted malformed hook storage", kind, hooks)
				}
			})
		}
	}
}

func TestCOFFNativeDelayHookPriorityAndEvents(t *testing.T) {
	native := coffNativeDelayHooks{notify: 20, failure: 21}
	for _, event := range []coffLazyEvent{coffLazyStart, coffLazyBeforeLoad, coffLazyBeforeLookup, coffLazyLoadFailure, coffLazyLookupFailure, coffLazyEnd} {
		for _, override := range []bool{false, true} {
			t.Run(string(rune('0'+event))+"/"+map[bool]string{false: "fallthrough", true: "go_override"}[override], func(t *testing.T) {
				var order []string
				goHook := func(coffLazyEvent, coffLazyInfo) coffLazyResult {
					order = append(order, "go")
					if override {
						return coffLazyResult{address: 142}
					}
					return coffLazyResult{}
				}
				call := func(request, cell uintptr, code uint32, module, address uintptr, number uint32, transient bool) uintptr {
					order = append(order, "native")
					wantCell := native.notify
					if event == coffLazyLoadFailure || event == coffLazyLookupFailure {
						wantCell = native.failure
					}
					if request != 10 || cell != wantCell || code != uint32(event) || module != 100 || address != 142 || number != 5 || !transient {
						t.Fatal(request, cell, code, module, address, number, transient)
					}
					return 242
				}
				hooks := native.bind(10, coffLazyHooks{notify: goHook, failure: goHook}, call)
				info := coffLazyInfo{module: 100, address: 142, err: syscall.Errno(5), transient: true}
				var result coffLazyResult
				if event == coffLazyLoadFailure || event == coffLazyLookupFailure {
					result = hooks.failureEvent(event, info)
				} else {
					result = hooks.notifyEvent(event, info)
				}
				if override && event != coffLazyEnd {
					if !reflect.DeepEqual(order, []string{"go"}) || result.address != 142 {
						t.Fatal(order, result)
					}
					return
				}
				if !reflect.DeepEqual(order, []string{"go", "native"}) {
					t.Fatal(order)
				}
				switch event {
				case coffLazyBeforeLoad, coffLazyLoadFailure:
					if result.module != 242 || !result.owned || result.address != 0 {
						t.Fatal(result)
					}
				case coffLazyStart:
					if result.address != 242 || !result.transient || result.owned {
						t.Fatal(result)
					}
				case coffLazyBeforeLookup, coffLazyLookupFailure:
					if result.address != 242 || result.transient || result.owned {
						t.Fatal(result)
					}
				case coffLazyEnd:
					if result != (coffLazyResult{}) {
						t.Fatal("End transferred ownership", result)
					}
				}
			})
		}
	}
}

func TestCOFFNativeDelayHookTransientAndScope(t *testing.T) {
	ops := coffLazyOperations{open: func(string) (uintptr, error) { t.Fatal("Start bypass loaded a DLL"); return 0, nil }, close: func(uintptr) error { t.Fatal("Start bypass transferred a module"); return nil }}
	p, _ := newCOFFLazyProviders(lazyProviderObjects(lazyProviderEntry("missing.dll", "add")), nil, nil, nil, ops, coffLazyHooks{})
	var events []coffLazyEvent
	for _, request := range []uintptr{10, 11} {
		hooks := (coffNativeDelayHooks{notify: 20}).bind(request, coffLazyHooks{}, func(ctx, cell uintptr, event uint32, module, address uintptr, number uint32, transient bool) uintptr {
			if ctx != request || cell != 20 {
				t.Fatal("request escaped into another resolution", ctx, request)
			}
			events = append(events, coffLazyEvent(event))
			if event == uint32(coffLazyStart) {
				return 142
			}
			if event != uint32(coffLazyEnd) || !transient || address != 142 {
				t.Fatal(event, transient, address)
			}
			return 0
		})
		result, code, err := p.resolveWithHooks(0, 1, coffLazyResult{}, hooks)
		if err != nil || code != 0 || result.address != 142 || !result.transient || p.slots[0].address != 0 || len(p.active) != 0 {
			t.Fatal(result, code, err, p.slots[0])
		}
	}
	if !reflect.DeepEqual(events, []coffLazyEvent{coffLazyStart, coffLazyEnd, coffLazyStart, coffLazyEnd}) {
		t.Fatal(events)
	}
	if err := p.unload(); err != nil {
		t.Fatal(err)
	}
}

func TestCOFFNativeDelayHookFailureAndPanic(t *testing.T) {
	for _, event := range []coffLazyEvent{coffLazyLoadFailure, coffLazyLookupFailure} {
		hooks := (coffNativeDelayHooks{failure: 21}).bind(10, coffLazyHooks{}, func(_, _ uintptr, code uint32, _, _ uintptr, number uint32, _ bool) uintptr {
			want := coffDelayMissingModule
			if event == coffLazyLookupFailure {
				want = coffDelayMissingProcedure
			}
			if code != uint32(event) || number != want {
				t.Fatal(code, number, want)
			}
			return 0
		})
		if result := hooks.failureEvent(event, coffLazyInfo{err: errors.New("non-native loader error")}); result != (coffLazyResult{}) {
			t.Fatal(result)
		}
	}
	nativeCalls := 0
	hooks := (coffNativeDelayHooks{notify: 20}).bind(10, coffLazyHooks{notify: func(coffLazyEvent, coffLazyInfo) coffLazyResult { panic("hook panic") }}, func(_, _ uintptr, _ uint32, _, _ uintptr, _ uint32, _ bool) uintptr { nativeCalls++; return 142 })
	p, _ := newCOFFLazyProviders(lazyProviderObjects(lazyProviderEntry("missing.dll", "add")), nil, nil, nil, coffLazyOperations{}, coffLazyHooks{})
	if _, code, err := p.resolveWithHooks(0, 1, coffLazyResult{}, hooks); code != coffDelayInvalidParameter || err == nil || nativeCalls != 0 || len(p.active) != 0 {
		t.Fatal(code, err, nativeCalls)
	}
}
