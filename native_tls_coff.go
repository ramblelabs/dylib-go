package dylib

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"

	"github.com/ramblelabs/dylib-go/internal/native"
	"github.com/ramblelabs/dylib-go/internal/petls"
)

const coffTLSSection = 0x100
const coffTLSIndex = "_tls_index"
const coffTLSArray = "_tls_array"
const coffTLSGuard = "__tls_guard"

type coffTLSLayout struct {
	sections           map[*section]uint64
	size, align, index uint64
	handle             uintptr
	directory          string
}

func coffTLSRuntime(name string) bool {
	return name == coffTLSIndex || name == coffTLSArray || name == coffTLSGuard
}

func usesMSVCNativeTLS(objects []*object) bool {
	for _, o := range objects {
		if o.info.Format != "COFF" || o.info.OS != "windows" {
			continue
		}
		for _, s := range o.symbols {
			if s.section != -3 && s.global && strings.TrimPrefix(s.name, "__imp_") == coffTLSGuard {
				return true
			}
		}
	}
	return false
}

// The runtime guard is real per-module native TLS, separate from any emulated
// descriptor. Add a private definition only after validating all input symbols.
func coffTLSGuardObject(objects []*object, defs map[string]definition) ([]*object, map[string]definition) {
	if !usesMSVCNativeTLS(objects) {
		return objects, defs
	}
	o := &object{info: objects[0].info, sections: []*section{nil, {name: ".tls$ZZZ", size: 1, align: 1, write: true, nativeTLS: coffTLSSection}}, symbols: []symbol{{name: coffTLSGuard, section: 1, global: true}}}
	o.info.Name = "<native MSVC TLS guard>"
	owned := make(map[string]definition, len(defs)+1)
	for name, d := range defs {
		owned[name] = d
	}
	owned[coffTLSGuard] = definition{o, 0}
	return append(append([]*object(nil), objects...), o), owned
}

func checkCOFFTLS(objects []*object, enabled bool) (bool, error) {
	needed := false
	for _, o := range objects {
		if o.info.Format != "COFF" {
			continue
		}
		for _, s := range o.sections {
			if s == nil || s.nativeTLS == 0 {
				continue
			}
			needed = true
			if s.nativeTLS != coffTLSSection || !s.write || s.exec || s.lifecycle != 0 || s.align == 0 || s.align > 4096 || s.align&(s.align-1) != 0 || s.size > maxImage {
				return false, fmt.Errorf("%s: invalid native COFF TLS section %s", o.info.Name, s.name)
			}
		}
		for _, s := range o.symbols {
			if s.name == coffTLSGuard && !s.global {
				continue
			}
			if s.section == -3 || !coffTLSRuntime(strings.TrimPrefix(s.name, "__imp_")) {
				continue
			}
			needed = true
			if s.section != 0 || !s.global || s.weak || s.name != strings.TrimPrefix(s.name, "__imp_") || s.imported != nil || s.alias != nil || s.forward != nil || s.name == coffTLSArray && o.info.Arch != "386" {
				return false, fmt.Errorf("native COFF TLS requires the image-owned module index and native TLS array constant")
			}
		}
	}
	if needed && !enabled {
		return false, fmt.Errorf("native COFF TLS requires Options.NativeTLS")
	}
	return needed, nil
}

func (im *image) layoutCOFFTLS(start uint64) (uint64, error) {
	tls := &coffTLSLayout{sections: make(map[*section]uint64), align: 1, index: alignUp(start, im.page)}
	var sections []*section
	for _, o := range im.objects {
		for _, s := range o.sections {
			if s != nil && s.nativeTLS == coffTLSSection {
				sections = append(sections, s)
			}
		}
	}
	// MS COFF concatenates identically named sections in input order and orders
	// dollar suffixes lexically within the final .tls section.
	sort.SliceStable(sections, func(i, j int) bool { return sections[i].name < sections[j].name })
	for _, s := range sections {
		tls.size = alignUp(tls.size, s.align)
		tls.sections[s] = tls.size
		tls.size += s.size
		if s.align > tls.align {
			tls.align = s.align
		}
		if tls.size > maxImage {
			return 0, fmt.Errorf("native COFF TLS template exceeds image budget")
		}
	}
	if tls.size == 0 {
		return 0, fmt.Errorf("native COFF TLS requires an image-owned template")
	}
	if tls.index+im.page > maxImage {
		return 0, fmt.Errorf("native COFF TLS index exceeds image budget")
	}
	im.coffTLS = tls
	return tls.index + im.page, nil
}

func (im *image) prepareCOFFTLS() error {
	tls := im.coffTLS
	template := make([]byte, int(tls.size))
	for s, off := range tls.sections {
		copy(template[off:off+s.size], im.mem[s.offset:s.offset+s.size])
	}
	b, err := petls.Image(im.objects[0].info.Arch, template, tls.align)
	if err != nil {
		return err
	}
	tls.directory, err = os.MkdirTemp("", "dylib-tls-")
	if err != nil {
		return err
	}
	path := filepath.Join(tls.directory, filepath.Base(tls.directory)+".dll")
	if err = os.WriteFile(path, b, 0600); err != nil {
		return err
	}
	tls.handle, err = native.Open(path)
	if err != nil {
		return fmt.Errorf("load native COFF TLS carrier: %w", err)
	}
	// A normal LoadLibrary handle is the module base. The generated minimal
	// image has a fixed RVA for the OS-written index, independent of ASLR.
	address := tls.handle + petls.IndexRVA
	p := *(*unsafe.Pointer)(unsafe.Pointer(&address))
	index := *(*uint32)(p)
	if index == 0xffffffff {
		directoryAddress := tls.handle + petls.IndexRVA + 32
		directoryPointer := *(*unsafe.Pointer)(unsafe.Pointer(&directoryAddress))
		return fmt.Errorf("Windows did not allocate the native TLS module index (module %#x, TLS directory %x)", tls.handle, unsafe.Slice((*byte)(directoryPointer), int(4*im.pointerSize+8)))
	}
	le.PutUint32(im.mem[tls.index:], index)
	return nil
}

func (tls *coffTLSLayout) close(closeLibrary func(uintptr) error) error {
	if tls == nil {
		return nil
	}
	if tls.handle != 0 {
		if err := closeLibrary(tls.handle); err != nil {
			return err
		}
		tls.handle = 0
	}
	if tls.directory != "" {
		if err := os.RemoveAll(tls.directory); err != nil {
			return err
		}
		tls.directory = ""
	}
	return nil
}

func (im *image) relocateCOFFTLS(o *object, r relocation, b []byte) (bool, error) {
	if im.coffTLS == nil {
		return false, nil
	}
	d, err := im.resolveDefinition(o, r.symbol)
	if err != nil {
		return true, err
	}
	target := nativeTLSSection(d.o, d.sym())
	if target == nil {
		return false, nil
	}
	sectionRelative := r.typ == 11
	if o.info.Arch == "arm64" {
		sectionRelative = r.typ >= 8 && r.typ <= 11
	}
	if target.nativeTLS != coffTLSSection || !sectionRelative {
		return true, fmt.Errorf("native COFF TLS variables require section-relative offsets; use a native getter")
	}
	return true, im.relocCOFFSection(o, r, b)
}
