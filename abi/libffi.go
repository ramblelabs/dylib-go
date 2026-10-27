//go:build libffi && cgo

package abi

/*
#cgo pkg-config: libffi
#include <ffi.h>
#include <stdint.h>
#include <string.h>
#include <stdlib.h>
typedef union { ffi_arg word; int32_t i32; uint32_t u32; int64_t i64;
    uint64_t u64; int8_t i8; uint8_t u8; int16_t i16; uint16_t u16; float f32; double f64; void *ptr; } dylib_scalar;
typedef struct { ffi_cif cif; ffi_type *types[]; } dylib_call_plan;
static size_t dylib_array_size(size_t header, size_t n, size_t element) {
    if (n > (SIZE_MAX-header)/element) return 0;
    return header+n*element;
}
static int dylib_abi(uint8_t convention) {
    if (convention <= 1) return FFI_DEFAULT_ABI;
#if defined(_WIN32) && (defined(__i386__) || defined(_M_IX86))
    if (convention == 2) return FFI_STDCALL;
    if (convention == 3) return FFI_FASTCALL;
    if (convention == 5) return FFI_THISCALL;
#endif
#if defined(__x86_64__) || defined(_M_X64)
    if (convention == 6) return FFI_WIN64;
#if !defined(_WIN32)
    if (convention == 7) return FFI_UNIX64;
#endif
#endif
    return -1;
}
static dylib_call_plan *dylib_plan_new(unsigned n) {
    size_t size=dylib_array_size(sizeof(dylib_call_plan),n,sizeof(ffi_type *));
    if (!size) return NULL;
    return (dylib_call_plan *)calloc(1,size);
}
static void dylib_plan_arg(dylib_call_plan *p, unsigned i, ffi_type *type) { p->types[i]=type; }
static int dylib_plan_prepare(dylib_call_plan *p, int abi, unsigned n, unsigned fixed, int variadic, ffi_type *result) {
    if (variadic) return ffi_prep_cif_var(&p->cif,(ffi_abi)abi,fixed,n,result,p->types);
    return ffi_prep_cif(&p->cif,(ffi_abi)abi,n,result,p->types);
}
static ffi_type *dylib_ffi_type(uint8_t t) {
    switch(t) {
    case 0: return &ffi_type_void; case 1: return &ffi_type_sint32;
    case 2: return &ffi_type_uint32; case 3: return &ffi_type_sint64;
    case 4: return &ffi_type_uint64; case 5: return &ffi_type_float;
    case 6: return &ffi_type_double; case 7: return &ffi_type_pointer;
    case 8: return &ffi_type_sint8; case 9: return &ffi_type_uint8;
    case 10: return &ffi_type_sint16; case 11: return &ffi_type_uint16;
    case 12: return &ffi_type_uint8;
    default: return NULL;
    }
}
typedef struct {
    dylib_scalar ret;
    unsigned n;
    int status;
    dylib_scalar *storage;
    uint64_t *bits;
    void **args;
} dylib_scalar_call;
static size_t dylib_scalar_size(unsigned n) {
    return dylib_array_size(sizeof(dylib_scalar_call),n,sizeof(dylib_scalar)+sizeof(uint64_t)+sizeof(void *));
}
static dylib_scalar_call *dylib_scalar_new(unsigned n) {
    size_t size=dylib_scalar_size(n);
    if (!size) return NULL;
    dylib_scalar_call *c=(dylib_scalar_call *)calloc(1,size);
    if (c) {
        c->n=n;
        c->storage=(dylib_scalar *)(c+1);
        c->bits=(uint64_t *)(c->storage+n);
        // Keep 64-bit values before pointers: pointer alignment can be smaller.
        c->args=(void **)(c->bits+n);
    }
    return c;
}
static uint64_t dylib_scalar_execute(dylib_call_plan *plan, dylib_scalar_call *c,
    uintptr_t address, uint8_t result) {
    c->status=-1;
    if (c->n != plan->cif.nargs) return 0;
    dylib_scalar *storage=c->storage; void **args=c->args;
    const uint64_t *bits=c->bits;
    for (unsigned i=0;i<c->n;i++) {
        // Rebuild the mutable libffi vector on every invocation.
        args[i]=&storage[i];
        // Prepared CIF types are immutable and already validated.
        switch(plan->cif.arg_types[i]->type) {
        case FFI_TYPE_SINT32: storage[i].i32=(int32_t)bits[i]; break;
        case FFI_TYPE_UINT32: storage[i].u32=(uint32_t)bits[i]; break;
        case FFI_TYPE_SINT64: storage[i].i64=(int64_t)bits[i]; break;
        case FFI_TYPE_UINT64: storage[i].u64=bits[i]; break;
        case FFI_TYPE_FLOAT: { uint32_t b=(uint32_t)bits[i]; memcpy(&storage[i].f32,&b,4); break; }
        case FFI_TYPE_DOUBLE: memcpy(&storage[i].f64,&bits[i],8); break;
        case FFI_TYPE_POINTER: storage[i].ptr=(void *)(uintptr_t)bits[i]; break;
        case FFI_TYPE_SINT8: storage[i].i8=(int8_t)bits[i]; break;
        case FFI_TYPE_UINT8: storage[i].u8=(uint8_t)bits[i]; break;
        case FFI_TYPE_SINT16: storage[i].i16=(int16_t)bits[i]; break;
        case FFI_TYPE_UINT16: storage[i].u16=(uint16_t)bits[i]; break;
        default: return 0;
        }
    }
    memset(&c->ret,0,sizeof(c->ret));
    ffi_call(&plan->cif,FFI_FN(address),&c->ret,args);
    uint64_t out=0;
    switch(result) {
    case 0: break;
    case 1: case 2: out=(uint32_t)c->ret.word; break;
    case 3: case 4: out=c->ret.u64; break;
    case 5: { uint32_t b; memcpy(&b,&c->ret.f32,4); out=b; break; }
    case 6: memcpy(&out,&c->ret.f64,8); break;
    case 7: out=(uintptr_t)c->ret.ptr; break;
    case 8: case 9: case 12: out=(uint8_t)c->ret.word; break;
    case 10: case 11: out=(uint16_t)c->ret.word; break;
    default: return 0;
    }
    c->status=0;
    return out;
}
typedef struct {
    ffi_type type;
    size_t *offsets;
    ffi_type *elements[];
} dylib_struct_type;
static dylib_struct_type *dylib_struct_new(unsigned n) {
    dylib_struct_type *t=(dylib_struct_type *)calloc(1,sizeof(*t)+(n+1)*sizeof(ffi_type *)+n*sizeof(size_t));
    if (t) {
        t->type.type=FFI_TYPE_STRUCT; t->type.elements=t->elements;
        t->offsets=(size_t *)(t->elements+n+1);
    }
    return t;
}
static void dylib_struct_field(dylib_struct_type *t, unsigned i, ffi_type *f) { t->elements[i]=f; }
static int dylib_struct_layout(dylib_struct_type *t, int abi) { return ffi_get_struct_offsets((ffi_abi)abi,&t->type,t->offsets); }
static ffi_type *dylib_struct_ffi(dylib_struct_type *t) { return &t->type; }
static size_t dylib_struct_offset(dylib_struct_type *t, unsigned i) { return t->offsets[i]; }
static size_t dylib_type_size(ffi_type *t) { return t->size; }
static unsigned short dylib_type_alignment(ffi_type *t) { return t->alignment; }
static int dylib_scalar_layout(ffi_type *t, int abi) {
    ffi_cif cif;
    return ffi_prep_cif(&cif,(ffi_abi)abi,0,t,NULL);
}
static void *dylib_field_address(void *p, size_t offset) { return (char *)p+offset; }
static void dylib_store(void *p, uint8_t t, uint64_t bits) {
    switch(t) {
    case 1: { int32_t v=(int32_t)bits; memcpy(p,&v,4); break; }
    case 2: { uint32_t v=(uint32_t)bits; memcpy(p,&v,4); break; }
    case 3: case 4: case 6: memcpy(p,&bits,8); break;
    case 5: { uint32_t v=(uint32_t)bits; memcpy(p,&v,4); break; }
    case 7: { void *v=(void *)(uintptr_t)bits; memcpy(p,&v,sizeof(v)); break; }
    case 8: case 9: case 12: { uint8_t v=(uint8_t)bits; memcpy(p,&v,1); break; }
    case 10: case 11: { uint16_t v=(uint16_t)bits; memcpy(p,&v,2); break; }
    }
}
static uint64_t dylib_read(void *p, uint8_t t, int is_result) {
    uint64_t bits=0;
    if (is_result && ((t>=1 && t<=2) || (t>=8 && t<=12))) {
        ffi_arg word; memcpy(&word,p,sizeof(word));
        if (t==8 || t==9 || t==12) return (uint8_t)word;
        if (t==10 || t==11) return (uint16_t)word;
        return (uint32_t)word;
    }
    switch(t) {
    case 1: case 2: case 5: { uint32_t v; memcpy(&v,p,4); bits=v; break; }
    case 3: case 4: case 6: memcpy(&bits,p,8); break;
    case 7: { void *v; memcpy(&v,p,sizeof(v)); bits=(uintptr_t)v; break; }
    case 8: case 9: case 12: { uint8_t v; memcpy(&v,p,1); bits=v; break; }
    case 10: case 11: { uint16_t v; memcpy(&v,p,2); bits=v; break; }
    }
    return bits;
}
typedef struct {
    unsigned n;
    void *args[];
} dylib_record_call;
static size_t dylib_record_size(unsigned n) {
    return dylib_array_size(sizeof(dylib_record_call),n,2*sizeof(void *));
}
static dylib_record_call *dylib_record_new(unsigned n) {
    size_t size=dylib_record_size(n);
    if (!size) return NULL;
    dylib_record_call *c=(dylib_record_call *)calloc(1,size);
    if (c) c->n=n;
    return c;
}
static void dylib_record_arg(dylib_record_call *c, unsigned i, void *p) { c->args[i]=p; }
static void dylib_record_execute(dylib_call_plan *plan, dylib_record_call *c, uintptr_t address, void *out) {
    // libffi may replace argument pointers with its own temporary struct
    // copies. Rebuild a mutable vector for every invocation, retaining roots.
    void **values=c->args+c->n;
    memcpy(values,c->args,c->n*sizeof(void *));
    ffi_call(&plan->cif,FFI_FN(address),out,values);
}

extern void dylibgo_dispatch_callback(uintptr_t handle, void *result, void *arguments);
typedef struct {
    void *writable;
    void *code;
    uintptr_t handle;
} dylib_callback;
static int dylib_register_result(ffi_type *type) {
    switch (type->type) {
    case FFI_TYPE_INT: case FFI_TYPE_SINT8: case FFI_TYPE_UINT8:
    case FFI_TYPE_SINT16: case FFI_TYPE_UINT16:
    case FFI_TYPE_SINT32: case FFI_TYPE_UINT32: return 1;
    }
    return 0;
}
static void dylib_closure_dispatch(ffi_cif *cif, void *out, void **args, void *user) {
    dylib_callback *callback = (dylib_callback *)user;
    if (cif->rtype->type != FFI_TYPE_VOID) {
        size_t size = cif->rtype->size;
        if (dylib_register_result(cif->rtype) && size < sizeof(ffi_arg)) size = sizeof(ffi_arg);
        memset(out, 0, size);
    }
    dylibgo_dispatch_callback(callback->handle, out, args);
}
static dylib_callback *dylib_callback_new(dylib_call_plan *plan, uintptr_t handle, int *status) {
    *status = -1;
#if FFI_CLOSURES
    dylib_callback *callback = (dylib_callback *)calloc(1,sizeof(*callback));
    if (!callback) return NULL;
    callback->writable = (ffi_closure *)ffi_closure_alloc(sizeof(ffi_closure), &callback->code);
    if (!callback->writable) { free(callback); return NULL; }
    callback->handle = handle;
    *status = ffi_prep_closure_loc(callback->writable, &plan->cif,
                                  dylib_closure_dispatch, callback, callback->code);
    if (*status != FFI_OK) { ffi_closure_free(callback->writable); free(callback); return NULL; }
    return callback;
#else
    return NULL;
#endif
}
static void *dylib_callback_code(dylib_callback *callback) { return callback->code; }
static void dylib_callback_free(dylib_callback *callback) {
#if FFI_CLOSURES
    ffi_closure_free(callback->writable);
#endif
    free(callback);
}
static void dylib_callback_store(void *out, uint8_t type, uint64_t bits) {
    switch (type) {
    case 1: { ffi_sarg v=(int32_t)bits; memcpy(out,&v,sizeof(v)); return; }
    case 8: { ffi_sarg v=(int8_t)bits; memcpy(out,&v,sizeof(v)); return; }
    case 10: { ffi_sarg v=(int16_t)bits; memcpy(out,&v,sizeof(v)); return; }
    case 2: { ffi_arg v=(uint32_t)bits; memcpy(out,&v,sizeof(v)); return; }
    case 9: case 12: { ffi_arg v=(uint8_t)bits; memcpy(out,&v,sizeof(v)); return; }
    case 11: { ffi_arg v=(uint16_t)bits; memcpy(out,&v,sizeof(v)); return; }
    }
    dylib_store(out,type,bits);
}
*/
import "C"

import (
	"fmt"
	"reflect"
	"runtime/cgo"
	"sync"
	"unsafe"
)

func Available() bool { return true }

type ffiBackend struct {
	layoutMu    sync.Mutex // Protect lazy owned layouts, never native execution.
	plan        *C.dylib_call_plan
	pool        nativePool
	result      *nativeType
	args        []*nativeType
	recordMu    sync.Mutex // Protect idle buffers, never native execution.
	records     []*recordBuffer
	recordBytes uint64
	scalarMu    sync.Mutex // Protect idle buffers, never native execution.
	scalars     []*scalarBuffer
	scalarBytes uint64
}

// Explicit ownership is needed for native allocations: a sync.Pool could drop
// them during GC without freeing them. Keep a bounded, plan-owned idle cache.
const maxIdleRecordBuffers = 4
const maxIdleRecordBytes = 1 << 20
const maxIdleScalarBuffers = 4
const maxIdleScalarBytes = 1 << 20
const maxIdlePointeeBuffers = 32
const maxIdlePointeeBytes = 256 << 10

// Each active record context owns its value cache. Only cleared native bytes
// survive a call; logical values, aliases and ephemeral layouts never do.
type nativeValueBuffer struct {
	memory unsafe.Pointer
	size   uint64
}

func (v *nativeValueBuffer) close() {
	C.free(v.memory)
	v.memory = nil
}

type nativeValueCache struct {
	idle  []*nativeValueBuffer
	bytes uint64
}

func (c *nativeValueCache) acquire(size uint64) (*nativeValueBuffer, error) {
	best := -1
	for i, value := range c.idle {
		if value.size >= size && (best == -1 || value.size < c.idle[best].size) {
			best = i
		}
	}
	if best != -1 {
		value := c.idle[best]
		last := len(c.idle) - 1
		c.idle[best], c.idle[last] = c.idle[last], nil
		c.idle = c.idle[:last]
		c.bytes -= value.size
		return value, nil
	}
	mem := C.calloc(1, C.size_t(size))
	if mem == nil {
		return nil, fmt.Errorf("native allocation failed")
	}
	return &nativeValueBuffer{memory: mem, size: size}, nil
}

func (c *nativeValueCache) release(value *nativeValueBuffer) {
	clear(unsafe.Slice((*byte)(value.memory), int(value.size)))
	if len(c.idle) < maxIdlePointeeBuffers && value.size <= maxIdlePointeeBytes-c.bytes {
		c.idle = append(c.idle, value)
		c.bytes += value.size
	} else {
		value.close()
	}
}

func (c *nativeValueCache) close() {
	for _, value := range c.idle {
		value.close()
	}
	c.idle, c.bytes = nil, 0
}

type scalarBuffer struct {
	call    *C.dylib_scalar_call
	bits    []C.uint64_t
	payload []byte // Native storage, value bits and mutable argument addresses.
	size    uint64
}

func (b *ffiBackend) acquireScalars() (*scalarBuffer, error) {
	b.scalarMu.Lock()
	var work *scalarBuffer
	if n := len(b.scalars); n != 0 {
		work = b.scalars[n-1]
		b.scalars[n-1] = nil
		b.scalars = b.scalars[:n-1]
		b.scalarBytes -= work.size
	}
	b.scalarMu.Unlock()
	if work != nil {
		return work, nil
	}
	size := uint64(C.dylib_scalar_size(C.uint(len(b.args))))
	if size == 0 || size > uint64(^uint(0)>>1) {
		return nil, fmt.Errorf("native scalar call allocation failed")
	}
	call := C.dylib_scalar_new(C.uint(len(b.args)))
	if call == nil {
		return nil, fmt.Errorf("native scalar call allocation failed")
	}
	return &scalarBuffer{
		call: call, bits: unsafe.Slice(call.bits, len(b.args)), size: size,
		payload: unsafe.Slice((*byte)(unsafe.Add(unsafe.Pointer(call), C.sizeof_dylib_scalar_call)), int(size)-C.sizeof_dylib_scalar_call),
	}, nil
}

func (b *ffiBackend) releaseScalars(work *scalarBuffer) {
	// No Go pointers enter these native buffers. Clear foreign values/addresses
	// before caching, including after errors, without another cgo transition.
	clear(work.payload)
	clear(unsafe.Slice((*byte)(unsafe.Pointer(&work.call.ret)), C.sizeof_dylib_scalar))
	work.call.status = 0
	b.scalarMu.Lock()
	keep := len(b.scalars) < maxIdleScalarBuffers && work.size <= maxIdleScalarBytes-b.scalarBytes
	if keep {
		b.scalars = append(b.scalars, work)
		b.scalarBytes += work.size
	}
	b.scalarMu.Unlock()
	if !keep {
		work.close()
	}
}

func (w *scalarBuffer) close() {
	C.free(unsafe.Pointer(w.call))
	w.call, w.bits, w.payload = nil, nil, nil
}

type recordBuffer struct {
	call      *C.dylib_record_call
	args      []unsafe.Pointer
	out       unsafe.Pointer
	roots     nativePool // Fixed argument/result storage; lives until eviction/Close.
	pool      nativePool // Per-call types, pointees, aliases and lifetime checks.
	pointees  nativeValueCache
	fixedSize uint64
	size      uint64 // Fixed storage plus idle pointees; counts toward plan budget.
}

func (b *ffiBackend) acquireRecords() (*recordBuffer, error) {
	b.recordMu.Lock()
	var work *recordBuffer
	if n := len(b.records); n != 0 {
		work = b.records[n-1]
		b.records[n-1] = nil
		b.records = b.records[:n-1]
		b.recordBytes -= work.size
	}
	b.recordMu.Unlock()
	if work == nil {
		work = &recordBuffer{pool: nativePool{abi: b.pool.abi, types: b, pointers: make(map[*Value]nativeCopy), owners: make(map[uint64]*Value)}}
		work.pool.valueCache = &work.pointees
		work.call = C.dylib_record_new(C.uint(len(b.args)))
		if work.call == nil {
			return nil, fmt.Errorf("native call allocation failed")
		}
		work.size = uint64(C.dylib_record_size(C.uint(len(b.args))))
		work.args = make([]unsafe.Pointer, len(b.args))
		for i, t := range b.args {
			mem, err := work.roots.allocate(uint64(C.dylib_type_size(t.ffi)))
			if err != nil {
				work.close()
				return nil, err
			}
			work.args[i] = mem
			C.dylib_record_arg(work.call, C.uint(i), mem)
		}
		var err error
		work.out, err = work.roots.allocate(uint64(C.dylib_type_size(b.result.ffi)))
		if err != nil {
			work.close()
			return nil, err
		}
		for _, buffer := range work.roots.buffers {
			work.size += buffer.size
		}
		work.fixedSize = work.size
	}
	// Native code may inspect struct padding; restore calloc's zeroing guarantee.
	for i, mem := range work.roots.allocations {
		C.memset(mem, 0, C.size_t(work.roots.buffers[i].size))
	}
	work.pool.buffers = append(work.pool.buffers, work.roots.buffers...)
	return work, nil
}

func (b *ffiBackend) releaseRecords(work *recordBuffer) {
	// Copy-back has completed. Free ephemeral types, drop every Go owner and
	// return cleared pointees to this context, including on marshaling/read errors.
	work.pool.reset()
	work.size = work.fixedSize + work.pointees.bytes
	b.recordMu.Lock()
	keep := len(b.records) < maxIdleRecordBuffers && work.size <= maxIdleRecordBytes-b.recordBytes
	if keep {
		b.records = append(b.records, work)
		b.recordBytes += work.size
	}
	b.recordMu.Unlock()
	if !keep {
		work.close()
	}
}

func (w *recordBuffer) close() {
	w.pool.close()
	w.pointees.close()
	w.roots.close()
	C.free(unsafe.Pointer(w.call))
	w.call, w.args, w.out = nil, nil, nil
}

// Only signature-declared pointees use this persistent cache. Opaque pointers
// can carry arbitrary temporary shapes, which stay in the invocation pool.
// Keep preparation lazy: a large native pointee remains usable as a raw pointer
// even when copying its value would exceed the temporary marshaling budget.
func (b *ffiBackend) pointeeLayout(d TypeDesc) (*nativeType, error) {
	b.layoutMu.Lock()
	defer b.layoutMu.Unlock()
	allocations, layouts := len(b.pool.allocations), len(b.pool.layouts)
	n, err := b.pool.build(d)
	if err != nil {
		for i := len(b.pool.allocations) - 1; i >= allocations; i-- {
			C.free(b.pool.allocations[i])
		}
		clear(b.pool.allocations[allocations:])
		clear(b.pool.layouts[layouts:])
		b.pool.allocations = b.pool.allocations[:allocations]
		b.pool.layouts = b.pool.layouts[:layouts]
	}
	return n, err
}

func prepare(s Signature) (callBackend, error) {
	convention := C.dylib_abi(C.uint8_t(s.Convention))
	if convention < 0 {
		return nil, fmt.Errorf("calling convention %d is unavailable on this host", s.Convention)
	}
	// libffi uses GNU thiscall result placement; MSVC keeps the receiver
	// in ECX instead. A matching producer header must describe struct results.
	if s.Convention == ThisCall && s.Result == Struct {
		return nil, fmt.Errorf("thiscall struct results require a compiled C bridge")
	}
	b := &ffiBackend{pool: nativePool{abi: convention}, plan: C.dylib_plan_new(C.uint(len(s.Args)))}
	if b.plan == nil {
		return nil, fmt.Errorf("native call plan allocation failed")
	}
	ok := false
	defer func() {
		if !ok {
			b.close()
		}
	}()
	var err error
	b.result, err = b.pool.build(s.ReturnType())
	if err != nil {
		return nil, err
	}
	for i := range s.Args {
		t, err := b.pool.build(s.ArgumentType(i))
		if err != nil {
			return nil, err
		}
		b.args = append(b.args, t)
		C.dylib_plan_arg(b.plan, C.uint(i), t.ffi)
	}
	variadic := C.int(0)
	if s.Variadic {
		variadic = 1
	}
	if rc := C.dylib_plan_prepare(b.plan, convention, C.uint(len(s.Args)), C.uint(s.FixedArgs), variadic, b.result.ffi); rc != 0 {
		return nil, fmt.Errorf("ffi call preparation failed: %d", rc)
	}
	ok = true
	return b, nil
}

func (b *ffiBackend) close() {
	b.scalarMu.Lock()
	scalars := b.scalars
	b.scalars, b.scalarBytes = nil, 0
	b.scalarMu.Unlock()
	for _, work := range scalars {
		work.close()
	}
	b.recordMu.Lock()
	works := b.records
	b.records, b.recordBytes = nil, 0
	b.recordMu.Unlock()
	for _, work := range works {
		work.close()
	}
	C.free(unsafe.Pointer(b.plan))
	b.pool.close()
}

func (b *ffiBackend) invoke(address uintptr, s Signature, args []Value) (Value, error) {
	records := s.Result == Struct
	for _, v := range args {
		records = records || v.Type == Struct || v.Pointee != nil
	}
	if records {
		return b.invokeRecords(address, s, args)
	}
	work, err := b.acquireScalars()
	if err != nil {
		return Value{}, err
	}
	defer b.releaseScalars(work)
	for i, v := range args {
		work.bits[i] = C.uint64_t(v.Bits)
	}
	out := C.dylib_scalar_execute(b.plan, work.call, C.uintptr_t(address), C.uint8_t(s.Result))
	if work.call.status != 0 {
		return Value{}, fmt.Errorf("ffi scalar invocation failed: %d", work.call.status)
	}
	return Value{Type: s.Result, Bits: uint64(out)}, nil
}

type nativeType struct {
	ffi     *C.ffi_type
	fields  []*nativeType
	offsets []C.size_t
}

func layoutOf(d TypeDesc, convention Convention) (Layout, error) {
	nativeABI := C.dylib_abi(C.uint8_t(convention))
	if nativeABI < 0 {
		return Layout{}, fmt.Errorf("calling convention %d is unavailable on this host", convention)
	}
	pool := nativePool{abi: nativeABI}
	defer pool.close()
	native, err := pool.build(d)
	if err != nil {
		return Layout{}, err
	}
	return nativeStorageLayout(native, d.Type, nativeABI)
}

func nativeStorageLayout(native *nativeType, kind Type, nativeABI C.int) (Layout, error) {
	// Aggregates were initialized by ffi_get_struct_offsets in build. Prepare
	// scalar types before reading metadata, as required by libffi's ABI contract.
	if kind != Struct && kind != Array {
		if rc := C.dylib_scalar_layout(native.ffi, nativeABI); rc != 0 {
			return Layout{}, fmt.Errorf("ffi scalar layout failed: %d", rc)
		}
	}
	layout := Layout{Size: uint64(C.dylib_type_size(native.ffi)), Alignment: uint64(C.dylib_type_alignment(native.ffi))}
	for _, offset := range native.offsets {
		layout.Offsets = append(layout.Offsets, uint64(offset))
	}
	return layout, nil
}

type nativeCopy struct {
	value  *Value
	desc   TypeDesc
	native *nativeType
	memory unsafe.Pointer
}
type nativePool struct {
	types       *ffiBackend
	layouts     []nativeLayout
	abi         C.int
	allocations []unsafe.Pointer
	buffers     []nativeBuffer
	pointers    map[*Value]nativeCopy
	owners      map[uint64]*Value
	err         error
	valueCache  *nativeValueCache // Only per-call record pools reuse native values.
	values      []*nativeValueBuffer
}
type nativeLayout struct {
	desc   TypeDesc
	native *nativeType
}
type nativeBuffer struct{ base, size uint64 }

func (p *nativePool) freeAllocations() {
	for i := len(p.allocations) - 1; i >= 0; i-- {
		C.free(p.allocations[i])
	}
	clear(p.allocations)
	p.allocations = p.allocations[:0]
}
func (p *nativePool) close() {
	p.freeAllocations()
	for _, value := range p.values {
		value.close()
	}
	clear(p.values)
	p.values = p.values[:0]
}
func (p *nativePool) reset() {
	p.freeAllocations()
	clear(p.layouts)
	p.layouts = p.layouts[:0]
	clear(p.buffers)
	p.buffers = p.buffers[:0]
	clear(p.pointers)
	clear(p.owners)
	p.err = nil
	for _, value := range p.values {
		p.valueCache.release(value)
	}
	clear(p.values)
	p.values = p.values[:0]
}
func (p *nativePool) allocate(size uint64) (unsafe.Pointer, error) {
	if size > 65536 {
		return nil, fmt.Errorf("native value exceeds 64 KiB")
	}
	if size < 8 {
		size = 8
	}
	if p.valueCache != nil {
		value, err := p.valueCache.acquire(size)
		if err != nil {
			return nil, err
		}
		p.values = append(p.values, value)
		// Include the entire capacity, also when a smaller shape reuses it.
		p.buffers = append(p.buffers, nativeBuffer{uint64(uintptr(value.memory)), value.size})
		return value.memory, nil
	}
	mem := C.calloc(1, C.size_t(size))
	if mem == nil {
		return nil, fmt.Errorf("native allocation failed")
	}
	p.allocations = append(p.allocations, mem)
	p.buffers = append(p.buffers, nativeBuffer{uint64(uintptr(mem)), size})
	return mem, nil
}
func (p *nativePool) build(d TypeDesc) (*nativeType, error) {
	if d.Type != Struct && d.Type != Array {
		return &nativeType{ffi: C.dylib_ffi_type(C.uint8_t(d.Type))}, nil
	}
	for _, layout := range p.layouts {
		if reflect.DeepEqual(layout.desc, d) {
			return layout.native, nil
		}
	}
	t := C.dylib_struct_new(C.uint(d.memberCount()))
	if t == nil {
		return nil, fmt.Errorf("native type allocation failed")
	}
	p.allocations = append(p.allocations, unsafe.Pointer(t))
	n := &nativeType{ffi: C.dylib_struct_ffi(t)}
	var child *nativeType
	for i := 0; i < d.memberCount(); i++ {
		// libffi represents an array member as a struct with repeated elements.
		// Reuse its element layout; array arguments/results are rejected earlier.
		if d.Type != Array || i == 0 {
			var err error
			child, err = p.build(d.memberType(i))
			if err != nil {
				return nil, err
			}
		}
		n.fields = append(n.fields, child)
		C.dylib_struct_field(t, C.uint(i), child.ffi)
	}
	if rc := C.dylib_struct_layout(t, p.abi); rc != 0 {
		return nil, fmt.Errorf("ffi struct layout failed: %d", rc)
	}
	if uint64(C.dylib_type_size(n.ffi)) > 65536 {
		return nil, fmt.Errorf("native aggregate exceeds 64 KiB")
	}
	for i := 0; i < d.memberCount(); i++ {
		n.offsets = append(n.offsets, C.dylib_struct_offset(t, C.uint(i)))
	}
	p.layouts = append(p.layouts, nativeLayout{d, n})
	return n, nil
}
func (p *nativePool) write(v Value, d TypeDesc, n *nativeType, mem unsafe.Pointer) error {
	if d.Type == Struct || d.Type == Array {
		for i, member := range v.Aggregate.Fields {
			if err := p.write(member, d.memberType(i), n.fields[i], C.dylib_field_address(mem, n.offsets[i])); err != nil {
				return err
			}
		}
		return nil
	}
	bits := v.Bits
	if v.Pointee != nil {
		copy, ok := p.pointers[v.Pointee]
		if !ok {
			e := v.Pointee.Description()
			if d.Elem != nil {
				e = *d.Elem
			}
			var t *nativeType
			var err error
			if d.Elem != nil && p.types != nil {
				t, err = p.types.pointeeLayout(e)
			} else {
				t, err = p.build(e)
			}
			if err != nil {
				return err
			}
			data, err := p.allocate(uint64(C.dylib_type_size(t.ffi)))
			if err != nil {
				return err
			}
			copy = nativeCopy{value: v.Pointee, desc: e, native: t, memory: data}
			p.pointers[v.Pointee] = copy
			p.owners[uint64(uintptr(data))] = v.Pointee
			if err := p.write(*v.Pointee, e, t, data); err != nil {
				return err
			}
		}
		bits = uint64(uintptr(copy.memory))
	}
	C.dylib_store(mem, C.uint8_t(d.Type), C.uint64_t(bits))
	return nil
}
func (p *nativePool) read(d TypeDesc, n *nativeType, mem unsafe.Pointer, result bool) Value {
	v := Value{Type: d.Type}
	if d.Type == Struct || d.Type == Array {
		v.Aggregate = &Aggregate{Type: d.Clone()}
		for i := 0; i < d.memberCount(); i++ {
			v.Aggregate.Fields = append(v.Aggregate.Fields, p.read(d.memberType(i), n.fields[i], C.dylib_field_address(mem, n.offsets[i]), false))
		}
	} else {
		isResult := C.int(0)
		if result {
			isResult = 1
		}
		v.Bits = uint64(C.dylib_read(mem, C.uint8_t(d.Type), isResult))
		if d.Type == Pointer {
			if owner := p.owners[v.Bits]; owner != nil {
				if d.Elem != nil && !sameLayout(*d.Elem, p.pointers[owner].desc) {
					p.err = fmt.Errorf("returned temporary pointer has an incompatible pointee type")
				}
				v.Bits = 0
				v.Pointee = owner
			} else {
				for _, b := range p.buffers {
					if v.Bits >= b.base && v.Bits-b.base < b.size {
						p.err = fmt.Errorf("returned interior pointer into temporary native storage cannot outlive the call")
						v.Bits = 0
					}
				}
			}
		}
	}
	return v
}
func (b *ffiBackend) invokeRecords(address uintptr, s Signature, args []Value) (Value, error) {
	work, err := b.acquireRecords()
	if err != nil {
		return Value{}, err
	}
	defer b.releaseRecords(work)
	pool := &work.pool
	resultDesc := s.ReturnType()
	resultType := b.result
	for i, v := range args {
		d := s.ArgumentType(i)
		t := b.args[i]
		if err := pool.write(v, d, t, work.args[i]); err != nil {
			return Value{}, err
		}
	}
	C.dylib_record_execute(b.plan, work.call, C.uintptr_t(address), work.out)
	for _, copy := range pool.pointers {
		*copy.value = pool.read(copy.desc, copy.native, copy.memory, false)
	}
	result := pool.read(resultDesc, resultType, work.out, true)
	if pool.err != nil {
		return Value{}, pool.err
	}
	return result, nil
}

type ffiCallbackBackend struct {
	plan            *ffiBackend
	native          *C.dylib_callback
	handle          uintptr
	receiverPadding bool
}

func prepareCallback(state *callbackState) (callbackBackend, error) {
	signature := state.signature
	// Old libffi thiscall closures over-discount register words after a 64-bit
	// or struct argument. A fastcall closure with an ignored EDX argument has
	// exactly the same ECX receiver, stack arguments and callee cleanup, and
	// works with both old and corrected libffi. The handler sees no padding.
	padding := signature.Convention == ThisCall && signature.Result != Struct && C.dylib_abi(C.uint8_t(ThisCall)) >= 0
	if padding {
		if uint64(len(signature.Args))+1 > 1<<32-1 || len(signature.Args) == int(^uint(0)>>1) {
			return nil, fmt.Errorf("thiscall callback padding exceeds the native argument limit")
		}
		signature.Convention = FastCall
		signature.Args = append([]Type{Pointer, U32}, signature.Args[1:]...)
		if len(signature.ArgTypes) != 0 {
			signature.ArgTypes = append([]TypeDesc{signature.ArgTypes[0], {Type: U32}}, signature.ArgTypes[1:]...)
		}
	}
	backend, err := prepare(signature)
	if err != nil {
		return nil, err
	}
	b := &ffiCallbackBackend{plan: backend.(*ffiBackend), handle: uintptr(cgo.NewHandle(state)), receiverPadding: padding}
	var status C.int
	b.native = C.dylib_callback_new(b.plan.plan, C.uintptr_t(b.handle), &status)
	if b.native == nil {
		cgo.Handle(b.handle).Delete()
		b.plan.close()
		return nil, fmt.Errorf("native callback preparation failed: %d", status)
	}
	return b, nil
}

func (b *ffiCallbackBackend) pointer() unsafe.Pointer { return C.dylib_callback_code(b.native) }
func (b *ffiCallbackBackend) close() {
	C.dylib_callback_free(b.native)
	cgo.Handle(b.handle).Delete()
	b.plan.close()
}
func (b *ffiCallbackBackend) dispatch(state *callbackState, result, arguments unsafe.Pointer) {
	args := make([]Value, len(state.signature.Args))
	pointers := unsafe.Slice((*unsafe.Pointer)(arguments), len(b.plan.args))
	// Native argument storage and pointee addresses are borrowed for this call.
	// The type graph is immutable; each invocation owns its logical Go values.
	pool := nativePool{abi: b.plan.pool.abi}
	for i := range args {
		nativeIndex := i
		if b.receiverPadding && i > 0 {
			nativeIndex++
		}
		args[i] = pool.read(state.signature.ArgumentType(i), b.plan.args[nativeIndex], pointers[nativeIndex], false)
	}
	value, err := state.handler(args)
	if err == nil {
		err = value.Validate(state.signature.ReturnType())
	}
	if err == nil && hasTemporaryPointees(value) {
		err = fmt.Errorf("callback result cannot contain temporary pointees")
	}
	if err != nil {
		state.record(err)
		return
	}
	if value.Type == Void {
		return
	}
	if value.Type == Struct {
		if err := pool.write(value, state.signature.ReturnType(), b.plan.result, result); err != nil {
			state.record(err)
		}
	} else {
		C.dylib_callback_store(result, C.uint8_t(value.Type), C.uint64_t(value.Bits))
	}
}
