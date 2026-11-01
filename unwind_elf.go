package dylib

import (
	"encoding/binary"
	"fmt"
)

func (im *image) registerELFUnwind() error {
	frames, err := im.elfFrameSections()
	if err != nil || len(frames) == 0 {
		return err
	}
	return im.registerDwarfFrames("libgcc_s.so.1", frames)
}

func (im *image) elfFrameSections() ([]uintptr, error) {
	var frames []uintptr
	for _, o := range im.objects {
		for _, s := range o.sections {
			if s == nil || !elfUnwindSection(s.name) {
				continue
			}
			if s.exec || s.write || s.tail != 4 || s.offset+s.size+4 > uint64(len(im.mem)) {
				return nil, fmt.Errorf("%s: invalid ELF frame storage", o.info.Name)
			}
			b := im.mem[s.offset : s.offset+s.size]
			fdes, err := im.validateFrameRecords(b, s.offset, framePolicy{language: true, arm64RASigning: o.info.Arch == "arm64"})
			if err != nil {
				return nil, fmt.Errorf("%s: %s: %w", o.info.Name, s.name, err)
			}
			if binary.LittleEndian.Uint32(im.mem[s.offset+s.size:]) != 0 {
				return nil, fmt.Errorf("%s: missing ELF frame terminator", o.info.Name)
			}
			if len(fdes) != 0 {
				frames = append(frames, im.base+uintptr(s.offset))
			}
		}
	}
	return frames, nil
}
