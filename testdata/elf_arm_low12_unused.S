.text
.p2align 2
.global low12_unused_invalid
.type low12_unused_invalid, %function
low12_unused_invalid:
    .reloc ., R_AARCH64_LDST8_ABS_LO12_NC, low12_8
    add x0, x0, #0
    ret
.size low12_unused_invalid, .-low12_unused_invalid
.section .note.GNU-stack,"",%progbits
