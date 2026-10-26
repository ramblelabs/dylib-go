#ifdef DYLIB_ZERO_STRONG
long ordinary_zero[2] = {11, 31};
#else
extern long ordinary_zero[2];
#ifndef DYLIB_ZERO_ALIASLESS
extern long *ordinary_zero_pointers[3];
#endif
#ifndef DYLIB_ZERO_WHOLE
extern long ordinary_before_zero;
#endif
int ordinary_zero_check(int a, int b) {
#ifdef DYLIB_ZERO_SELECTED
    if (ordinary_zero[0] != 11 || ordinary_zero[1] != 31) return -1;
#else
    if (ordinary_zero[0] != 0 || ordinary_zero[1] != 0) return -1;
#endif
#ifndef DYLIB_ZERO_ALIASLESS
    if (ordinary_zero_pointers[0] != ordinary_zero ||
        ordinary_zero_pointers[1] != ordinary_zero+1 ||
        ordinary_zero_pointers[2] != ordinary_zero+2) return -2;
#endif
#ifndef DYLIB_ZERO_WHOLE
    if (ordinary_before_zero != 0) return -3;
    ordinary_before_zero = 17;
    if (ordinary_before_zero != 17) return -4;
    ordinary_before_zero = 0;
#endif
    return a + b;
}
#ifdef DYLIB_ZERO_STANDALONE
int main(void) { return ordinary_zero_check(20,22) == 42 ? 0 : 1; }
#endif
#endif
