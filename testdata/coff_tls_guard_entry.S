.text
#if defined(__aarch64__)
movz x1, #0x5678
movk x1, #0x1234, lsl #16
movk x1, #0, lsl #32
movk x1, #0, lsl #48
ldr w1, [x1]
ldr x0, [x18, #0x58]
ldr x0, [x0, x1, lsl #3]
movz w1, #0x4321
movk w1, #0x12, lsl #16
add x0, x0, x1
ret
#elif defined(__x86_64__)
movabsq $0x12345678, %rcx
movl (%rcx), %ecx
movq %gs:0x58, %rax
movq (%rax,%rcx,8), %rax
addq $0x124321, %rax
retq
#else
movl $0x12345678, %ecx
movl (%ecx), %ecx
movl %fs:0x2c, %eax
movl (%eax,%ecx,4), %eax
addl $0x124321, %eax
retl
#endif
