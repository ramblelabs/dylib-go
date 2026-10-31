#include <stdint.h>
#include <stdarg.h>
#include <windows.h>
#include <delayimp.h>
#include <string.h>

#define EXPORT __declspec(dllexport)
EXPORT volatile uintptr_t factory_entry;
EXPORT uintptr_t factory_cell;
EXPORT uintptr_t recovery_target, recovery_module;
EXPORT volatile LONG native_exceptions;
EXPORT DWORD probe_thread;
static PVOID handler, continue_handler;
// Windows invokes continue handlers even after a VEH recovers. Go's final
// continue handler otherwise treats this foreign exception as fatal. Only
// suppress that path for the exact record recovered on this native thread.
static _Thread_local EXCEPTION_RECORD *recovered_exception;
static int recovery_mode;

typedef struct { int32_t a,b; } Pair;
typedef struct { int64_t a,b,c,d; } Large;
typedef struct { double a,b,c,d; } HFA;
EXPORT int32_t scalar(int32_t a,int32_t b) { return a+b; }
static double mixed(int32_t a,double b,float c,uint64_t d,int16_t e,uint8_t f,
                    double g,double h,double i,double j,double k,double l) {
    return a+b+c+d+e+f+g+h+i+j+k+l;
}
static int32_t pair(Pair value) { return value.a+value.b; }
static Large wide(Large value,int32_t marker,double fraction,const int64_t *native,
                   double a,double b,double c,double d,double e,double f) {
    value.a+=marker;value.b+=(int64_t)fraction;value.c+=*native;
    value.d+=(int64_t)(a+b+c+d+e+f);return value;
}
static HFA homogeneous(HFA a,HFA b) {
    return (HFA){a.a+b.d,a.b+b.c,a.c+b.b,a.d+b.a};
}
static double variadic(int count,...) {
    va_list ap;va_start(ap,count);double value=0;
    for(int i=0;i<count;++i) value+=va_arg(ap,double);
    va_end(ap);return value;
}
static int32_t pointers(const int32_t *a,Pair *value,int32_t *out,void *empty) {
    if(empty) return -1;
    ++value->a;*out=*a+value->b;return *out;
}
#if defined(__x86_64__) || defined(__i386__)
static double __attribute__((vectorcall)) vector(HFA value,double a,double b,int32_t seed) {
    return value.a+value.b+value.c+value.d+a+b+seed;
}
#endif
#if defined(__i386__)
static int32_t __attribute__((stdcall)) stdcall_target(int32_t a,int32_t b) {return a+b;}
static int32_t __attribute__((fastcall)) fastcall_target(int32_t a,int32_t b) {return a+b;}
static int32_t __attribute__((thiscall)) thiscall_target(Pair *self,int32_t a) {return self->a+self->b+a;}
#endif
EXPORT const uintptr_t targets[] = {
    (uintptr_t)&scalar,(uintptr_t)&mixed,(uintptr_t)&pair,(uintptr_t)&wide,
    (uintptr_t)&homogeneous,(uintptr_t)&variadic,(uintptr_t)&pointers
#if defined(__x86_64__) || defined(__i386__)
    ,(uintptr_t)&vector
#endif
#if defined(__i386__)
    ,(uintptr_t)&stdcall_target,(uintptr_t)&fastcall_target,(uintptr_t)&thiscall_target
#endif
};
EXPORT int32_t probe(int32_t kind,int32_t seed) {
    switch(kind) {
    case 0:return ((int32_t (*)(int32_t,int32_t))factory_entry)(seed,42-seed);
    case 1:{
        double result=((double (*)(int32_t,double,float,uint64_t,int16_t,uint8_t,
              double,double,double,double,double,double))factory_entry)(
              seed,22.5,2.0f,6,-3,5,1,2,3,4,5,6);
        return result==seed+53.5 ? 42 : -1;
    }
    case 2:return ((int32_t (*)(Pair))factory_entry)((Pair){seed,42-seed});
    case 3:{
        int64_t native=3;
        Large value=((Large (*)(Large,int32_t,double,const int64_t *,
              double,double,double,double,double,double))factory_entry)(
              (Large){1,2,3,4},seed,2.5,&native,1,2,3,4,5,6);
        return value.a==seed+1 && value.b==4 && value.c==6 && value.d==25 ? 42 : -1;
    }
    case 4:{
        HFA value=((HFA (*)(HFA,HFA))factory_entry)((HFA){seed,1,2,3},(HFA){4,5,6,7});
        return value.a==seed+7 && value.b==7 && value.c==7 && value.d==7 ? 42 : -1;
    }
    case 5:{
        double value=((double (*)(int,...))factory_entry)(12,(double)seed,
                2.0,3.0,4.0,5.0,6.0,7.0,8.0,9.0,10.0,11.0,12.0);
        return value==seed+77 ? 42 : -1;
    }
    case 6:{
        Pair value={seed,22};int32_t out=0;
        int32_t result=((int32_t (*)(const int32_t *,Pair *,int32_t *,void *))factory_entry)(
                &seed,&value,&out,0);
        return result==seed+22 && out==seed+22 && value.a==seed+1 ? 42 : -1;
    }
#if defined(__x86_64__) || defined(__i386__)
    case 7:{
        double result=((double (__attribute__((vectorcall)) *)(HFA,double,double,int32_t))factory_entry)(
                (HFA){1,2,3,4},22,3,seed);
        return result==seed+35 ? 42 : -1;
    }
#endif
#if defined(__i386__)
    case 8:return ((int32_t (__attribute__((stdcall)) *)(int32_t,int32_t))factory_entry)(seed,42-seed);
    case 9:return ((int32_t (__attribute__((fastcall)) *)(int32_t,int32_t))factory_entry)(seed,42-seed);
    case 10:{
        Pair value={20,2};
        return ((int32_t (__attribute__((thiscall)) *)(Pair *,int32_t))factory_entry)(&value,seed);
    }
#endif
    }
    return -2;
}
#if defined(__aarch64__)
EXPORT int32_t check_nonvolatile_fp(int32_t seed,int32_t unused) {
    (void)unused;
    register double a __asm__("d8")=8.25,b __asm__("d9")=9.25;
    register double c __asm__("d10")=10.25,d __asm__("d11")=11.25;
    register double e __asm__("d12")=12.25,f __asm__("d13")=13.25;
    register double g __asm__("d14")=14.25,h __asm__("d15")=15.25;
    // Keep real nonvolatile FP registers live across the first and cached
    // calls, including the native helper and its allocating Go callback.
    __asm__ volatile("" : "+w"(a),"+w"(b),"+w"(c),"+w"(d),"+w"(e),"+w"(f),"+w"(g),"+w"(h));
    int32_t result=((int32_t (*)(int32_t,int32_t))factory_entry)(seed,42-seed);
    // Output-only operands read the physical registers without restoring the
    // earlier C variables first, including when compiled without optimization.
    __asm__ volatile("" : "=w"(a),"=w"(b),"=w"(c),"=w"(d),"=w"(e),"=w"(f),"=w"(g),"=w"(h));
    if(a!=8.25 || b!=9.25 || c!=10.25 || d!=11.25 ||
       e!=12.25 || f!=13.25 || g!=14.25 || h!=15.25) return -1;
    return result;
}
#endif
static DWORD WINAPI worker(void *argument) {
    int32_t seed=(int32_t)(uintptr_t)argument;
    for(int i=0;i<100;++i) if(probe(0,seed)!=42) return 1;
    return 0;
}
EXPORT int32_t threads(int32_t seed,int32_t unused) {
    (void)unused;HANDLE handles[8];probe_thread=GetCurrentThreadId();
    int count=0;int32_t result=42;
    for(int i=0;i<8;++i) {
        handles[i]=CreateThread(0,0,worker,(void *)(uintptr_t)seed,0,0);
        if(!handles[i]) {result=-1;break;}
        ++count;
    }
    for(int i=0;i<count;++i) {
        if(WaitForSingleObject(handles[i],INFINITE)!=WAIT_OBJECT_0) result=-2;
        DWORD code=1;if(!GetExitCodeThread(handles[i],&code) || code) result=-3;
        CloseHandle(handles[i]);
    }
    return result;
}
static LONG WINAPI recover(EXCEPTION_POINTERS *exception) {
    DWORD code=exception->ExceptionRecord->ExceptionCode;
    if((code&0xffffff00u)!=0xc06d0000u || exception->ExceptionRecord->NumberParameters!=1)
        return EXCEPTION_CONTINUE_SEARCH;
    if(exception->ExceptionRecord->ExceptionFlags&EXCEPTION_NONCONTINUABLE)
        return EXCEPTION_CONTINUE_SEARCH;
    DelayLoadInfo *info=(DelayLoadInfo *)exception->ExceptionRecord->ExceptionInformation[0];
    if(!info || info->cb!=sizeof(*info) || !info->pidd || info->pidd->grAttrs!=dlattrRva || !info->ppfn)
        return EXCEPTION_CONTINUE_SEARCH;
    if(strcmp(info->szDll,"prototype.dll") ||
       (info->dlp.fImportByName ? (strcmp(info->dlp.szProcName,"entry") && strcmp(info->dlp.szProcName,"scalar")) : info->dlp.dwOrdinal!=65535))
        return EXCEPTION_CONTINUE_SEARCH;
    InterlockedIncrement(&native_exceptions);
    if(recovery_mode==1) info->hmodCur=(HMODULE)recovery_module;
    else if(recovery_mode==2) info->pfnCur=(FARPROC)recovery_target;
    else if(recovery_mode==3) {
        if((code&0xff)==ERROR_MOD_NOT_FOUND) info->hmodCur=(HMODULE)recovery_module;
        else info->pfnCur=(FARPROC)recovery_target;
    }
    else if(recovery_mode==4 || recovery_mode==5) {
        if((code&0xff)==ERROR_MOD_NOT_FOUND) {
            // Transfer a real, separately acquired loader reference to the
            // image. The control DLL still retains its original reference.
            HMODULE owned=0;
            if(!GetModuleHandleExA(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS,
                    (LPCSTR)recovery_target,&owned)) return EXCEPTION_CONTINUE_SEARCH;
            info->hmodCur=owned;
        } else if(recovery_mode==5) info->pfnCur=(FARPROC)recovery_target;
        else return EXCEPTION_CONTINUE_SEARCH;
    }
    recovered_exception=exception->ExceptionRecord;
    return EXCEPTION_CONTINUE_EXECUTION;
}
static LONG WINAPI continue_recovered(EXCEPTION_POINTERS *exception) {
    if(exception->ExceptionRecord!=recovered_exception) return EXCEPTION_CONTINUE_SEARCH;
    recovered_exception=0;
    return EXCEPTION_CONTINUE_EXECUTION;
}
EXPORT int32_t configure_handler(int32_t mode,int32_t unused) {
    (void)unused;SetErrorMode(SEM_FAILCRITICALERRORS|SEM_NOGPFAULTERRORBOX);
    if(handler) {if(!RemoveVectoredExceptionHandler(handler)) return -2;handler=0;}
    if(continue_handler) {if(!RemoveVectoredContinueHandler(continue_handler)) return -3;continue_handler=0;}
    recovered_exception=0;recovery_mode=mode;native_exceptions=0;
    if(mode) {
        continue_handler=AddVectoredContinueHandler(1,continue_recovered);
        if(!continue_handler) return -4;
        handler=AddVectoredExceptionHandler(1,recover);
        if(!handler) {
            RemoveVectoredContinueHandler(continue_handler);continue_handler=0;
            return -1;
        }
    }
    return 0;
}

#if defined(__x86_64__) || defined(__aarch64__)
/* Ask Windows itself to unwind synthetic native contexts at instruction
 * boundaries, including partially executed prologues and tail-jump epilogues.
 * The states follow the independent assembler references, not the Go bytes. */
EXPORT int32_t check_unwind(int32_t unused,int32_t ignored) {
    (void)unused;(void)ignored;DWORD64 base=0;
    if(RtlLookupFunctionEntry((DWORD64)factory_entry,&base,0)) return -1;
    PRUNTIME_FUNCTION function=RtlLookupFunctionEntry((DWORD64)factory_entry+64,&base,0);
    if(!function) return -2;
    uintptr_t begin=(uintptr_t)base+function->BeginAddress;
    _Alignas(16) uintptr_t stack[128]={0};
    uintptr_t previous=(uintptr_t)&stack[112],returned=(uintptr_t)&scalar;
    if(!factory_cell) return -3;
    *(uintptr_t *)factory_cell=returned;
#if defined(__x86_64__)
    uintptr_t *initial=&stack[65];initial[0]=returned;initial[-1]=previous;
    const unsigned offsets[]={0,1,8,16,139,248,255,256};
    for(unsigned i=0;i<sizeof(offsets)/sizeof(offsets[0]);++i) {
        CONTEXT context={0};context.ContextFlags=CONTEXT_FULL;
        context.Rip=begin+offsets[i];context.Rbp=previous;context.Rsp=(DWORD64)initial;
        initial[1]=returned;context.R11=(DWORD64)(initial+1);
        if(i==1) context.Rsp=(DWORD64)(initial-1);
        else if(i>=2 && i<=5) context.Rsp=(DWORD64)(initial-29);
        else if(i==6) context.Rsp=(DWORD64)(initial-1);
        if(i>=3 && i<=6) context.Rbp=(DWORD64)(initial-1);
        PVOID data=0;DWORD64 frame=0;
        RtlVirtualUnwind(UNW_FLAG_NHANDLER,base,context.Rip,function,&context,&data,&frame,0);
        if(context.Rip!=returned || context.Rsp!=(DWORD64)(initial+1) || context.Rbp!=previous)
            return -10-(int32_t)i;
    }
#else
    uintptr_t *initial=&stack[64];initial[-18]=previous;initial[-17]=returned;
    const unsigned offsets[]={0,4,8,12,84,124,128,132};
    for(unsigned i=0;i<sizeof(offsets)/sizeof(offsets[0]);++i) {
        CONTEXT context={0};context.ContextFlags=CONTEXT_FULL;
        context.Pc=begin+offsets[i];context.Fp=previous;context.Lr=returned;context.Sp=(DWORD64)initial;
        context.X[17]=returned;
        if(i>=1 && i<=6) context.Sp=(DWORD64)(initial-28);
        if(i>=3 && i<=5) context.Fp=(DWORD64)(initial-18);
        if(i==4 || i==5) context.Lr=begin+84;
        PVOID data=0;DWORD64 frame=0;
        RtlVirtualUnwind(UNW_FLAG_NHANDLER,base,context.Pc,function,&context,&data,&frame,0);
        if(context.Pc!=returned || context.Sp!=(DWORD64)initial || context.Fp!=previous)
            return -10-(int32_t)i;
    }
#endif
    return 42;
}
#endif
