#ifndef DYLIB_SHARED_KIND
#define DYLIB_SHARED_KIND regular
#endif
#ifndef DYLIB_SHARED_FORM
#define DYLIB_SHARED_FORM 0
#endif
.section __DATA,__shared,DYLIB_SHARED_KIND
.p2align 3
#if DYLIB_SHARED_FORM & 1
.globl _shared_before
.weak_definition _shared_before
_shared_before:
.alt_entry lshared_before
lshared_before:
.quad _shared_before_missing
#endif
.globl _shared_owner, _shared_zalias
#if DYLIB_SHARED_FORM < 2 || DYLIB_SHARED_FORM >= 4
.weak_definition _shared_owner
#endif
#if DYLIB_SHARED_FORM < 4
.weak_definition _shared_zalias
#endif
_shared_owner:
.alt_entry _shared_zalias
_shared_zalias:
.alt_entry lshared_start
lshared_start:
.quad 9
.alt_entry lshared_middle
lshared_middle:
.quad 10
#ifdef DYLIB_SHARED_POISON
.quad _shared_owner_missing
#else
.quad 11
#endif
.alt_entry lshared_end
lshared_end:
.data
.p2align 3
.globl _shared_refs, _shared_deltas
_shared_refs:
.quad _shared_owner, _shared_zalias, lshared_start, lshared_middle, lshared_end
#if DYLIB_SHARED_FORM & 1
.quad lshared_before
#else
.quad lshared_start
#endif
_shared_deltas:
.quad _shared_owner - _shared_refs
.quad _shared_refs - _shared_owner
.quad _shared_zalias - _shared_refs
.quad _shared_refs - _shared_zalias
.quad lshared_middle - _shared_refs
.quad _shared_refs - lshared_middle
.text
.p2align 2
.globl _shared_get_owner, _shared_get_alias, _shared_get_middle
_shared_get_owner:
#if defined(__arm64__)
adrp x0, _shared_owner@GOTPAGE
ldr x0, [x0, _shared_owner@GOTPAGEOFF]
ret
#else
movq _shared_owner@GOTPCREL(%rip), %rax
retq
#endif
_shared_get_alias:
#if defined(__arm64__)
adrp x0, _shared_zalias@GOTPAGE
ldr x0, [x0, _shared_zalias@GOTPAGEOFF]
ret
#else
movq _shared_zalias@GOTPCREL(%rip), %rax
retq
#endif
_shared_get_middle:
#if defined(__arm64__)
adrp x0, lshared_middle@GOTPAGE
ldr x0, [x0, lshared_middle@GOTPAGEOFF]
ret
#else
movq lshared_middle@GOTPCREL(%rip), %rax
retq
#endif
.subsections_via_symbols
