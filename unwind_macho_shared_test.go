package dylib

import "testing"

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
