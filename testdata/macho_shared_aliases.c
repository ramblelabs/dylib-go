#include <stdint.h>
extern long shared_owner[], shared_zalias[], *shared_refs[6];
extern uintptr_t shared_deltas[6];
extern long *shared_get_owner(void), *shared_get_alias(void), *shared_get_middle(void);
extern long shared_alias_ref[];
__asm__(".globl _shared_alias_ref\n_shared_alias_ref = _shared_zalias\n");
int shared_check(int mode, int value) {
    long *volatile owner = shared_owner, *volatile alias = shared_zalias;
    long *volatile forwarded = shared_alias_ref;
    uintptr_t middle = (uintptr_t)owner + sizeof(long);
    uintptr_t end = (uintptr_t)owner + 3*sizeof(long);
    if (shared_refs[0] != owner || shared_refs[1] != alias ||
        shared_refs[2] != owner || (uintptr_t)shared_refs[3] != middle ||
        (uintptr_t)shared_refs[4] != end || shared_get_owner() != owner ||
        shared_get_alias() != alias || (uintptr_t)shared_get_middle() != middle ||
        forwarded != alias) return -1;
    uintptr_t anchor = (uintptr_t)shared_refs;
    uintptr_t targets[3] = {(uintptr_t)owner, (uintptr_t)alias, middle};
    for (int i = 0; i < 3; ++i)
        if (shared_deltas[2*i] != targets[i]-anchor ||
            shared_deltas[2*i+1] != anchor-targets[i]) return -2;
    long a = mode & 1 ? 42 : 9;
    long b = mode & 2 ? 84 : a;
    if (*owner != a || *alias != b || owner[1] != (mode & 1 ? 43 : 10)) return -3;
#if DYLIB_SHARED_FORM & 1
    if (*shared_refs[5] != 99) return -4;
#else
    if (shared_refs[5] != owner) return -4;
#endif
    return value;
}
#ifdef DYLIB_SHARED_LIFECYCLE
extern void record_event(int);
__attribute__((constructor)) static void initialize(void) {
    record_event(shared_check(1,42) == 42 ? 2 : -2);
}
__attribute__((destructor)) static void finalize(void) {
    record_event(shared_check(1,42) == 42 ? 3 : -3);
}
#endif
#ifdef DYLIB_SHARED_STANDALONE
int main(void) { return shared_check(DYLIB_SHARED_OVERRIDE,42) == 42 ? 0 : 1; }
#endif
