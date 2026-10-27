#include <stdint.h>
#ifndef DYLIB_TERM_MODE
#define DYLIB_TERM_MODE 0
#endif
#if DYLIB_TERM_CODE
extern int term_owner(int,int), term_marker(int,int);
typedef int (*TermFunction)(int,int);
#define TERM_VALUE(p) (((TermFunction)(p))(20,22))
#if defined(__arm64__)
#define TERM_SIZE 16
#else
#define TERM_SIZE 9
#endif
#define TERM_MIDDLE 4
#define TERM_ORIGINAL 49
#define TERM_ADD 42
#else
extern long term_owner[], term_marker[];
#define TERM_VALUE(p) (*(long *)(p))
#define TERM_SIZE 24
#define TERM_MIDDLE 8
#if DYLIB_TERM_ZERO
#define TERM_ORIGINAL 0
#else
#define TERM_ORIGINAL 9
#endif
#define TERM_ADD 0
#endif
extern uintptr_t term_refs[6], term_deltas[4];
extern void *term_get_marker(void), *term_get_private(void);
extern char term_alias[];
__asm__(".globl _term_alias\n_term_alias = _term_marker\n");
#if DYLIB_TERM_MODE & 1
extern char term_owner_storage[];
#endif
#if DYLIB_TERM_MODE & 2
extern char term_marker_storage[];
#endif
#if DYLIB_TERM_MODE & 4
#if DYLIB_TERM_FORM == 1
extern char term_zmarker_storage[];
#else
extern char term_alt_storage[];
#endif
#endif
int term_check(int mode,int value) {
    uintptr_t volatile owner=(uintptr_t)term_owner, marker=(uintptr_t)term_marker;
    uintptr_t end=term_refs[2], extra=term_refs[3], canonical=marker;
#if DYLIB_TERM_FORM == 1
    extern char term_zmarker[];
    canonical=(uintptr_t)term_zmarker;
    if (extra!=canonical || (!(mode&2) && marker!=canonical)) return -1;
#elif DYLIB_TERM_FORM == 2 || DYLIB_TERM_FORM == 3
    extern char term_alt[];
    if (extra!=(uintptr_t)term_alt || (!(mode&4) && extra!=marker)) return -1;
#else
    if (extra!=marker) return -1;
#endif
    if (mode!=DYLIB_TERM_MODE || term_refs[0]!=owner || term_refs[1]!=marker ||
        end!=canonical || term_refs[4]!=owner+TERM_MIDDLE ||
        term_refs[5]!=owner+TERM_SIZE || (uintptr_t)term_get_marker()!=marker ||
        (uintptr_t)term_get_private()!=canonical || (uintptr_t)term_alias!=marker) return -2;
    uintptr_t anchor=(uintptr_t)term_refs;
    if(term_deltas[0]!=end-anchor || term_deltas[1]!=anchor-end ||
       term_deltas[2]!=owner-anchor || term_deltas[3]!=anchor-owner) return -3;
    if(TERM_VALUE(owner)!=(mode&1?42+TERM_ADD:TERM_ORIGINAL)) return -4;
#if DYLIB_TERM_MODE & 1
    if(owner!=(uintptr_t)term_owner_storage) return -5;
#endif
#if DYLIB_TERM_MODE & 2
    if(marker!=(uintptr_t)term_marker_storage || TERM_VALUE(marker)!=84+TERM_ADD) return -6;
#if DYLIB_TERM_FORM != 1
    if(TERM_VALUE(end)!=84+TERM_ADD) return -6;
#endif
#endif
#if DYLIB_TERM_MODE & 4
#if DYLIB_TERM_FORM == 1
    if(canonical!=(uintptr_t)term_zmarker_storage || TERM_VALUE(end)!=126+TERM_ADD) return -7;
#else
    if(extra!=(uintptr_t)term_alt_storage || TERM_VALUE(extra)!=126+TERM_ADD) return -7;
#endif
#endif
    return value;
}
#ifdef DYLIB_TERM_LIFECYCLE
extern void record_event(int);
__attribute__((constructor)) static void initialize(void) { record_event(term_check(DYLIB_TERM_MODE,42)==42?2:-2); }
__attribute__((destructor)) static void finalize(void) { record_event(term_check(DYLIB_TERM_MODE,42)==42?3:-3); }
#endif
#ifdef DYLIB_TERM_STANDALONE
int main(void) { return term_check(DYLIB_TERM_MODE,42)==42?0:1; }
#endif
