#include "unwind_cxx_dwarf.h"
typedef __UINTPTR_TYPE__ TerminalAddress;
#define TERM_STRING_(x) #x
#define TERM_STRING(x) TERM_STRING_(x)
extern "C" int cxx_host_throw(int,int);
extern "C" void cxx_event(int), cxx_save(void *);
extern "C" int term_frame_marker(int,int);
extern "C" void *term_frame_pointer;
struct TerminalGuard { ~TerminalGuard() { cxx_event(1); } };
extern "C" __attribute__((weak,noinline,section("__TEXT,__term_frame," TERM_STRING(DYLIB_TERM_KIND) ",pure_instructions")))
int term_frame_root(int a,int b) {
    DYLIB_CXX_DWARF();
    cxx_save(reinterpret_cast<char *>(&term_frame_root)+4);
    try { TerminalGuard guard; return cxx_host_throw(a,b); }
    catch(int result) { return result; }
    catch(double result) { return int(result); }
}
extern "C" int term_frame_check(int mode,int value) {
    TerminalAddress volatile marker=(TerminalAddress)&term_frame_marker;
    if((TerminalAddress)term_frame_pointer!=marker) return -1;
    if(term_frame_root(20,22)!=42 || term_frame_root(-20,62)!=42) return -2;
    if(mode && (term_frame_marker(20,22)!=42 || ((int(*)(int,int))term_frame_pointer)(20,22)!=42)) return -3;
    return value;
}
#ifdef DYLIB_TERM_STANDALONE
extern "C" int cxx_events(int,int), cxx_has_frame(int,int);
int main() {
    if(cxx_events(1,0)!=0 || term_frame_check(DYLIB_TERM_MODE,42)!=42 ||
       cxx_events(1,0)!=2 || cxx_has_frame(0,0)!=1) return 1;
    return 0;
}
#endif
