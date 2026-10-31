#include <stdint.h>
#include <windows.h>
// Use the installed SDK types while allowing both mutable and const pointer
// definitions, independently of the SDK version's declaration qualifiers.
#define __pfnDliNotifyHook2 sdk_notify_declaration
#define __pfnDliFailureHook2 sdk_failure_declaration
#include <delayimp.h>
#undef __pfnDliNotifyHook2
#undef __pfnDliFailureHook2
#ifdef HOOK_ALIAS_STORAGE
#define __pfnDliNotifyHook2 hook_notify_storage
#define __pfnDliFailureHook2 hook_failure_storage
#endif
#ifndef HOOK_STORAGE
#define HOOK_STORAGE
#endif

unsigned hook_mode;
LONG hook_counts[6],hook_sequence[64],hook_steps,hook_bad;
WCHAR hook_path[4096];
uintptr_t hook_replacement;
static LONG nested_once;
extern int nested(int,int);

static FARPROC WINAPI delay_hook(unsigned event,PDelayLoadInfo info) {
    if(event>5) {__atomic_store_n(&hook_bad,1,__ATOMIC_SEQ_CST);return 0;}
    __atomic_fetch_add(&hook_counts[event],1,__ATOMIC_SEQ_CST);
    LONG index=__atomic_fetch_add(&hook_steps,1,__ATOMIC_SEQ_CST);
    if(index<64) hook_sequence[index]=(LONG)event;
    if(!info || info->cb!=sizeof(*info) || !info->pidd ||
       info->pidd->grAttrs!=dlattrRva || !info->ppfn || !info->szDll ||
       (info->dlp.fImportByName ? !info->dlp.szProcName :
          (info->dlp.dwOrdinal!=7 && info->dlp.dwOrdinal!=65535))) {
        __atomic_store_n(&hook_bad,2,__ATOMIC_SEQ_CST);return 0;
    }
    if(event==dliNotePreGetProcAddress && !info->hmodCur)
        __atomic_store_n(&hook_bad,3,__ATOMIC_SEQ_CST);
    if(event==dliFailLoadLib && (!info->dwLastError || info->hmodCur))
        __atomic_store_n(&hook_bad,4,__ATOMIC_SEQ_CST);
    if(event==dliFailGetProc && (!info->dwLastError || !info->hmodCur))
        __atomic_store_n(&hook_bad,5,__ATOMIC_SEQ_CST);
    if(event==dliNoteEndProcessing) {
        if(info->dwLastError || !info->pfnCur ||
           (hook_mode!=1 && *info->ppfn!=info->pfnCur))
            __atomic_store_n(&hook_bad,6,__ATOMIC_SEQ_CST);
        // Both End returns must be ignored by the owner.
        return (FARPROC)(uintptr_t)1;
    }
    if(hook_mode==1 && event==dliStartProcessing)
        return (FARPROC)hook_replacement;
    if((hook_mode==2 && event==dliNotePreLoadLibrary) ||
       (hook_mode==4 && event==dliFailLoadLib))
        return (FARPROC)LoadLibraryW(hook_path);
    if((hook_mode==3 && event==dliNotePreGetProcAddress) ||
       (hook_mode==5 && event==dliFailGetProc))
        return GetProcAddress(info->hmodCur,"add");
    if(hook_mode==6 && event==dliStartProcessing &&
       __atomic_exchange_n(&nested_once,1,__ATOMIC_SEQ_CST)==0) {
        if(nested(20,22)!=42) __atomic_store_n(&hook_bad,7,__ATOMIC_SEQ_CST);
    }
    return 0;
}
#ifdef HOOK_INIT_CRT
PfnDliHook __pfnDliNotifyHook2, __pfnDliFailureHook2;
#else
HOOK_STORAGE PfnDliHook __pfnDliNotifyHook2=delay_hook;
HOOK_STORAGE PfnDliHook __pfnDliFailureHook2=delay_hook;
#endif
LONG hook_installed;

// Updating mutable cells before first use is covered separately from the
// initialized const cells. The caller ensures native quiescence.
void install_hooks(void) {
    ++hook_installed;
#ifndef HOOK_CONST
    __pfnDliNotifyHook2=delay_hook;
    __pfnDliFailureHook2=delay_hook;
#endif
}

#ifdef HOOK_INIT_CRT
__attribute__((used,section(".CRT$XCU"))) static void (*const startup)(void)=install_hooks;
#endif
