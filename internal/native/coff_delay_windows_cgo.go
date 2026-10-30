//go:build windows && cgo

package native

/*
#include <stdint.h>
#include <windows.h>
#include <delayimp.h>

// Use the installed SDK's DelayLoadInfo and descriptor definitions. Entry
// storage is owned by the raw image; each request lives on the native stack.
typedef struct {
    uintptr_t callback, owner, slot;
    PCImgDelayDescr descriptor;
    FARPROC *cell;
    const char *dll, *name;
    unsigned ordinal;
} dylib_coff_delay_entry;
typedef struct dylib_coff_delay_request {
    uintptr_t owner, slot, thread;
    unsigned recovering;
    DWORD exception;
    DelayLoadInfo info;
} dylib_coff_delay_request;
typedef uintptr_t (WINAPI *dylib_coff_delay_dispatch)(uintptr_t);

static uintptr_t __cdecl dylib_coff_delay_resolve(dylib_coff_delay_entry *entry) {
    dylib_coff_delay_request request = {0};
    request.owner = entry->owner;
    request.slot = entry->slot;
    request.thread = GetCurrentThreadId();
    request.info.cb = (DWORD)sizeof(request.info);
    request.info.pidd = entry->descriptor;
    request.info.ppfn = entry->cell;
    request.info.szDll = entry->dll;
    request.info.dlp.fImportByName = entry->ordinal == 0;
    if (entry->ordinal) request.info.dlp.dwOrdinal = entry->ordinal;
    else request.info.dlp.szProcName = entry->name;
    dylib_coff_delay_dispatch dispatch = (dylib_coff_delay_dispatch)entry->callback;
    uintptr_t target = dispatch((uintptr_t)&request);
    // Only native frames exist above the callback now. A handler can recover
    // a missing module, then separately recover a missing procedure.
    for (unsigned attempt = 0; !target && attempt < 2 &&
            request.exception != ERROR_INVALID_PARAMETER; ++attempt) {
        DWORD error = request.exception ? request.exception : ERROR_INVALID_PARAMETER;
        ULONG_PTR parameters[] = {(ULONG_PTR)&request.info};
        RaiseException(VcppException(ERROR_SEVERITY_ERROR, error), 0, 1, parameters);
        request.recovering = 1;
        request.exception = 0;
        target = dispatch((uintptr_t)&request);
    }
    if (!target) {
        DWORD error = request.exception ? request.exception : ERROR_INVALID_PARAMETER;
        DWORD code = VcppException(ERROR_SEVERITY_ERROR, error);
        ULONG_PTR parameters[] = {(ULONG_PTR)&request.info};
        RaiseException(code, EXCEPTION_NONCONTINUABLE, 1, parameters);
        // An invalid handler must never make a generic call jump to zero.
        TerminateProcess(GetCurrentProcess(), code);
        for (;;) {}
    }
    void *previous = InterlockedCompareExchangePointer(
        (void *volatile *)entry->cell, (void *)target, NULL);
    return previous ? (uintptr_t)previous : target;
}
static uintptr_t dylib_coff_delay_helper(void) {
    return (uintptr_t)&dylib_coff_delay_resolve;
}
static size_t dylib_coff_delay_entry_size(void) {
    return sizeof(dylib_coff_delay_entry);
}
static void dylib_coff_delay_configure(uintptr_t address, uintptr_t callback,
        uintptr_t owner, uintptr_t slot, uintptr_t descriptor, uintptr_t cell,
        uintptr_t dll, uintptr_t name, unsigned ordinal) {
    dylib_coff_delay_entry *entry = (dylib_coff_delay_entry *)address;
    entry->callback = callback;
    entry->owner = owner;
    entry->slot = slot;
    entry->descriptor = (PCImgDelayDescr)descriptor;
    entry->cell = (FARPROC *)cell;
    entry->dll = (const char *)dll;
    entry->name = (const char *)name;
    entry->ordinal = ordinal;
}
*/
import "C"

import "unsafe"

func COFFDelayHelper() uintptr { return uintptr(C.dylib_coff_delay_helper()) }
func COFFDelayEntrySize() int  { return int(C.dylib_coff_delay_entry_size()) }

func ConfigureCOFFDelayEntry(address, callback, owner, slot, descriptor, cell, dll, name uintptr, ordinal uint16) bool {
	width := unsafe.Sizeof(uintptr(0))
	if address == 0 || address%width != 0 || callback == 0 || owner == 0 || descriptor == 0 || cell == 0 || cell%width != 0 || dll == 0 || ordinal == 0 && name == 0 {
		return false
	}
	C.dylib_coff_delay_configure(C.uintptr_t(address), C.uintptr_t(callback), C.uintptr_t(owner), C.uintptr_t(slot), C.uintptr_t(descriptor), C.uintptr_t(cell), C.uintptr_t(dll), C.uintptr_t(name), C.uint(ordinal))
	return true
}

func ReadCOFFDelayRequest(address uintptr) (COFFDelayRequest, bool) {
	if address == 0 {
		return COFFDelayRequest{}, false
	}
	// This address belongs to a live native helper frame, not the Go heap.
	pointer := *(*unsafe.Pointer)(unsafe.Pointer(&address))
	request := (*C.struct_dylib_coff_delay_request)(pointer)
	return COFFDelayRequest{
		Owner: uintptr(request.owner), Slot: uintptr(request.slot), Thread: uintptr(request.thread),
		Recovering: request.recovering != 0,
		Module:     *(*uintptr)(unsafe.Pointer(&request.info.hmodCur)), Address: *(*uintptr)(unsafe.Pointer(&request.info.pfnCur)),
		NativeError: uint32(request.info.dwLastError), Exception: uint32(request.exception),
	}, true
}

func SetCOFFDelayResult(address, target, module uintptr, exception, nativeError uint32) bool {
	if address == 0 {
		return false
	}
	pointer := *(*unsafe.Pointer)(unsafe.Pointer(&address))
	request := (*C.struct_dylib_coff_delay_request)(pointer)
	// Use the SDK-derived field addresses directly, avoiding another cgo
	// call from the callback just to update native request storage.
	*(*uintptr)(unsafe.Pointer(&request.info.pfnCur)) = target
	*(*uintptr)(unsafe.Pointer(&request.info.hmodCur)) = module
	request.info.dwLastError = C.DWORD(nativeError)
	request.exception = C.DWORD(exception)
	return true
}
