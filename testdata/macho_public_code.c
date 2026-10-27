#include <stdint.h>
typedef int (*Function)(int, int);
extern int public_fn_a(int, int), public_fn_b(int, int);
extern Function public_fn_refs[3];
int public_fn_check(int mode, int value) {
    Function volatile pa = public_fn_a, pb = public_fn_b;
    int a_owns = DYLIB_PUBLIC_FORM;
#ifdef DYLIB_PUBLIC_SHARED
    a_owns = 1;
#endif
    Function owner = a_owns ? pa : pb;
    uintptr_t entry = (uintptr_t)owner + (DYLIB_PUBLIC_FORM ? 4 : 0);
    if (public_fn_refs[0] != pa || public_fn_refs[1] != pb ||
        (uintptr_t)public_fn_refs[2] != entry) return -1;
    int a = 49, b = 49, local = 49;
    if (mode & 1) {
        a = 84;
        if (a_owns) b = local = 84;
    }
    if (mode & 2) {
        b = 126;
        if (!a_owns) {
            local = 126;
            if (!(mode & 1)) a = 126;
        }
    }
    if (public_fn_a(20,22) != a || public_fn_b(20,22) != b ||
        public_fn_refs[0](20,22) != a || public_fn_refs[1](20,22) != b ||
        public_fn_refs[2](20,22) != local) return -2;
    return value;
}
#ifdef DYLIB_PUBLIC_STANDALONE
int main(void) {
    int result = public_fn_check(DYLIB_PUBLIC_OVERRIDE, 42);
    return result == 42 ? 0 : -result;
}
#endif
