#include <stdint.h>

__declspec(dllexport) uintptr_t detach_entry, detach_observed;
int add(int a,int b) { return a+b; }
int second(int a,int b) { return a+b; }
// No CRT or SDK imports: only a real loader detach notification and a call
// back into the raw image while this DLL's final owned reference is released.
int __stdcall DllMain(void *module,unsigned reason,void *reserved) {
    (void)module;(void)reserved;
    if(reason==0 && detach_entry && detach_observed)
        *(int32_t *)detach_observed=((int32_t (*)(int32_t,int32_t))detach_entry)(20,22);
    return 1;
}
