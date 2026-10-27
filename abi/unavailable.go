//go:build !libffi || !cgo

package abi

func prepare(Signature) (callBackend, error) { return nil, ErrUnavailable }
func Available() bool                        { return false }
