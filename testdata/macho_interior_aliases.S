#ifndef DYLIB_COAL_KIND
#define DYLIB_COAL_KIND coalesced
#endif
.section __DATA,__coal_alias,DYLIB_COAL_KIND
.p2align 4
.globl _coal_alias_record
.weak_definition _coal_alias_record
_coal_alias_record:
.quad 7
.alt_entry lalias_mid
lalias_mid:
.quad 9
.alt_entry lalias_end
lalias_end:

.section __TEXT,__coal_code,DYLIB_COAL_KIND,pure_instructions
.p2align 3
.globl _coal_alias_function
.weak_definition _coal_alias_function
_coal_alias_function:
#if defined(__aarch64__)
add w0,w0,w1
ret
#else
leal (%rdi,%rsi),%eax
retq
.p2align 3
#endif
.alt_entry lalias_function
lalias_function:
#if defined(__aarch64__)
add w0,w0,w1
add w0,w0,#1
ret
#else
leal 1(%rdi,%rsi),%eax
retq
#endif

.text
.p2align 2
.globl _alias_got_mid, _alias_got_end, _alias_got_function
_alias_got_mid:
#if defined(__aarch64__)
adrp x0,lalias_mid@GOTPAGE
ldr x0,[x0,lalias_mid@GOTPAGEOFF]
ret
#else
movq lalias_mid@GOTPCREL(%rip),%rax
retq
#endif
_alias_got_end:
#if defined(__aarch64__)
adrp x0,lalias_end@GOTPAGE
ldr x0,[x0,lalias_end@GOTPAGEOFF]
ret
#else
movq lalias_end@GOTPCREL(%rip),%rax
retq
#endif
_alias_got_function:
#if defined(__aarch64__)
adrp x0,lalias_function@GOTPAGE
ldr x0,[x0,lalias_function@GOTPAGEOFF]
ret
#else
movq lalias_function@GOTPCREL(%rip),%rax
retq
#endif

#if defined(__x86_64__)
.globl _alias_got_slot
_alias_got_slot:
leaq lalias_mid@GOTPCREL(%rip),%rax
movq (%rax),%rax
retq
#else
.section __DATA,__got_relative
.p2align 2
.globl _alias_got_relative
_alias_got_relative:
.long lalias_mid@GOT - .
#endif

#ifdef DYLIB_ALIAS_CONTROL
.data
#else
.section __DATA,__nl_symbol_ptr,non_lazy_symbol_pointers
#endif
.p2align 4
.globl _alias_indirect
_alias_indirect:
#ifndef DYLIB_ALIAS_CONTROL
.indirect_symbol lalias_mid
#endif
.quad lalias_mid
#ifndef DYLIB_ALIAS_CONTROL
.indirect_symbol lalias_end
#endif
.quad lalias_end
#ifndef DYLIB_ALIAS_CONTROL
.indirect_symbol lalias_function
#endif
.quad lalias_function

#ifdef DYLIB_ALIAS_CONTROL
.text
.p2align 2
#elif defined(__aarch64__)
.section __TEXT,__stubs,symbol_stubs,pure_instructions,12
.p2align 2
#else
.section __TEXT,__stubs,symbol_stubs,pure_instructions,6
#endif
.globl _alias_stub
_alias_stub:
#ifndef DYLIB_ALIAS_CONTROL
.indirect_symbol lalias_function
#endif
#if defined(__aarch64__)
#ifdef DYLIB_ALIAS_CONTROL
adrp x16,lalias_function@GOTPAGE
ldr x16,[x16,lalias_function@GOTPAGEOFF]
#else
adrp x16,_alias_indirect@PAGE
ldr x16,[x16,_alias_indirect@PAGEOFF+16]
#endif
br x16
#else
#ifdef DYLIB_ALIAS_CONTROL
jmpq *lalias_function@GOTPCREL(%rip)
#else
jmpq *_alias_indirect+16(%rip)
#endif
#endif
.subsections_via_symbols
