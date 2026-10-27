package dylib

import "testing"

func assertMachOSharedFrameAlias(t *testing.T, o *object) {
	t.Helper()
	for _, s := range o.symbols {
		if s.name != "coal_frame_keep" {
			continue
		}
		if !s.global || !s.weak || s.forward == nil || !s.forward.storage || s.forward.offset != 0 || o.symbols[s.forward.target].name != "coal_frame_zkeep" {
			t.Fatal("frame fixture lacks a public alternate with its non-alternate owner", s)
		}
		return
	}
	t.Fatal("frame fixture lacks its public keep alias")
}

func TestMachOSharedPublicCoalescedFrames(t *testing.T) {
	testMachOFramePruning(t, false, true, true)
}

func TestMachOSharedPublicOrdinaryFrames(t *testing.T) {
	testMachOFramePruning(t, true, true, true)
}

func TestNativeMachOSharedPublicCoalescedFrames(t *testing.T) {
	testNativeMachOFrames(t, false, true, true)
}

func TestNativeMachOSharedPublicOrdinaryFrames(t *testing.T) {
	testNativeMachOFrames(t, true, true, true)
}
