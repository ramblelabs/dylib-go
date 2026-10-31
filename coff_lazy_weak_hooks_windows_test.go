//go:build windows

package dylib

import (
	"fmt"
	"testing"

	"github.com/ramblelabs/dylib-go/internal/native"
)

func TestNativeCOFFWeakDelayHookVariables(t *testing.T) {
	needNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		for _, storage := range []string{"weak", "weak_chain", "weak_const", "weak_crt", "weak_archive"} {
			t.Run(opt+"/"+storage, func(t *testing.T) {
				s, _ := nativeHookFixture(t, opt, storage, 0, "add", 0, DelayImportHooks{})
				for _, pair := range [][2]string{{"__pfnDliNotifyHook2", "hook_notify_storage"}, {"__pfnDliFailureHook2", "hook_failure_storage"}} {
					public, err := s.Lookup(pair[0])
					if err != nil {
						t.Fatal(err)
					}
					selected, err := s.Lookup(pair[1])
					if err != nil || public != selected {
						t.Fatal("weak hook did not use ordinary symbol selection", public, selected, err)
					}
				}
				call(t, s, "entry", 20, 22, 42)
				call(t, s, "entry", 20, 22, 42)
				if got := nativeHookEvents(t, s); fmt.Sprint(got) != "[0 1 2 5]" {
					t.Fatal("weak SDK hook/cache", got)
				}
				if storage == "weak" {
					// The alias denotes the cell itself, so quiescent writes and
					// explicit resets have the same behavior as direct definitions.
					*probeNativeWord(t, s, "__pfnDliNotifyHook2") = 0
					*probeNativeWord(t, s, "__pfnDliFailureHook2") = 0
					if err := s.UnloadDelayImports(); err != nil {
						t.Fatal(err)
					}
					call(t, s, "entry", 20, 22, 42)
					if got := nativeHookEvents(t, s); fmt.Sprint(got) != "[0 1 2 5]" {
						t.Fatal("weak null hook was cached", got)
					}
					install, err := s.Resolve("install_hooks")
					if err != nil {
						t.Fatal(err)
					}
					if err := install.WithAddress(func(address uintptr) error { native.CallVoid(address); return nil }); err != nil {
						t.Fatal(err)
					}
					if err := s.UnloadDelayImports(); err != nil {
						t.Fatal(err)
					}
					call(t, s, "entry", 20, 22, 42)
					if got := nativeHookEvents(t, s); fmt.Sprint(got) != "[0 1 2 5 0 1 2 5]" {
						t.Fatal("weak hook reinstallation/reset", got)
					}
				}
			})
		}
	}
}

func TestNativeCOFFWeakDelayHookFailures(t *testing.T) {
	needNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		for _, tc := range []struct {
			name   string
			mode   uint32
			export string
			events string
		}{{"module", 4, "add", "[0 1 3 2 5]"}, {"procedure", 5, "missing", "[0 1 2 4 5]"}} {
			t.Run(opt+"/"+tc.name, func(t *testing.T) {
				s, _ := nativeHookFixture(t, opt, "weak_chain", tc.mode, tc.export, 0, DelayImportHooks{})
				call(t, s, "entry", 20, 22, 42)
				call(t, s, "entry", 20, 22, 42)
				if got := nativeHookEvents(t, s); fmt.Sprint(got) != tc.events {
					t.Fatal("weak failure hook/cache", got, tc.events)
				}
			})
		}
	}
}

func TestNativeCOFFWeakDelayHookStrongAndUnused(t *testing.T) {
	needNative(t)
	for _, opt := range []string{"-O0", "-O2"} {
		for _, storage := range []string{"weak_strong", "weak_archive_unused"} {
			t.Run(opt+"/"+storage, func(t *testing.T) {
				s, _ := nativeHookFixture(t, opt, storage, 0, "add", 0, DelayImportHooks{})
				call(t, s, "entry", 20, 22, 42)
				if got := nativeHookEvents(t, s); len(got) != 0 {
					t.Fatal("fallback bypassed strong selection or extracted unused archive", got)
				}
				if storage == "weak_archive_unused" {
					if _, selected := s.image.defs["hook_alias_root"]; selected || s.image.lazy.nativeHooks != (coffNativeDelayHooks{}) {
						t.Fatal("unused metadata archive was extracted")
					}
					return
				}
				for _, pair := range [][2]string{{"__pfnDliNotifyHook2", "hook_notify_storage"}, {"__pfnDliFailureHook2", "hook_failure_storage"}} {
					*probeNativeWord(t, s, pair[0]) = *probeNativeWord(t, s, pair[1])
				}
				if err := s.UnloadDelayImports(); err != nil {
					t.Fatal(err)
				}
				call(t, s, "entry", 20, 22, 42)
				if got := nativeHookEvents(t, s); fmt.Sprint(got) != "[0 1 2 5]" {
					t.Fatal("strong pointer storage was not authoritative", got)
				}
			})
		}
	}
}
