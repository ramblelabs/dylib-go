.text
.globl lazy_entry
lazy_entry:
movabsq $0x23456780, %r11
cmpq $0, (%r11)
je lazy_slow
// Force REX.W+B: LLVM otherwise omits W, which Windows' tail unwinder
// requires. The prefix extends the assembler's RBX memory operand to R11.
.byte 0x49
jmp *(%rbx)
.seh_proc lazy_slow
lazy_slow:
pushq %rbp
.seh_pushreg %rbp
subq $224, %rsp
.seh_stackalloc 224
leaq 224(%rsp), %rbp
.seh_setframe %rbp, 224
.seh_endprologue
movq %rax, 32(%rsp)
movq %rcx, 40(%rsp)
movq %rdx, 48(%rsp)
movq %r8, 56(%rsp)
movq %r9, 64(%rsp)
movq %rdi, 72(%rsp)
movq %rsi, 80(%rsp)
movdqu %xmm0, 96(%rsp)
movdqu %xmm1, 112(%rsp)
movdqu %xmm2, 128(%rsp)
movdqu %xmm3, 144(%rsp)
movdqu %xmm4, 160(%rsp)
movdqu %xmm5, 176(%rsp)
movdqu %xmm6, 192(%rsp)
movdqu %xmm7, 208(%rsp)
movabsq $0x34567898, %rcx
movabsq $0x456789a8, %rax
call *%rax
movdqu 96(%rsp), %xmm0
movdqu 112(%rsp), %xmm1
movdqu 128(%rsp), %xmm2
movdqu 144(%rsp), %xmm3
movdqu 160(%rsp), %xmm4
movdqu 176(%rsp), %xmm5
movdqu 192(%rsp), %xmm6
movdqu 208(%rsp), %xmm7
movq 32(%rsp), %rax
movq 40(%rsp), %rcx
movq 48(%rsp), %rdx
movq 56(%rsp), %r8
movq 64(%rsp), %r9
movq 72(%rsp), %rdi
movq 80(%rsp), %rsi
movabsq $0x23456780, %r11
addq $224, %rsp
popq %rbp
// Force REX.W+B: LLVM otherwise omits W, which Windows' tail unwinder
// requires. The prefix extends the assembler's RBX memory operand to R11.
.byte 0x49
jmp *(%rbx)
.seh_endproc
