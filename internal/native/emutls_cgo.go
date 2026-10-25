//go:build cgo && (darwin || linux || windows)

package native

/*
#cgo linux LDFLAGS: -pthread
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <stdatomic.h>
#include "destructor.h"
#if defined(__i386__)
// Raw MSVC i386 callers and Windows callbacks only promise four-byte incoming
// stack alignment; the GNU-built host helper may call sixteen-byte-aligned code.
#define DYLIB_TLS_ENTRY __attribute__((force_align_arg_pointer))
#else
#define DYLIB_TLS_ENTRY
#endif
#ifdef _WIN32
#include <windows.h>
#else
#include <pthread.h>
#endif

// The compiler ABI is four pointer-sized words on the supported targets.
// Only the object word is mutable. All other fields are validated in Go.
typedef struct {
    uintptr_t size, alignment;
    void *object;
    const void *initial;
} dylib_tls_descriptor;
_Static_assert(sizeof(dylib_tls_descriptor) == 4 * sizeof(uintptr_t), "emulated TLS descriptor ABI");
typedef struct dylib_tls_control dylib_tls_control;
typedef struct dylib_tls_thread dylib_tls_thread;
typedef struct dylib_tls_value dylib_tls_value;
typedef struct dylib_tls_exit dylib_tls_exit;
typedef struct {
    dylib_tls_control *controls;
    size_t live, pending, active;
    dylib_tls_descriptor *guard;
    unsigned char *(*native_guard)(void);
    int initializers_set;
    void (**initializers)(void);
    size_t initializer_count;
} dylib_tls_owner;
struct dylib_tls_control {
    dylib_tls_control *next;
    dylib_tls_owner *owner;
    dylib_tls_descriptor *descriptor;
    dylib_tls_value *values;
    uintptr_t size, alignment;
    const void *initial;
};
struct dylib_tls_thread {
    dylib_tls_value *values;
    dylib_tls_exit *entries;
    size_t pending;
};
struct dylib_tls_exit {
    dylib_tls_exit *next;
    dylib_tls_owner *owner;
    void (*function)(void *);
    void (*plain)(void);
    void *argument;
};
struct dylib_tls_value {
    dylib_tls_value *owner_next, **owner_previous;
    dylib_tls_value *thread_next, **thread_previous;
    dylib_tls_control *control;
    void *allocation, *address;
};

// One process-lifetime native key; neither its callback nor its thread state
// points into a retired image. The same lock covers owner close and thread exit.
static atomic_flag dylib_tls_lock = ATOMIC_FLAG_INIT;
static int dylib_tls_key_ready;
#ifdef _WIN32
static DWORD dylib_tls_key;
#else
static pthread_key_t dylib_tls_key;
#endif
static void dylib_tls_enter(void) {
    while (atomic_flag_test_and_set_explicit(&dylib_tls_lock, memory_order_acquire)) {}
}
static void dylib_tls_leave(void) {
    atomic_flag_clear_explicit(&dylib_tls_lock, memory_order_release);
}
static void dylib_tls_set_thread(dylib_tls_thread *thread) {
#ifdef _WIN32
    if (!FlsSetValue(dylib_tls_key, thread)) abort();
#else
    if (pthread_setspecific(dylib_tls_key, thread)) abort();
#endif
}
// Requires the native registry lock. Native callbacks never run under it.
static dylib_tls_thread *dylib_tls_current(int create) {
#ifdef _WIN32
    dylib_tls_thread *thread = (dylib_tls_thread *)FlsGetValue(dylib_tls_key);
#else
    dylib_tls_thread *thread = (dylib_tls_thread *)pthread_getspecific(dylib_tls_key);
#endif
    if (!thread && create) {
        thread = (dylib_tls_thread *)calloc(1, sizeof(*thread));
        if (!thread) abort();
        dylib_tls_set_thread(thread);
    }
    return thread;
}
static size_t dylib_tls_drain(dylib_tls_thread *thread, dylib_tls_owner *selected) {
    size_t drained = 0;
    for (;;) {
        dylib_tls_enter();
        dylib_tls_exit **cursor = &thread->entries;
        while (*cursor && selected && (*cursor)->owner != selected) cursor = &(*cursor)->next;
        dylib_tls_exit *entry = *cursor;
        if (!entry) { dylib_tls_leave(); return drained; }
        *cursor = entry->next;
        dylib_tls_owner *owner = entry->owner;
        --thread->pending;
        --owner->pending;
        ++owner->active;
        void (*function)(void *) = entry->function;
        void (*plain)(void) = entry->plain;
        void *argument = entry->argument;
        free(entry);
        dylib_tls_leave();
        if (plain) plain(); else dylib_call_destructor(function, argument);
        ++drained;
        dylib_tls_enter();
        --owner->active;
        dylib_tls_leave();
    }
}
static int dylib_tls_register(void (*function)(void *), void (*plain)(void),
                              void *argument, dylib_tls_owner *owner) {
    if ((!function && !plain) || !owner) return -1;
    dylib_tls_exit *entry = (dylib_tls_exit *)malloc(sizeof(*entry));
    if (!entry) return -1;
    dylib_tls_enter();
    dylib_tls_thread *thread = dylib_tls_current(1);
    if (thread->pending >= 4096) {
        dylib_tls_leave(); free(entry); return -1;
    }
    entry->owner = owner;
    entry->function = function;
    entry->plain = plain;
    entry->argument = argument;
    entry->next = thread->entries;
    thread->entries = entry;
    ++thread->pending;
    ++owner->pending;
    dylib_tls_leave();
    return 0;
}
static int DYLIB_TLS_ENTRY dylib_tls_atexit(void (*function)(void *), void *argument,
                                          void *dso, dylib_tls_owner *owner) {
    (void)dso; // The image-bound thunk supplies the owner independently of dso.
    return dylib_tls_register(function, NULL, argument, owner);
}
static int DYLIB_TLS_ENTRY dylib_tls_tlregdtor(void (*function)(void), dylib_tls_owner *owner) {
    return dylib_tls_register(NULL, function, NULL, owner);
}
static void dylib_tls_remove(dylib_tls_value *v) {
    *v->owner_previous = v->owner_next;
    if (v->owner_next) v->owner_next->owner_previous = v->owner_previous;
    *v->thread_previous = v->thread_next;
    if (v->thread_next) v->thread_next->thread_previous = v->thread_previous;
    --v->control->owner->live;
    free(v->allocation);
    free(v);
}
#ifdef _WIN32
static void DYLIB_TLS_ENTRY WINAPI dylib_tls_thread_exit(void *p)
#else
static void DYLIB_TLS_ENTRY dylib_tls_thread_exit(void *p)
#endif
{
    dylib_tls_thread *thread = (dylib_tls_thread *)p;
    // pthread/FLS exit dispatch may clear the key first. Reentrant destructors
    // must see the same TLS values and registration list until draining ends.
    dylib_tls_set_thread(thread);
    dylib_tls_drain(thread, NULL);
    dylib_tls_set_thread(NULL);
    dylib_tls_enter();
    while (thread->values) dylib_tls_remove(thread->values);
    dylib_tls_leave();
    free(thread);
}
static dylib_tls_owner *dylib_tls_new(void) {
    dylib_tls_enter();
    if (!dylib_tls_key_ready) {
#ifdef _WIN32
        dylib_tls_key = FlsAlloc(dylib_tls_thread_exit);
        dylib_tls_key_ready = dylib_tls_key != FLS_OUT_OF_INDEXES;
#else
        dylib_tls_key_ready = pthread_key_create(&dylib_tls_key, dylib_tls_thread_exit) == 0;
#endif
    }
    int ready = dylib_tls_key_ready;
    dylib_tls_leave();
    return ready ? (dylib_tls_owner *)calloc(1, sizeof(dylib_tls_owner)) : NULL;
}
static int dylib_tls_add(dylib_tls_owner *owner, uintptr_t address) {
    dylib_tls_control *c = (dylib_tls_control *)calloc(1, sizeof(*c));
    if (!c) return -1;
    c->owner = owner;
    c->descriptor = (dylib_tls_descriptor *)address;
    // Snapshot validated fields before native initialization can write data.
    c->size = c->descriptor->size;
    c->alignment = c->descriptor->alignment;
    c->initial = c->descriptor->initial;
    c->next = owner->controls;
    owner->controls = c;
    c->descriptor->object = c;
    return 0;
}
static void *DYLIB_TLS_ENTRY dylib_tls_get_address(dylib_tls_descriptor *descriptor) {
    dylib_tls_enter();
    dylib_tls_control *c = (dylib_tls_control *)descriptor->object;
    if (!c || c->descriptor != descriptor) abort();
    dylib_tls_thread *thread = dylib_tls_current(1);
    for (dylib_tls_value *v = thread->values; v; v = v->thread_next) {
        if (v->control == c) {
            void *address = v->address;
            dylib_tls_leave();
            return address;
        }
    }
    dylib_tls_value *v = (dylib_tls_value *)calloc(1, sizeof(*v));
    if (!v) abort();
    v->allocation = malloc(c->size + c->alignment - 1);
    if (!v->allocation) abort();
    v->address = (void *)(((uintptr_t)v->allocation + c->alignment - 1) & ~(c->alignment - 1));
    if (c->initial) memcpy(v->address, c->initial, c->size);
    else memset(v->address, 0, c->size);
    v->control = c;
    v->owner_next = c->values;
    v->owner_previous = &c->values;
    if (c->values) c->values->owner_previous = &v->owner_next;
    c->values = v;
    v->thread_next = thread->values;
    v->thread_previous = &thread->values;
    if (thread->values) thread->values->thread_previous = &v->thread_next;
    thread->values = v;
    ++c->owner->live;
    void *address = v->address;
    dylib_tls_leave();
    return address;
}
static uintptr_t dylib_tls_helper(void) { return (uintptr_t)&dylib_tls_get_address; }
static uintptr_t dylib_tls_exit_helper(void) { return (uintptr_t)&dylib_tls_atexit; }
static void DYLIB_TLS_ENTRY dylib_tls_msvc_init(dylib_tls_owner *owner) {
    // No registry lock crosses constructors, which may reenter Go or register
    // destructors. Mark the thread guard before calling any initializer.
    dylib_tls_enter();
    ++owner->active;
    dylib_tls_leave();
    unsigned char *guard = owner->guard ? (unsigned char *)dylib_tls_get_address(owner->guard) : NULL;
    unsigned char *native_guard = owner->native_guard ? owner->native_guard() : NULL;
    if ((guard && !*guard) || (native_guard && !*native_guard)) {
        if (guard) *guard = 1;
        if (native_guard) *native_guard = 1;
        for (size_t i = 0; i < owner->initializer_count; ++i) owner->initializers[i]();
    }
    dylib_tls_enter();
    --owner->active;
    dylib_tls_leave();
}
static int dylib_tls_set_initializers(dylib_tls_owner *owner, uintptr_t guard,
                                      const uintptr_t *initializers, size_t count) {
    if ((!guard && !owner->native_guard) || count > 4096 || owner->initializers_set) return -1;
    void (**copy)(void) = count ? (void (**)(void))malloc(count * sizeof(*copy)) : NULL;
    if (count && !copy) return -1;
    for (size_t i = 0; i < count; ++i) copy[i] = (void (*)(void))initializers[i];
    owner->guard = (dylib_tls_descriptor *)guard;
    owner->initializers_set = 1;
    owner->initializers = copy;
    owner->initializer_count = count;
    return 0;
}
static int dylib_tls_set_native_guard(dylib_tls_owner *owner, uintptr_t entry) {
    if (!entry || owner->native_guard || owner->initializers_set) return -1;
    owner->native_guard = (unsigned char *(*)(void))entry;
    return 0;
}
static uintptr_t dylib_tls_msvc_init_helper(void) { return (uintptr_t)&dylib_tls_msvc_init; }
static uintptr_t dylib_tls_msvc_exit_helper(void) { return (uintptr_t)&dylib_tls_tlregdtor; }
static size_t dylib_tls_prepare_close(dylib_tls_owner *owner, size_t *drained) {
    dylib_tls_enter();
    dylib_tls_thread *thread = dylib_tls_current(0);
    dylib_tls_leave();
    *drained = thread ? dylib_tls_drain(thread, owner) : 0;
    dylib_tls_enter();
    size_t pending = owner->pending + owner->active;
    dylib_tls_leave();
    return pending;
}
static size_t dylib_tls_live(dylib_tls_owner *owner) {
    dylib_tls_enter();
    size_t live = owner->live;
    dylib_tls_leave();
    return live;
}
static void dylib_tls_free(dylib_tls_owner *owner) {
    dylib_tls_enter();
    if (owner->pending || owner->active) abort();
    while (owner->controls) {
        dylib_tls_control *c = owner->controls;
        while (c->values) dylib_tls_remove(c->values);
        c->descriptor->object = NULL;
        owner->controls = c->next;
        free(c);
    }
    dylib_tls_leave();
    free(owner->initializers);
    free(owner);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// EmulatedTLS owns native per-thread values for one raw image. Close requires
// all native users to have stopped and DrainThreadDestructors to report zero
// pending/active callbacks. The session retains its image until then.
type EmulatedTLS struct{ context *C.dylib_tls_owner }

func NewEmulatedTLS() (*EmulatedTLS, error) {
	p := C.dylib_tls_new()
	if p == nil {
		return nil, fmt.Errorf("allocate native emulated TLS owner/key")
	}
	return &EmulatedTLS{context: p}, nil
}
func EmulatedTLSHelper() uintptr           { return uintptr(C.dylib_tls_helper()) }
func (t *EmulatedTLS) Context() uintptr    { return uintptr(unsafe.Pointer(t.context)) }
func EmulatedTLSThreadExitHelper() uintptr { return uintptr(C.dylib_tls_exit_helper()) }
func EmulatedTLSMSVCInitHelper() uintptr   { return uintptr(C.dylib_tls_msvc_init_helper()) }
func EmulatedTLSMSVCExitHelper() uintptr   { return uintptr(C.dylib_tls_msvc_exit_helper()) }
func (t *EmulatedTLS) SetNativeThreadGuard(entry uintptr) error {
	if C.dylib_tls_set_native_guard(t.context, C.uintptr_t(entry)) != 0 {
		return fmt.Errorf("configure native MSVC TLS guard")
	}
	return nil
}
func (t *EmulatedTLS) SetThreadInitializers(guard uintptr, entries []uintptr) error {
	if C.dylib_tls_set_initializers(t.context, C.uintptr_t(guard), (*C.uintptr_t)(unsafe.Pointer(unsafe.SliceData(entries))), C.size_t(len(entries))) != 0 {
		return fmt.Errorf("allocate native MSVC thread initializer table")
	}
	return nil
}
func (t *EmulatedTLS) DrainThreadDestructors() (uint64, bool) {
	if t == nil || t.context == nil {
		return 0, false
	}
	var drained C.size_t
	pending := C.dylib_tls_prepare_close(t.context, &drained)
	return uint64(pending), drained != 0
}
func (t *EmulatedTLS) Add(descriptor uintptr) error {
	if C.dylib_tls_add(t.context, C.uintptr_t(descriptor)) != 0 {
		return fmt.Errorf("allocate native emulated TLS control")
	}
	return nil
}
func (t *EmulatedTLS) LiveValues() int {
	if t == nil || t.context == nil {
		return 0
	}
	return int(C.dylib_tls_live(t.context))
}
func (t *EmulatedTLS) Close() {
	if t != nil && t.context != nil {
		C.dylib_tls_free(t.context)
		t.context = nil
	}
}
