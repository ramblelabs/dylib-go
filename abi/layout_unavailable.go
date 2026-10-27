//go:build !cgo

package abi

func layoutOf(TypeDesc, Convention) (Layout, error) { return Layout{}, ErrUnavailable }
