#include <stdint.h>
#ifndef DYLIB_PUBLIC_FORM
#define DYLIB_PUBLIC_FORM 0
#endif
extern long public_a[], public_b[], *public_refs[5];
extern uintptr_t public_deltas[6];
extern long *public_get_a(void), *public_get_b(void);
// Also exercise N_INDR followed by a public alias's interior offset.
extern long public_b_ref[];
__asm__(".globl _public_b_ref\n_public_b_ref = _public_b\n");
int public_check(int mode, int value) {
    long *volatile pa = public_a, *volatile pb = public_b;
    long *volatile pr = public_b_ref;
#if DYLIB_PUBLIC_FORM < 2
    long *owner = pb;
#else
    long *owner = pa;
#endif
    uintptr_t middle = (uintptr_t)owner + sizeof(long);
    uintptr_t end = (uintptr_t)owner + 3*sizeof(long);
    if (public_refs[0] != pa || public_refs[1] != pb ||
        public_refs[2] != owner || (uintptr_t)public_refs[3] != middle ||
        (uintptr_t)public_refs[4] != end || public_get_a() != pa || public_get_b() != pb || pr != pb)
        return -1;
    uintptr_t anchor = (uintptr_t)public_refs;
    uintptr_t targets[3] = {(uintptr_t)pa, (uintptr_t)pb, middle};
    for (int i = 0; i < 3; ++i)
        if (public_deltas[2*i] != targets[i] - anchor ||
            public_deltas[2*i+1] != anchor - targets[i]) return -2;
    long a = DYLIB_PUBLIC_FORM < 3 ? 9 : 7;
    long b = 9;
    if (mode & 1) {
        a = 42;
#if DYLIB_PUBLIC_FORM >= 3
        b = 43;
#endif
    }
    if (mode & 2) {
        b = 84;
#if DYLIB_PUBLIC_FORM == 0
        if (!(mode & 1)) a = 84;
#endif
    }
    if (*pa != a) return -3;
#if DYLIB_PUBLIC_FORM == 5
    if (!(mode & 2) && (uintptr_t)pb != end) return -4;
#else
    if (*pb != b) return -4;
#endif
    return value;
}
#ifdef DYLIB_PUBLIC_LIFECYCLE
extern void record_event(int);
__attribute__((constructor)) static void initialize(void) {
    record_event(public_check(2, 42) == 42 ? 2 : -2);
}
__attribute__((destructor)) static void finalize(void) {
    record_event(public_check(2, 42) == 42 ? 3 : -3);
}
#endif
#ifdef DYLIB_PUBLIC_STANDALONE
int main(void) {
    int result = public_check(DYLIB_PUBLIC_OVERRIDE, 42);
    return result == 42 ? 0 : -result;
}
#endif
