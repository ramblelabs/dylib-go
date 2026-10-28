#ifndef CBRIDGE_CONVENTIONS_H
#define CBRIDGE_CONVENTIONS_H
#include <stdint.h>

#if defined(_WIN32)
#define CC_API __declspec(dllexport) __attribute__((noinline))
#else
#define CC_API __attribute__((visibility("default"),noinline))
#endif

typedef struct __attribute__((packed,aligned(8))) {
    unsigned :3;
    const float a;
    const int32_t b;
} CCPair;
typedef struct { const double values[5]; const int32_t marker; } CCLarge;
typedef struct { float a,b,c,d; } CCHFA;

#define CC_DECLARE(prefix,cc) \
typedef CCPair (cc *prefix##_Entry)(void *,CCPair,int32_t); \
typedef CCHFA (cc *prefix##_HFAEntry)(void *,CCHFA,float); \
CC_API int32_t cc prefix##_scalar(void *,int32_t,double); \
CC_API void *cc prefix##_pointer(void *,void *); \
CC_API CCPair cc prefix##_pair(void *,CCPair,int32_t); \
CC_API CCLarge cc prefix##_large(void *,CCLarge,int32_t); \
CC_API CCHFA cc prefix##_hfa(void *,CCHFA,float); \
CC_API double cc prefix##_spill(void *,int32_t,int32_t,int32_t,int32_t,int32_t,int32_t,int32_t,int32_t,double,double,double,double,double,double,double,double,double,double,CCPair); \
CC_API prefix##_Entry prefix##_factory(void); \
CC_API CCPair prefix##_call(prefix##_Entry,void *,CCPair,int32_t); \
CC_API CCPair cc prefix##_forward(void *,prefix##_Entry,CCPair,int32_t); \
CC_API prefix##_HFAEntry prefix##_hfa_factory(void); \
CC_API CCHFA prefix##_hfa_call(prefix##_HFAEntry,void *,CCHFA,float); \
CC_API int32_t prefix##_control(void);

CC_API void *cc_state(void);
CC_DECLARE(cc_cdecl,)
#if defined(__x86_64__)
CC_DECLARE(cc_ms,__attribute__((ms_abi)))
CC_DECLARE(cc_sysv,__attribute__((sysv_abi)))
#endif
#if defined(_WIN32) && defined(__i386__)
CC_DECLARE(cc_std,__attribute__((stdcall)))
CC_DECLARE(cc_fast,__attribute__((fastcall)))
CC_DECLARE(cc_this,__attribute__((thiscall)))
#endif
#if defined(_WIN32) && (defined(__i386__) || defined(__x86_64__))
CC_DECLARE(cc_vector,__attribute__((vectorcall)))
#endif
#endif
