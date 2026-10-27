//go:build cgo

package abi

/*
#include <stdint.h>
#include <stdbool.h>
#include <stdlib.h>
#define DYLIB_STORAGE_SHAPE(T) (((uint64_t)sizeof(T) << 32) | _Alignof(T))
static uint64_t dylib_storage_shape(uint8_t kind) {
    switch (kind) {
    case 1: return DYLIB_STORAGE_SHAPE(int32_t);
    case 2: return DYLIB_STORAGE_SHAPE(uint32_t);
    case 3: return DYLIB_STORAGE_SHAPE(int64_t);
    case 4: return DYLIB_STORAGE_SHAPE(uint64_t);
    case 5: return DYLIB_STORAGE_SHAPE(float);
    case 6: return DYLIB_STORAGE_SHAPE(double);
    case 7: return DYLIB_STORAGE_SHAPE(void *);
    case 8: return DYLIB_STORAGE_SHAPE(int8_t);
    case 9: return DYLIB_STORAGE_SHAPE(uint8_t);
    case 10: return DYLIB_STORAGE_SHAPE(int16_t);
    case 11: return DYLIB_STORAGE_SHAPE(uint16_t);
    case 12: return DYLIB_STORAGE_SHAPE(bool);
    default: return 0;
    }
}
static void *dylib_storage_alloc(size_t size) { return malloc(size); }
*/
import "C"

import (
	"encoding/binary"
	"fmt"
	"unsafe"
)

// StorageAvailable reports whether host C storage and Default/CDecl layout
// queries are available. Unlike Available, it does not require libffi.
func StorageAvailable() bool { return true }

const maxNativeStorage = 65536

// Scalar alignment comes from the host C compiler, including 32-bit double and
// int64 alignment. Go's scalar/struct alignment is deliberately not used.
var hostScalarLayouts = func() [Bool + 1]Layout {
	var layouts [Bool + 1]Layout
	for kind := I32; kind <= Bool; kind++ {
		shape := uint64(C.dylib_storage_shape(C.uint8_t(kind)))
		layouts[kind] = Layout{Size: shape >> 32, Alignment: shape & 0xffffffff}
	}
	return layouts
}()

type storageNode struct {
	desc     TypeDesc
	storage  Layout
	children []*storageNode // One child for arrays, direct fields for structs.
}

func hostStorage(d TypeDesc) (*storageNode, error) {
	node := &storageNode{desc: d}
	if d.Type != Struct && d.Type != Array {
		if d.Type < I32 || d.Type > Bool {
			return nil, fmt.Errorf("type %d has no native storage", d.Type)
		}
		node.storage = hostScalarLayouts[d.Type]
		if size, align := node.storage.Size, node.storage.Alignment; (size != 1 && size != 2 && size != 4 && size != 8) || align == 0 || align > size || size%align != 0 {
			return nil, fmt.Errorf("unsupported host C scalar layout for type %d", d.Type)
		}
		return node, nil
	}
	node.storage.Alignment = 1
	count := len(d.Fields)
	if d.Type == Array {
		count = 1
	}
	for i := 0; i < count; i++ {
		child, err := hostStorage(d.memberType(i))
		if err != nil {
			return nil, err
		}
		node.children = append(node.children, child)
		alignment := child.storage.Alignment
		if alignment > node.storage.Alignment {
			node.storage.Alignment = alignment
		}
		offset := alignStorage(node.storage.Size, alignment)
		node.storage.Offsets = append(node.storage.Offsets, offset)
		node.storage.Size = offset + child.storage.Size
		if node.storage.Size > maxNativeStorage {
			return nil, fmt.Errorf("native storage exceeds 64 KiB")
		}
	}
	if d.Type == Array {
		size := node.children[0].storage.Size
		if uint64(d.Len) > maxNativeStorage/size {
			return nil, fmt.Errorf("native storage exceeds 64 KiB")
		}
		node.storage.Size = uint64(d.Len) * size
		node.storage.Offsets = make([]uint64, d.Len)
		for i := range node.storage.Offsets {
			node.storage.Offsets[i] = uint64(i) * size
		}
	}
	node.storage.Size = alignStorage(node.storage.Size, node.storage.Alignment)
	if node.storage.Size > maxNativeStorage {
		return nil, fmt.Errorf("native storage exceeds 64 KiB")
	}
	return node, nil
}

func alignStorage(size, alignment uint64) uint64 {
	return (size + alignment - 1) / alignment * alignment
}

func hostStorageLayout(d TypeDesc) (Layout, error) {
	node, err := hostStorage(d)
	if err != nil {
		return Layout{}, err
	}
	return node.storage, nil
}

type cValueBackend struct {
	node   *storageNode
	memory unsafe.Pointer
}

func prepareValue(d TypeDesc, initial Value) (valueBackend, error) {
	node, err := hostStorage(d)
	if err != nil {
		return nil, err
	}
	memory := C.dylib_storage_alloc(C.size_t(node.storage.Size))
	if memory == nil {
		return nil, fmt.Errorf("native value allocation failed")
	}
	b := &cValueBackend{node: node, memory: memory}
	if err := b.write(initial); err != nil {
		b.close()
		return nil, err
	}
	return b, nil
}

func (b *cValueBackend) pointer() unsafe.Pointer { return b.memory }
func (b *cValueBackend) layout() Layout          { return b.node.storage }
func (b *cValueBackend) read() Value {
	return b.node.read(unsafe.Slice((*byte)(b.memory), int(b.node.storage.Size)))
}
func (b *cValueBackend) write(value Value) error {
	bytes := unsafe.Slice((*byte)(b.memory), int(b.node.storage.Size))
	clear(bytes)
	b.node.write(bytes, value)
	return nil
}
func (b *cValueBackend) close() {
	C.free(b.memory)
	b.node, b.memory = nil, nil
}

func (n *storageNode) child(i int) *storageNode {
	if n.desc.Type == Array {
		return n.children[0]
	}
	return n.children[i]
}

func (n *storageNode) read(bytes []byte) Value {
	value := Value{Type: n.desc.Type}
	if n.desc.Type == Struct || n.desc.Type == Array {
		value.Aggregate = &Aggregate{Type: n.desc.Clone(), Fields: make([]Value, len(n.storage.Offsets))}
		for i, offset := range n.storage.Offsets {
			child := n.child(i)
			value.Aggregate.Fields[i] = child.read(bytes[offset : offset+child.storage.Size])
		}
		return value
	}
	switch n.storage.Size {
	case 1:
		value.Bits = uint64(bytes[0])
	case 2:
		value.Bits = uint64(binary.NativeEndian.Uint16(bytes))
	case 4:
		value.Bits = uint64(binary.NativeEndian.Uint32(bytes))
	case 8:
		value.Bits = binary.NativeEndian.Uint64(bytes)
	}
	return value
}

func (n *storageNode) write(bytes []byte, value Value) {
	if n.desc.Type == Struct || n.desc.Type == Array {
		for i, offset := range n.storage.Offsets {
			child := n.child(i)
			child.write(bytes[offset:offset+child.storage.Size], value.Aggregate.Fields[i])
		}
		return
	}
	switch n.storage.Size {
	case 1:
		bytes[0] = byte(value.Bits)
	case 2:
		binary.NativeEndian.PutUint16(bytes, uint16(value.Bits))
	case 4:
		binary.NativeEndian.PutUint32(bytes, uint32(value.Bits))
	case 8:
		binary.NativeEndian.PutUint64(bytes, value.Bits)
	}
}
