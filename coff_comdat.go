package dylib

import (
	"fmt"
	"strings"
)

func coffOmittedSection(s coffInputSection) bool {
	return s.flags&0x200 != 0 || strings.HasPrefix(s.name, ".debug") || s.name == ".drectve"
}

// Build once so objects with many functions do not rescan every leader for
// each unwind table. A negative index records an ambiguous executable suffix.
func coffUnwindParents(f *coffInput, groups map[int]sectionGroup, leaders map[int]int) map[string]int {
	parents := make(map[string]int)
	for index, leader := range leaders {
		s := f.sections[index-1]
		_, suffix, ok := strings.Cut(s.name, "$")
		if leader < 0 || !ok || suffix == "" || s.flags&0x20000000 == 0 || groups[index].selection == 5 || coffOmittedSection(s) {
			continue
		}
		if _, exists := parents[suffix]; exists {
			parents[suffix] = -1
		} else {
			parents[suffix] = index
		}
	}
	return parents
}

// MinGW emits leaderless unwind COMDATs with ANY selection. LLVM's COFF linker
// treats their suffix as an implicit association to the executable COMDAT with
// the same suffix, including decorated i386 and private/weak text leaders.
func coffImplicitParent(section coffInputSection, parents map[string]int) (int, error) {
	name := section.name
	suffix := ""
	for _, prefix := range []string{".pdata$", ".xdata$", ".eh_frame$"} {
		if strings.HasPrefix(name, prefix) {
			suffix = strings.TrimPrefix(name, prefix)
			break
		}
	}
	if suffix == "" {
		return 0, fmt.Errorf("COFF COMDAT has no external or static leader: %s", name)
	}
	if section.flags&0x20000000 != 0 {
		return 0, fmt.Errorf("executable implicit COFF unwind metadata: %s", name)
	}
	parent := parents[suffix]
	if parent < 0 {
		return 0, fmt.Errorf("ambiguous implicit COFF COMDAT parent: %s", name)
	}
	if parent == 0 {
		return 0, fmt.Errorf("missing implicit COFF COMDAT parent: %s", name)
	}
	return parent, nil
}
