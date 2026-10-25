package dylib

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/ramblelabs/dylib-go/internal/native"
)

const emulatedTLSMSVCGuard = emulatedTLSPrefix + "__tls_guard"
const emulatedTLSMSVCInit = "__dyn_tls_on_demand_init"
const emulatedTLSMSVCExit = "__tlregdtor"

func emulatedTLSMSVCHelper(name string) bool {
	return name == emulatedTLSMSVCInit || name == emulatedTLSMSVCExit
}

func usesMSVCEmulatedTLS(objects []*object) bool {
	for _, o := range objects {
		if usesMSVCNativeTLS([]*object{o}) {
			for _, s := range o.symbols {
				if s.section != -3 && strings.TrimPrefix(s.name, "__imp_") == emulatedTLSMSVCGuard {
					return true
				}
			}
			continue
		}
		for _, sec := range o.sections {
			if sec != nil && sec.lifecycle == lifecycleThreadInit {
				return true
			}
		}
		for _, s := range o.symbols {
			if s.section != -3 && (emulatedTLSMSVCHelper(strings.TrimPrefix(s.name, "__imp_")) || o.info.OS == "windows" && s.name == emulatedTLSMSVCGuard && s.section == 0) {
				return true
			}
		}
	}
	return false
}

func (im *image) prepareMSVCEmulatedTLSHelpers(needed map[string]bool) error {
	guard, ok := im.common[emulatedTLSMSVCGuard]
	if ok {
		// Keep the runtime descriptor near raw code, including RIP-relative and ADRP
		// references. Common data stays writable independently of the read-only GOT.
		im.hooks[emulatedTLSMSVCGuard] = im.base + uintptr(guard.offset)
		if im.pointerSize == 4 {
			binary.LittleEndian.PutUint32(im.mem[guard.offset:], 1)
			binary.LittleEndian.PutUint32(im.mem[guard.offset+4:], 1)
		} else {
			binary.LittleEndian.PutUint64(im.mem[guard.offset:], 1)
			binary.LittleEndian.PutUint64(im.mem[guard.offset+8:], 1)
		}
	}
	for _, name := range []string{emulatedTLSMSVCInit, emulatedTLSMSVCExit} {
		if !needed[name] {
			continue
		}
		args, target := 0, native.EmulatedTLSMSVCInitHelper()
		if name == emulatedTLSMSVCExit {
			args, target = 1, native.EmulatedTLSMSVCExitHelper()
		}
		if err := im.bindNativeHelper(name, im.emulatedTLS.Context(), target, args); err != nil {
			return err
		}
	}
	return nil
}

func (im *image) prepareMSVCThreadInitializers() error {
	guard := im.hooks[emulatedTLSMSVCGuard]
	var getter uintptr
	if d, ok := im.defs[coffTLSGuard]; ok && im.coffTLS != nil {
		offset := im.coffTLS.sections[d.o.sections[d.sym().section]]
		code, err := coffTLSGuardEntry(im.objects[0].info.Arch, im.base+uintptr(im.coffTLS.index), uint32(offset))
		if err != nil {
			return err
		}
		if im.stubNext+uint64(len(code)) > uint64(len(im.mem)) {
			return fmt.Errorf("native TLS guard entry capacity exceeded")
		}
		getter = im.base + uintptr(im.stubNext)
		copy(im.mem[im.stubNext:], code)
		im.stubNext += alignUp(uint64(len(code)), 16)
		if err := im.emulatedTLS.SetNativeThreadGuard(getter); err != nil {
			return err
		}
	}
	if guard == 0 && getter == 0 {
		if len(im.threadInitializers) != 0 {
			return fmt.Errorf("MSVC thread initializers require an image-owned TLS guard")
		}
		return nil
	}
	return im.emulatedTLS.SetThreadInitializers(guard, im.threadInitializers)
}
