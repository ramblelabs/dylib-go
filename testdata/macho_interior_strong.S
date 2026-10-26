.data
.p2align 4
.globl _coal_alias_record
_coal_alias_record:
.quad 11,31
.text
.p2align 3
.globl _coal_alias_function
_coal_alias_function:
#if defined(__aarch64__)
add w0,w0,w1
ret
add w0,w0,w1
add w0,w0,#2
ret
#else
leal (%rdi,%rsi),%eax
retq
.p2align 3
leal 2(%rdi,%rsi),%eax
retq
#endif
.subsections_via_symbols
