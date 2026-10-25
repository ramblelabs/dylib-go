.section __DATA,__coal,coalesced
.p2align 3
.globl _coal_value
.weak_definition _coal_value
_coal_value:
#ifdef DYLIB_COAL_VALUE
.quad DYLIB_COAL_VALUE
#else
.quad _coal_missing
#endif
lcoal_private:
.quad 17
.globl _coal_keep
.weak_definition _coal_keep
_coal_keep:
.quad 13
.globl _coal_record
.weak_definition _coal_record
.p2align 4
_coal_record:
#ifdef DYLIB_COAL_VALUE
.quad DYLIB_COAL_VALUE
#else
.quad 7
#endif
.alt_entry lcoal_middle
lcoal_middle:
Lcoal_middle:
.quad 9
#ifndef DYLIB_COAL_NO_NAMED_END
.alt_entry lcoal_end
lcoal_end:
#endif
Lcoal_end:

.section __DATA,__coal_drop,coalesced
.p2align 3
.globl _coal_drop
.weak_definition _coal_drop
_coal_drop:
#ifdef DYLIB_COAL_VALUE
.quad 5
#else
.quad _coal_missing_drop
#endif

.data
.p2align 4
#ifndef DYLIB_COAL_ANCHOR
#define DYLIB_COAL_ANCHOR _coal_anchor
#endif
.globl DYLIB_COAL_ANCHOR
DYLIB_COAL_ANCHOR:
.quad _coal_value
.quad _coal_keep
.quad _coal_record
.quad lcoal_middle
.quad Lcoal_middle
.quad Lcoal_end
.quad lcoal_private
.quad _coal_drop
#ifndef DYLIB_COAL_NO_NAMED_END
.quad lcoal_end
#endif
.globl _coal_delta, _coal_reverse
.weak_definition _coal_delta, _coal_reverse
_coal_delta:
.quad lcoal_middle - _coal_drop
_coal_reverse:
.quad _coal_drop - lcoal_middle

#if defined(__x86_64__)
.text
.globl _coal_local_read, _coal_compare1, _coal_compare2, _coal_compare4
.weak_definition _coal_local_read, _coal_compare1, _coal_compare2, _coal_compare4
_coal_local_read:
movl Lcoal_middle(%rip), %eax
retq
_coal_compare1:
#ifdef DYLIB_COAL_VALUE
cmpl $9, Lcoal_middle(%rip)
#else
cmpl $31, Lcoal_middle(%rip)
#endif
sete %al
movzbl %al, %eax
retq
_coal_compare2:
cmpw $0x1234, Lcoal_middle(%rip)
sete %al
movzbl %al, %eax
retq
_coal_compare4:
cmpl $0x12345678, Lcoal_middle(%rip)
sete %al
movzbl %al, %eax
retq
#endif

.section __TEXT,__coal_code,coalesced,pure_instructions
.p2align 2
.globl _coal_func
.weak_definition _coal_func
_coal_func:
#ifdef DYLIB_COAL_CFI
.cfi_startproc
#endif
#ifdef DYLIB_COAL_VALUE
#if defined(__arm64__)
add w0, w0, w1
add w0, w0, #DYLIB_COAL_VALUE
ret
#else
leal DYLIB_COAL_VALUE(%rdi,%rsi), %eax
retq
#endif
#else
#if defined(__arm64__)
b _coal_missing_func
#else
jmp _coal_missing_func
#endif
#endif
#ifdef DYLIB_COAL_CFI
.cfi_endproc
#endif
.p2align 2
.globl _coal_keep_func
.weak_definition _coal_keep_func
_coal_keep_func:
#if defined(__arm64__)
add w0, w0, w1
add w0, w0, #1
ret
#else
leal 1(%rdi,%rsi), %eax
retq
#endif
#ifdef DYLIB_COAL_LIFECYCLE
.p2align 2
.globl _coal_init, _coal_fini
.weak_definition _coal_init, _coal_fini
_coal_init:
#if defined(__arm64__)
b _coal_missing_event
#else
jmp _coal_missing_event
#endif
_coal_fini:
#if defined(__arm64__)
b _coal_missing_event
#else
jmp _coal_missing_event
#endif
.section __DATA,__mod_init_func,mod_init_funcs
.p2align 3
.quad _coal_init
.section __DATA,__mod_term_func,mod_term_funcs
.p2align 3
.quad _coal_fini
#endif
.subsections_via_symbols
