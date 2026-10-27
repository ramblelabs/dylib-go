//go:build !cgo

package abi

func StorageAvailable() bool                             { return false }
func prepareValue(TypeDesc, Value) (valueBackend, error) { return nil, ErrUnavailable }
