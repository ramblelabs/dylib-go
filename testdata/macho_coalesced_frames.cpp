#include "unwind_cxx_dwarf.h"

#ifdef DYLIB_FRAME_STRONG
extern "C" int coal_frame_drop(int a, int b) { return a + b; }
extern "C" int coal_frame_tail(int a, int b) { return a + b; }
extern "C" int coal_frame_deadonly(int a, int b) { return a + b; }
#ifdef DYLIB_FRAME_PUBLIC_ALIASES
extern "C" int coal_frame_keep(int a, int b) { return a + b; }
#endif
#else
extern "C" int coal_frame_missing(int, int), coal_frame_missing_tail(int, int);
extern "C" int cxx_host_throw(int, int);
extern "C" void cxx_event(int), cxx_save(void *);
#ifdef DYLIB_FRAME_PUBLIC_ALIASES
extern "C" int coal_frame_zkeep(int, int);
#endif
// The undefined key function keeps this RTTI external. Its only relocation
// belongs to a discarded function's LSDA, rather than to executable code.
struct DeadFrameType { virtual ~DeadFrameType(); };
struct FrameGuard {
    __attribute__((always_inline)) ~FrameGuard() { cxx_event(1); }
};
#ifdef DYLIB_FRAME_ORDINARY
#define COAL_FRAME __attribute__((weak, noinline))
#else
#define COAL_FRAME __attribute__((weak, noinline, section("__TEXT,__coal_frames,coalesced,pure_instructions")))
#endif
extern "C" COAL_FRAME int coal_frame_drop(int a, int b) {
    DYLIB_CXX_DWARF();
    try { return coal_frame_missing(a, b); }
    catch (DeadFrameType &) { return -1; }
}
extern "C" COAL_FRAME int coal_frame_keep(int a, int b) {
    DYLIB_CXX_DWARF();
#ifdef DYLIB_FRAME_PUBLIC_ALIASES
    cxx_save(reinterpret_cast<char *>(&coal_frame_zkeep) + 4);
#else
    cxx_save(reinterpret_cast<char *>(&coal_frame_keep) + 4);
#endif
    FrameGuard guard;
    return cxx_host_throw(a, b);
}
extern "C" COAL_FRAME int coal_frame_tail(int a, int b) {
    DYLIB_CXX_DWARF();
    volatile int value = coal_frame_missing_tail(a, b);
    return value;
}
#ifdef DYLIB_FRAME_PUBLIC_ALIASES
// The existing strong drop/tail names replace the storage owners. The keep
// name is replaced independently, while zkeep retains the original weak body.
__asm__(".globl _coal_frame_a_drop, _coal_frame_a_tail, _coal_frame_zkeep\n"
        ".weak_definition _coal_frame_a_drop, _coal_frame_a_tail, _coal_frame_zkeep\n"
        "_coal_frame_a_drop = _coal_frame_drop\n"
        "_coal_frame_a_tail = _coal_frame_tail\n"
        "_coal_frame_zkeep = _coal_frame_keep\n");
#endif
extern "C" int coal_frame_root(int a, int b) {
    DYLIB_CXX_DWARF();
    volatile int value;
#ifdef DYLIB_FRAME_PUBLIC_ALIASES
    try { value = coal_frame_zkeep(a, b); }
#else
    try { value = coal_frame_keep(a, b); }
#endif
    catch (int result) { value = result; }
    catch (double result) { value = int(result); }
    if (coal_frame_drop(a, b) != a + b || coal_frame_tail(a, b) != a + b) return -10;
    return value;
}
#ifdef DYLIB_FRAME_STANDALONE
extern "C" int cxx_events(int, int), cxx_has_frame(int, int);
int main() {
    int values[] = {20, -20};
    for (int a : values) {
        if (cxx_events(1, 0) != 0 || coal_frame_root(a, 42-a) != 42 ||
            cxx_events(1, 0) != 1 || cxx_has_frame(0, 0) != 1) return 1;
    }
    return 0;
}
#endif
#endif
