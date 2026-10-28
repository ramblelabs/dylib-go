#include "conventions.h"

#if defined(_WIN32)
int _fltused;
#endif
void *cc_state(void) { static int32_t state=20; return &state; }

#define CC_DEFINE(prefix,cc) \
int32_t cc prefix##_scalar(void *receiver,int32_t a,double b) { return *(int32_t *)receiver+a+(int32_t)b; } \
void *cc prefix##_pointer(void *receiver,void *value) { return value ? value : receiver; } \
CCPair cc prefix##_pair(void *receiver,CCPair value,int32_t delta) { CCPair result={.a=value.a+delta,.b=value.b+*(int32_t *)receiver}; return result; } \
CCLarge cc prefix##_large(void *receiver,CCLarge value,int32_t delta) { CCLarge result={{value.values[0]+*(int32_t *)receiver,value.values[1]+delta,value.values[2]+delta,value.values[3]+delta,value.values[4]+delta},value.marker+1}; return result; } \
CCHFA cc prefix##_hfa(void *receiver,CCHFA value,float delta) { (void)receiver; CCHFA result={value.a+delta,value.b+delta,value.c+delta,value.d+delta}; return result; } \
double cc prefix##_spill(void *receiver,int32_t a,int32_t b,int32_t c,int32_t d,int32_t e,int32_t f,int32_t g,int32_t h,double i,double j,double k,double l,double m,double n,double o,double p,double q,double r,CCPair value) { return *(int32_t *)receiver+a+b+c+d+e+f+g+h+i+j+k+l+m+n+o+p+q+r+value.a+value.b; } \
prefix##_Entry prefix##_factory(void) { return prefix##_pair; } \
CCPair prefix##_call(prefix##_Entry entry,void *receiver,CCPair value,int32_t delta) { return entry(receiver,value,delta); } \
CCPair cc prefix##_forward(void *receiver,prefix##_Entry entry,CCPair value,int32_t delta) { return entry(receiver,value,delta); } \
prefix##_HFAEntry prefix##_hfa_factory(void) { return prefix##_hfa; } \
CCHFA prefix##_hfa_call(prefix##_HFAEntry entry,void *receiver,CCHFA value,float delta) { return entry(receiver,value,delta); } \
int32_t prefix##_control(void) { \
    int32_t state=20; CCPair input={.a=20,.b=22}; CCLarge large={{1,2,3,4,5},6}; CCHFA hfa={1,2,3,4}; \
    CCPair result=prefix##_forward(&state,prefix##_factory(),input,2); \
    CCLarge output=prefix##_large(&state,large,2); \
    CCHFA floats=prefix##_hfa_call(prefix##_hfa_factory(),&state,hfa,2); \
    return prefix##_scalar(&state,20,2.5)==42 && prefix##_pointer(&state,0)==&state && \
        result.a==22 && result.b==42 && output.values[0]==21 && output.values[4]==7 && output.marker==7 && \
        floats.a==3 && floats.d==6 && prefix##_spill(&state,1,2,3,4,5,6,7,8,1,2,3,4,5,6,7,8,9,10,input)==153 ? 42 : -1; \
}

CC_DEFINE(cc_cdecl,)
#if defined(__x86_64__)
CC_DEFINE(cc_ms,__attribute__((ms_abi)))
CC_DEFINE(cc_sysv,__attribute__((sysv_abi)))
#endif
#if defined(_WIN32) && defined(__i386__)
CC_DEFINE(cc_std,__attribute__((stdcall)))
CC_DEFINE(cc_fast,__attribute__((fastcall)))
CC_DEFINE(cc_this,__attribute__((thiscall)))
#endif
#if defined(_WIN32) && (defined(__i386__) || defined(__x86_64__))
CC_DEFINE(cc_vector,__attribute__((vectorcall)))
#endif
