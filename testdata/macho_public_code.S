#ifndef DYLIB_PUBLIC_KIND
#define DYLIB_PUBLIC_KIND regular
#endif
.section __TEXT,__public_code,DYLIB_PUBLIC_KIND,pure_instructions
.p2align 2
.globl _public_fn_a, _public_fn_b
.weak_definition _public_fn_a, _public_fn_b
_public_fn_a:
#if DYLIB_PUBLIC_FORM == 0
#ifdef DYLIB_PUBLIC_SHARED
.alt_entry _public_fn_b
#endif
_public_fn_b:
.alt_entry lpublic_entry
lpublic_entry:
#endif
#if defined(__arm64__)
nop
#else
.byte 0x0f,0x1f,0x40,0x00
#endif
#if DYLIB_PUBLIC_FORM == 1
.alt_entry _public_fn_b
_public_fn_b:
.alt_entry lpublic_entry
lpublic_entry:
#endif
#if defined(__arm64__)
add w0, w0, w1
add w0, w0, #7
ret
#else
leal 7(%rdi,%rsi), %eax
retq
#endif
.data
.p2align 3
.globl _public_fn_refs
_public_fn_refs:
.quad _public_fn_a, _public_fn_b, lpublic_entry
.subsections_via_symbols
