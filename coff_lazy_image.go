package dylib

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"unsafe"

	"github.com/ramblelabs/dylib-go/internal/native"
)

type coffLazyAllocation struct {
	context, cell, descriptor, module, names, dll, hint uint64
}

type coffLazyImage struct {
	providers   *coffLazyProviders
	aliases     map[*coffImport]int
	slots       []coffLazyAllocation
	id          uintptr
	base        uintptr
	functions   *runtimeFunctions
	keep        bool
	nativeHooks coffNativeDelayHooks
}

func newCOFFLazyImage(objects []*object, imports map[*coffImport]uintptr, opts Options, borrowed map[string]uintptr) (*coffLazyImage, error) {
	if opts.DelayImports != DelayImportsLazy {
		return nil, nil
	}
	providers, aliases := newCOFFLazyProviders(objects, imports, opts.LibraryPaths, borrowed, coffLazyNativeOperations(), delayProviderHooks(opts.DelayHooks))
	if len(providers.slots) == 0 {
		return nil, nil
	}
	if runtime.GOOS != "windows" || native.COFFDelayHelper() == 0 {
		return nil, fmt.Errorf("raw COFF lazy imports require a native Windows Go/llgo host")
	}
	return &coffLazyImage{providers: providers, aliases: aliases, slots: make([]coffLazyAllocation, len(providers.slots)), keep: opts.KeepLibraries}, nil
}

func (lazy *coffLazyImage) layout(start, width, page uint64) (uint64, error) {
	if lazy == nil {
		return start, nil
	}
	position := alignUp(start, page)
	for index, slot := range lazy.providers.slots {
		allocation := &lazy.slots[index]
		allocation.context = alignUp(position, width)
		allocation.cell = alignUp(allocation.context+uint64(native.COFFDelayEntrySize()), width)
		allocation.descriptor = alignUp(allocation.cell+width, 4)
		allocation.module = alignUp(allocation.descriptor+32, width)
		allocation.names = allocation.module + width
		allocation.dll = allocation.names + 2*width
		allocation.hint = alignUp(allocation.dll+uint64(len(slot.entry.dll))+1, 2)
		position = allocation.hint + 2 + uint64(len(slot.entry.name)) + 1
		if position > maxImage {
			return 0, fmt.Errorf("lazy import metadata exceeds image limit")
		}
	}
	return alignUp(position, page), nil
}

func (lazy *coffLazyImage) codeSize() uint64 {
	if lazy == nil {
		return 0
	}
	return uint64(len(lazy.slots)) * 512 // Includes alignment and native xdata.
}

func nativeWord(address uintptr) *uintptr {
	pointer := *(*unsafe.Pointer)(unsafe.Pointer(&address))
	return (*uintptr)(pointer)
}

func (lazy *coffLazyImage) prepare(im *image) error {
	if lazy == nil {
		return nil
	}
	var err error
	if lazy.nativeHooks, err = im.coffNativeDelayHooks(); err != nil {
		return err
	}
	id, callback, err := registerCOFFLazyImage(lazy)
	if err != nil {
		return err
	}
	lazy.id = id
	lazy.base = im.base
	if im.imports == nil {
		im.imports = make(map[*coffImport]uintptr)
	}
	addresses := make([]uintptr, len(lazy.slots))
	var functions []byte
	putWord := func(offset, value uint64) {
		if im.pointerSize == 4 {
			le.PutUint32(im.mem[offset:], uint32(value))
		} else {
			le.PutUint64(im.mem[offset:], value)
		}
	}
	for index, allocation := range lazy.slots {
		slot := lazy.providers.slots[index]
		copy(im.mem[allocation.dll:], slot.entry.dll+"\x00")
		le.PutUint32(im.mem[allocation.descriptor:], 1)
		for offset, value := range map[uint64]uint64{4: allocation.dll, 8: allocation.module, 12: allocation.cell, 16: allocation.names} {
			le.PutUint32(im.mem[allocation.descriptor+offset:], uint32(value))
		}
		name := im.base + uintptr(allocation.hint+2)
		if slot.entry.ordinal != 0 {
			name = 0
			putWord(allocation.names, uint64(1)<<(im.pointerSize*8-1)|uint64(slot.entry.ordinal))
		} else {
			copy(im.mem[allocation.hint+2:], slot.entry.name+"\x00")
			putWord(allocation.names, allocation.hint)
		}
		if !native.ConfigureCOFFDelayEntry(im.base+uintptr(allocation.context), callback, id, uintptr(index), im.base+uintptr(allocation.descriptor), im.base+uintptr(allocation.cell), im.base+uintptr(allocation.dll), name, slot.entry.ordinal) {
			return fmt.Errorf("configure lazy import %s", slot.entry.public)
		}
		entry, err := makeCOFFLazyEntry(runtime.GOARCH, im.base+uintptr(allocation.cell), im.base+uintptr(allocation.context), native.COFFDelayHelper())
		if err != nil {
			return err
		}
		code := alignUp(im.stubNext, 16)
		xdata := alignUp(code+uint64(len(entry.code)), 4)
		end := alignUp(xdata+uint64(len(entry.unwind)), 16)
		if end > uint64(len(im.mem)) {
			return fmt.Errorf("lazy import code exceeds reserved image storage")
		}
		copy(im.mem[code:], entry.code)
		copy(im.mem[xdata:], entry.unwind)
		im.stubNext = end
		addresses[index] = im.base + uintptr(code)
		if runtime.GOARCH != "386" {
			size := 12
			if runtime.GOARCH == "arm64" {
				size = 8
			}
			record := make([]byte, size)
			le.PutUint32(record, uint32(code)+entry.slow)
			if size == 12 {
				le.PutUint32(record[4:], uint32(code)+uint32(len(entry.code)))
			}
			le.PutUint32(record[size-4:], uint32(xdata))
			functions = append(functions, record...)
		}
	}
	for entry, index := range lazy.aliases {
		im.imports[entry] = addresses[index]
	}
	lazy.providers.reset = func(index int, address uintptr) {
		allocation := lazy.slots[index]
		atomic.StoreUintptr(nativeWord(im.base+uintptr(allocation.cell)), address)
		module := lazy.providers.slots[index].module.handle // Provider mutex held.
		atomic.StoreUintptr(nativeWord(im.base+uintptr(allocation.module)), module)
	}
	if len(functions) != 0 {
		lazy.functions = &runtimeFunctions{}
		lazy.functions.mem, err = native.Alloc(len(functions))
		if err != nil {
			return err
		}
		copy(lazy.functions.mem, functions)
		if err := native.Protect(lazy.functions.mem, false, false); err != nil {
			return err
		}
		if err := native.AddFunctionTable(uintptr(unsafe.Pointer(&lazy.functions.mem[0])), uint32(len(lazy.slots)), im.base); err != nil {
			return err
		}
		lazy.functions.registered = true
	}
	return nil
}

func (lazy *coffLazyImage) releaseProviders() (bool, error) {
	if lazy == nil || lazy.keep {
		return false, nil
	}
	lazy.providers.mu.Lock()
	changed := len(lazy.providers.pending) != 0
	for _, module := range lazy.providers.order {
		changed = changed || module.owned && module.handle != 0
	}
	lazy.providers.mu.Unlock()
	return changed, lazy.providers.unload()
}

func (lazy *coffLazyImage) closeFunctions() error {
	if lazy == nil {
		return nil
	}
	return lazy.functions.close()
}

func (lazy *coffLazyImage) forget() {
	if lazy != nil && lazy.id != 0 {
		unregisterCOFFLazyImage(lazy.id)
		lazy.id = 0
	}
}
