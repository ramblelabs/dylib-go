//go:build cgo

// Package storageprobe independently checks storage through native C lvalues.
// It is used only by ABI tests.
package storageprobe

/*
#include <stdint.h>
#include <stdbool.h>
#include <stddef.h>
#include <string.h>
typedef struct { int8_t tag; double value; bool truth; } storage_inner;
typedef struct {
    uint16_t head;
    storage_inner items[2];
    int16_t matrix[2][3];
    void *pointer;
    uint64_t tail;
} storage_record;
static uint64_t storage_record_size(void) { return sizeof(storage_record); }
static uint64_t storage_record_alignment(void) { return _Alignof(storage_record); }
static uint64_t storage_record_offset(int i) {
    switch (i) {
    case 0: return offsetof(storage_record,head);
    case 1: return offsetof(storage_record,items);
    case 2: return offsetof(storage_record,matrix);
    case 3: return offsetof(storage_record,pointer);
    default: return offsetof(storage_record,tail);
    }
}
static int storage_record_check(void *p) {
    storage_record *r = (storage_record *)p;
    return r->head == 65535 && r->items[0].tag == -128 &&
        r->items[0].value == 20.5 && r->items[0].truth &&
        r->items[1].tag == 127 && r->items[1].value == -22.5 && !r->items[1].truth &&
        r->matrix[0][0] == -32768 && r->matrix[0][1] == -1 && r->matrix[0][2] == 0 &&
        r->matrix[1][0] == 1 && r->matrix[1][1] == 2 && r->matrix[1][2] == 32767 &&
        r->pointer == &r->items[1] && r->tail == UINT64_C(0xfedcba9876543210);
}
static void storage_record_update(void *p) {
    storage_record *r = (storage_record *)p;
    r->head = 42; r->items[0].value = 42.5; r->items[1].truth = true;
    r->matrix[1][2] = -42; r->pointer = NULL; r->tail = 42;
}
static int storage_record_padding(void *p, int poison) {
    unsigned char used[sizeof(storage_record)] = {0};
    #define MARK(offset,size) memset(used+(offset),1,(size))
    MARK(offsetof(storage_record,head),sizeof(uint16_t));
    for (size_t i=0;i<2;i++) {
        size_t base=offsetof(storage_record,items)+i*sizeof(storage_inner);
        MARK(base+offsetof(storage_inner,tag),sizeof(int8_t));
        MARK(base+offsetof(storage_inner,value),sizeof(double));
        MARK(base+offsetof(storage_inner,truth),sizeof(bool));
    }
    MARK(offsetof(storage_record,matrix),sizeof(((storage_record *)0)->matrix));
    MARK(offsetof(storage_record,pointer),sizeof(void *));
    MARK(offsetof(storage_record,tail),sizeof(uint64_t));
    #undef MARK
    unsigned char *bytes=(unsigned char *)p;
    for (size_t i=0;i<sizeof(storage_record);i++) {
        if (used[i]) continue;
        if (poison) bytes[i]=0xa5;
        else if (bytes[i]) return 0;
    }
    return 1;
}
static int storage_scalar_check(uint8_t kind, void *p) {
    switch (kind) {
    case 1: return *(int32_t *)p == INT32_MIN;
    case 2: return *(uint32_t *)p == UINT32_MAX;
    case 3: return *(int64_t *)p == INT64_MIN;
    case 4: return *(uint64_t *)p == UINT64_MAX;
    case 5: return *(float *)p == -20.5f;
    case 6: return *(double *)p == 22.5;
    case 7: return *(void **)p == NULL;
    case 8: return *(int8_t *)p == INT8_MIN;
    case 9: return *(uint8_t *)p == UINT8_MAX;
    case 10: return *(int16_t *)p == INT16_MIN;
    case 11: return *(uint16_t *)p == UINT16_MAX;
    case 12: return *(bool *)p;
    default: return 0;
    }
}
*/
import "C"

import "unsafe"

func Layout() (size, alignment uint64, offsets []uint64) {
	for i := 0; i < 5; i++ {
		offsets = append(offsets, uint64(C.storage_record_offset(C.int(i))))
	}
	return uint64(C.storage_record_size()), uint64(C.storage_record_alignment()), offsets
}
func Check(pointer unsafe.Pointer) bool { return C.storage_record_check(pointer) != 0 }
func Update(pointer unsafe.Pointer)     { C.storage_record_update(pointer) }
func PaddingZero(pointer unsafe.Pointer) bool {
	return C.storage_record_padding(pointer, 0) != 0
}
func PoisonPadding(pointer unsafe.Pointer) { C.storage_record_padding(pointer, 1) }
func CheckScalar(kind uint8, pointer unsafe.Pointer) bool {
	return C.storage_scalar_check(C.uint8_t(kind), pointer) != 0
}
