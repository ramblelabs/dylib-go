#ifndef DYLIB_TERM_KIND
#define DYLIB_TERM_KIND regular
#endif
#if DYLIB_TERM_ZERO && !DYLIB_TERM_ZERO_CONTROL
.globl _term_owner, _term_marker
.weak_definition _term_owner, _term_marker
.zerofill __DATA,__bss,_term_owner,24,4
.zerofill __DATA,__bss,_term_marker,0,0
.alt_entry lterm_middle
lterm_middle = _term_owner + 8
.alt_entry lterm_end
lterm_end = _term_owner + 24
#else
#if DYLIB_TERM_CODE
.section __TEXT,__term,DYLIB_TERM_KIND,pure_instructions
#else
.section __DATA,__term,DYLIB_TERM_KIND
#endif
.p2align 3
.globl _term_owner, _term_marker
.weak_definition _term_owner
#if DYLIB_TERM_FORM < 3
.weak_definition _term_marker
#endif
_term_owner:
.alt_entry lterm_start
lterm_start:
#if DYLIB_TERM_CODE
#if defined(__arm64__)
nop
#else
.byte 0x0f,0x1f,0x40,0x00
#endif
.alt_entry lterm_middle
lterm_middle:
#if defined(__arm64__)
add w0,w0,w1
add w0,w0,#7
ret
#else
leal 7(%rdi,%rsi),%eax
retq
#endif
#else
#if DYLIB_TERM_ZERO_CONTROL
.quad 0
#else
.quad 9
#endif
.alt_entry lterm_middle
lterm_middle:
#if DYLIB_TERM_ZERO_CONTROL
.quad 0
#else
.quad 10
#endif
#ifdef DYLIB_TERM_POISON
.quad _term_missing
#else
#if DYLIB_TERM_ZERO_CONTROL
.quad 0
#else
.quad 11
#endif
#endif
#endif
_term_marker:
#if DYLIB_TERM_FORM == 1
.globl _term_zmarker
.weak_definition _term_zmarker
_term_zmarker:
#elif DYLIB_TERM_FORM == 2 || DYLIB_TERM_FORM == 3
.globl _term_alt
#if DYLIB_TERM_FORM == 3
.weak_definition _term_alt
#endif
.alt_entry _term_alt
_term_alt:
#endif
.alt_entry lterm_end
lterm_end:
#endif
.data
.p2align 3
.globl _term_refs, _term_deltas
_term_refs:
.quad _term_owner, _term_marker, lterm_end
#if DYLIB_TERM_FORM == 1
.quad _term_zmarker
#elif DYLIB_TERM_FORM == 2 || DYLIB_TERM_FORM == 3
.quad _term_alt
#else
.quad _term_marker
#endif
.quad lterm_middle
#if DYLIB_TERM_CODE && defined(__arm64__)
.quad _term_owner + 16
#elif DYLIB_TERM_CODE
.quad _term_owner + 9
#else
.quad _term_owner + 24
#endif
_term_deltas:
.quad lterm_end - _term_refs
.quad _term_refs - lterm_end
.quad _term_owner - _term_refs
.quad _term_refs - _term_owner
.text
.p2align 2
.globl _term_get_marker, _term_get_private
_term_get_marker:
#if defined(__arm64__)
adrp x0,_term_marker@GOTPAGE
ldr x0,[x0,_term_marker@GOTPAGEOFF]
ret
#else
movq _term_marker@GOTPCREL(%rip),%rax
retq
#endif
_term_get_private:
#if defined(__arm64__)
adrp x0,lterm_end@GOTPAGE
ldr x0,[x0,lterm_end@GOTPAGEOFF]
ret
#else
movq lterm_end@GOTPCREL(%rip),%rax
retq
#endif
.subsections_via_symbols
