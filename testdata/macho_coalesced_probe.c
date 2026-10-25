extern long coal_value, coal_keep, coal_record[2], coal_drop;
#ifdef DYLIB_COAL_NO_NAMED_END
#define DYLIB_COAL_ANCHORS 8
#else
#define DYLIB_COAL_ANCHORS 9
#endif
extern long *coal_anchor[DYLIB_COAL_ANCHORS];
extern long coal_delta, coal_reverse;
extern int coal_func(int, int), coal_keep_func(int, int);
#if defined(__x86_64__)
extern int coal_local_read(void), coal_compare1(void), coal_compare2(void), coal_compare4(void);
#endif
#ifndef DYLIB_COAL_VALUE
#define DYLIB_COAL_EXPECTED 42
#define DYLIB_COAL_RECORD 11
#define DYLIB_COAL_MIDDLE 31
#define DYLIB_COAL_DELTA 0
#else
#define DYLIB_COAL_EXPECTED DYLIB_COAL_VALUE
#define DYLIB_COAL_RECORD DYLIB_COAL_VALUE
#define DYLIB_COAL_MIDDLE 9
#define DYLIB_COAL_DELTA DYLIB_COAL_VALUE
#endif
static int check_anchor(long **anchor) {
    return anchor[0] == &coal_value && anchor[1] == &coal_keep &&
        anchor[2] == coal_record && anchor[3] == coal_record+1 &&
        anchor[4] == coal_record+1 && anchor[5] == coal_record+2 &&
        *anchor[6] == 17 && anchor[7] == &coal_drop
#ifndef DYLIB_COAL_NO_NAMED_END
        && anchor[8] == coal_record+2
#endif
        ;
}
#ifdef DYLIB_COAL_TWO_ANCHORS
extern long *coal_anchor_second[DYLIB_COAL_ANCHORS];
#endif
int coal_check(int a, int b) {
    if (coal_value != DYLIB_COAL_EXPECTED || coal_keep != 13 ||
        coal_record[0] != DYLIB_COAL_RECORD || coal_record[1] != DYLIB_COAL_MIDDLE ||
        coal_drop != 5 || !check_anchor(coal_anchor) ||
        coal_func(a,b) != a+b+DYLIB_COAL_DELTA ||
        coal_keep_func(a,b) != a+b+1) return -1;
    if ((__UINTPTR_TYPE__)coal_delta != (__UINTPTR_TYPE__)(coal_record+1)-(__UINTPTR_TYPE__)&coal_drop ||
        (__UINTPTR_TYPE__)coal_reverse != (__UINTPTR_TYPE__)&coal_drop-(__UINTPTR_TYPE__)(coal_record+1)) return -4;
#ifdef DYLIB_COAL_TWO_ANCHORS
    if (!check_anchor(coal_anchor_second)) return -2;
#endif
#if defined(__x86_64__)
    if (coal_local_read() != DYLIB_COAL_MIDDLE || coal_compare1() != 1 ||
        coal_compare2() || coal_compare4()) return -3;
#endif
    return a+b;
}
#ifdef DYLIB_COAL_STANDALONE
int main(void) { return coal_check(20,22) == 42 ? 0 : 1; }
#endif
