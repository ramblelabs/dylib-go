//go:build libffi && cgo && (linux || darwin || windows)

package cxx_test

import (
	"path/filepath"
	"testing"
)

func TestNativeDynamicHeaderHierarchyFacade(t *testing.T) {
	header, err := filepath.Abs("testdata/header_hierarchy.hpp")
	if err != nil {
		t.Fatal(err)
	}
	facade, err := generateHeaderHierarchy(t)(filepath.ToSlash(header))
	if err != nil {
		t.Fatal(err)
	}
	testDynamicHierarchy(t, facade)
}
