#ifndef DYLIB_PUBLIC_KIND
#define DYLIB_PUBLIC_KIND regular
#endif
#ifndef DYLIB_PUBLIC_FORM
#define DYLIB_PUBLIC_FORM 0
#endif
.section __DATA,__public,DYLIB_PUBLIC_KIND
.p2align 4
.globl _public_a, _public_b
#if DYLIB_PUBLIC_FORM != 2
.weak_definition _public_a
#endif
#if DYLIB_PUBLIC_FORM != 1 && DYLIB_PUBLIC_FORM != 4
.weak_definition _public_b
#endif
_public_a:
#if DYLIB_PUBLIC_FORM < 3
_public_b:
#endif
.alt_entry lpublic_start
lpublic_start:
#if DYLIB_PUBLIC_FORM < 3
.quad 9
#else
.quad 7
#endif
#if DYLIB_PUBLIC_FORM == 3 || DYLIB_PUBLIC_FORM == 4
.alt_entry _public_b
_public_b:
#endif
.alt_entry lpublic_middle
lpublic_middle:
#if DYLIB_PUBLIC_FORM < 3
.quad 11
#else
.quad 9
#endif
#ifdef DYLIB_PUBLIC_POISON
.quad _public_missing
#else
.quad 13
#endif
.alt_entry lpublic_end
lpublic_end:
#if DYLIB_PUBLIC_FORM == 5
.alt_entry _public_b
_public_b:
#endif
.data
.p2align 3
.globl _public_refs, _public_deltas
_public_refs:
.quad _public_a, _public_b, lpublic_start, lpublic_middle, lpublic_end
_public_deltas:
.quad _public_a - _public_refs
.quad _public_refs - _public_a
.quad _public_b - _public_refs
.quad _public_refs - _public_b
.quad lpublic_middle - _public_refs
.quad _public_refs - lpublic_middle
.text
.p2align 2
.globl _public_get_a, _public_get_b
_public_get_a:
#if defined(__arm64__)
adrp x0, _public_a@GOTPAGE
ldr x0, [x0, _public_a@GOTPAGEOFF]
ret
#else
movq _public_a@GOTPCREL(%rip), %rax
retq
#endif
_public_get_b:
#if defined(__arm64__)
adrp x0, _public_b@GOTPAGE
ldr x0, [x0, _public_b@GOTPAGEOFF]
ret
#else
movq _public_b@GOTPCREL(%rip), %rax
retq
#endif
.subsections_via_symbols
