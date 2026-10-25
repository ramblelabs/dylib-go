package dylib

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	"github.com/ramblelabs/dylib-go/internal/native"
)

const emulatedTLSHelper = "__emutls_get_address"
const emulatedTLSPrefix = "__emutls_v."
const emulatedTLSTLVExit = "_tlv_atexit"

var emulatedTLSThreadHelpers = [...]string{"__cxa_thread_atexit", "__cxa_thread_atexit_impl", emulatedTLSTLVExit}

func emulatedTLSReserved(name string) bool {
	return name == emulatedTLSHelper || strings.HasPrefix(name, emulatedTLSPrefix) || emulatedTLSThreadHelper(name) || emulatedTLSMSVCHelper(name) || emulatedTLSUnsupportedRuntime(name)
}

func emulatedTLSThreadHelper(name string) bool {
	for _, helper := range emulatedTLSThreadHelpers {
		if name == helper {
			return true
		}
	}
	return false
}

func emulatedTLSUnsupportedRuntime(name string) bool {
	switch name {
	case "__tlv_atexit", "_tlregdtor":
		return true
	}
	return false
}

func checkTLSRuntime(objects []*object, opts Options) (bool, error) {
	enabled := opts.EmulatedTLS
	needed := false
	for _, o := range objects {
		nativeMSVC := opts.NativeTLS && usesMSVCNativeTLS([]*object{o})
		for _, sec := range o.sections {
			if sec != nil && sec.lifecycle == lifecycleThreadInit {
				needed = true
				if !enabled && !nativeMSVC {
					return false, fmt.Errorf("%s: emulated TLS requires Options.EmulatedTLS", o.info.Name)
				}
			}
		}
		for _, s := range o.symbols {
			name := strings.TrimPrefix(s.name, "__imp_")
			if s.section == -3 || !emulatedTLSReserved(name) {
				continue
			}
			needed = true
			nativeThreadHook := opts.NativeTLS && (o.info.Format == "ELF" || o.info.Format == "Mach-O" || o.info.Format == "COFF") && emulatedTLSThreadHelper(name)
			nativeMSVCHook := opts.NativeTLS && o.info.OS == "windows" && o.info.Format == "COFF" && emulatedTLSMSVCHelper(name) && (name == emulatedTLSMSVCExit || nativeMSVC)
			if !enabled && !nativeThreadHook && !nativeMSVCHook {
				return false, fmt.Errorf("%s: TLS runtime requires Options.EmulatedTLS, or Options.NativeTLS for native thread-exit hooks", o.info.Name)
			}
			if s.imported != nil || s.name != name && strings.HasPrefix(name, emulatedTLSPrefix) || name == emulatedTLSHelper && (s.section != 0 || s.alias != nil) {
				return false, fmt.Errorf("%s: emulated TLS requires image-owned descriptors and the host helper", o.info.Name)
			}
		}
	}
	if needed {
		msvc := usesMSVCEmulatedTLS(objects)
		for _, o := range objects {
			for _, s := range o.symbols {
				name := strings.TrimPrefix(s.name, "__imp_")
				if s.section == -3 {
					continue
				}
				if emulatedTLSUnsupportedRuntime(name) || name == emulatedTLSTLVExit && o.info.OS != "darwin" || emulatedTLSMSVCHelper(name) && o.info.OS != "windows" {
					return false, fmt.Errorf("%s: emulated TLS thread destructors/dynamic runtime initialization are unsupported", o.info.Name)
				}
				if (emulatedTLSThreadHelper(name) || emulatedTLSMSVCHelper(name) || msvc && name == emulatedTLSMSVCGuard && s.global) && (s.section != 0 || s.imported != nil || s.alias != nil) {
					return false, fmt.Errorf("%s: TLS thread destructors require the image-owned host helper", o.info.Name)
				}
			}
		}
	}
	return needed, nil
}

// Validate the relocated compiler descriptors before any native initialization.
// No native allocator or runtime helper reads an unvalidated descriptor.
func (im *image) emulatedTLSDescriptors() ([]uintptr, error) {
	word := im.pointerSize
	if word != 4 && word != 8 {
		return nil, fmt.Errorf("invalid emulated TLS pointer size")
	}
	contains := func(address, size uint64, writable bool) bool {
		if address < uint64(im.base) {
			return false
		}
		offset := address - uint64(im.base)
		for _, o := range im.objects {
			for _, sec := range o.sections {
				if sec != nil && !sec.exec && (!writable || sec.write) && offset >= sec.offset && offset-sec.offset <= sec.size && size <= sec.size-(offset-sec.offset) {
					return offset <= uint64(len(im.mem)) && size <= uint64(len(im.mem))-offset
				}
			}
		}
		return false
	}
	read := func(offset uint64) uint64 {
		if word == 4 {
			return uint64(binary.LittleEndian.Uint32(im.mem[offset:]))
		}
		return binary.LittleEndian.Uint64(im.mem[offset:])
	}
	seen := make(map[uintptr]bool)
	var descriptors []uintptr
	var budget uint64
	if im.machoTLS != nil {
		budget = im.machoTLS.size + im.machoTLS.align - 1
	}
	if im.elfTLS != nil {
		budget += im.elfTLS.size + im.elfTLS.align - 1
	}
	if im.coffTLS != nil {
		budget += im.coffTLS.size + im.coffTLS.align - 1
	}
	for _, o := range im.objects {
		for i, s := range o.symbols {
			if s.section == -3 || !strings.HasPrefix(s.name, emulatedTLSPrefix) {
				continue
			}
			if _, private := im.common[emulatedTLSMSVCGuard]; private && s.name == emulatedTLSMSVCGuard && s.section == 0 {
				continue // Validated private image storage, not an input definition.
			}
			d, err := im.resolveDefinition(o, i)
			if err != nil {
				return nil, err
			}
			v := d.sym()
			if v.section <= 0 {
				return nil, fmt.Errorf("%s: emulated TLS descriptor must belong to the image", s.name)
			}
			address, err := im.symbol(d.o, d.index, false)
			if err != nil {
				return nil, err
			}
			if seen[address] {
				continue
			}
			if uint64(address)%word != 0 || !contains(uint64(address), 4*word, true) || v.size != 0 && v.size != 4*word {
				return nil, fmt.Errorf("%s: invalid emulated TLS descriptor storage", s.name)
			}
			offset := uint64(address - im.base)
			size, alignment, object, initial := read(offset), read(offset+word), read(offset+2*word), read(offset+3*word)
			if size == 0 || size > maxImage || alignment == 0 || alignment > maxImage || alignment&(alignment-1) != 0 || object != 0 {
				return nil, fmt.Errorf("%s: invalid emulated TLS descriptor fields", s.name)
			}
			if initial != 0 && !contains(initial, size, false) {
				return nil, fmt.Errorf("%s: emulated TLS template must belong to image data", s.name)
			}
			budget += size + alignment - 1
			if budget > maxImage || len(descriptors) >= 4096 {
				return nil, fmt.Errorf("emulated TLS per-thread allocation budget exceeded")
			}
			seen[address] = true
			descriptors = append(descriptors, address)
		}
	}
	if guard, ok := im.common[emulatedTLSMSVCGuard]; ok {
		if guard.offset > uint64(len(im.mem)) || guard.size != 4*word || guard.size > uint64(len(im.mem))-guard.offset || (uint64(im.base)+guard.offset)%word != 0 ||
			read(guard.offset) != 1 || read(guard.offset+word) != 1 || read(guard.offset+2*word) != 0 || read(guard.offset+3*word) != 0 {
			return nil, fmt.Errorf("invalid image-owned MSVC emulated TLS guard")
		}
		if len(descriptors) >= 4096 || budget >= maxImage {
			return nil, fmt.Errorf("emulated TLS per-thread allocation budget exceeded")
		}
		descriptors = append(descriptors, im.base+uintptr(guard.offset))
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i] < descriptors[j] })
	for i := 1; i < len(descriptors); i++ {
		if descriptors[i]-descriptors[i-1] < uintptr(4*word) {
			return nil, fmt.Errorf("overlapping emulated TLS descriptors")
		}
	}
	if len(descriptors) == 0 {
		for _, o := range im.objects {
			for _, s := range o.symbols {
				if s.section != -3 && strings.TrimPrefix(s.name, "__imp_") == emulatedTLSHelper {
					return nil, fmt.Errorf("emulated TLS address helper requires an image-owned descriptor")
				}
			}
		}
	}
	return descriptors, nil
}

func (im *image) prepareEmulatedTLS() error {
	descriptors, err := im.emulatedTLSDescriptors()
	if err != nil {
		return err
	}
	for _, address := range descriptors {
		if err := im.emulatedTLS.Add(address); err != nil {
			return err
		}
	}
	return nil
}

func (im *image) prepareEmulatedTLSHelpers() error {
	var err error
	im.emulatedTLS, err = native.NewEmulatedTLS()
	if err != nil {
		return err
	}
	if im.hooks == nil {
		im.hooks = make(map[string]uintptr)
	}
	im.hooks[emulatedTLSHelper] = native.EmulatedTLSHelper()
	needed := make(map[string]bool)
	for _, o := range im.objects {
		for _, s := range o.symbols {
			name := strings.TrimPrefix(s.name, "__imp_")
			if s.section == 0 && (emulatedTLSThreadHelper(name) || emulatedTLSMSVCHelper(name)) {
				needed[name] = true
			}
		}
	}
	for _, name := range emulatedTLSThreadHelpers {
		if needed[name] {
			if err := im.bindNativeHelper(name, im.emulatedTLS.Context(), native.EmulatedTLSThreadExitHelper(), 3); err != nil {
				return err
			}
		}
	}
	return im.prepareMSVCEmulatedTLSHelpers(needed)
}

func (im *image) drainThreadDestructors() (bool, error) {
	pending, drained := im.emulatedTLS.DrainThreadDestructors()
	if pending != 0 {
		return drained, fmt.Errorf("%w: %d pending or active callbacks", ErrPendingThreadDestructors, pending)
	}
	return drained, nil
}
