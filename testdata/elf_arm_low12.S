.text
.p2align 2

.macro scalar source, output, load, store, reg
    adrp x9, \source
    \load \reg, [x9, :lo12:\source]
    adrp x9, \output
    \store \reg, [x9, :lo12:\output]
    add w0, w0, w10
.endm

.macro vector source, output, kind, lane, reg
    adrp x9, \source
    ldr \kind, [x9, :lo12:\source]
    adrp x9, \output
    str \kind, [x9, :lo12:\output]
    umov \reg, \lane
    add w0, w0, w10
.endm

.global low12_eval
.type low12_eval, %function
low12_eval:
    add w0, w0, w1
    scalar low12_8, low12_out8, ldrb, strb, w10
    scalar low12_16, low12_out16, ldrh, strh, w10
    scalar low12_32, low12_out32, ldr, str, w10
    scalar low12_64, low12_out64, ldr, str, x10
    vector low12_128, low12_out128, q0, v0.s[0], w10
    adrp x9, low12_64
    prfm pldl1keep, [x9, :lo12:low12_64]
    ret
.size low12_eval, .-low12_eval

.global low12_simd_eval
.type low12_simd_eval, %function
low12_simd_eval:
    add w0, w0, w1
    vector low12_8, low12_out8, b0, v0.b[0], w10
    vector low12_16, low12_out16, h0, v0.h[0], w10
    vector low12_32, low12_out32, s0, v0.s[0], w10
    vector low12_64, low12_out64, d0, v0.d[0], x10
    vector low12_128, low12_out128, q0, v0.s[0], w10
    ret
.size low12_simd_eval, .-low12_simd_eval

.global low12_signed_eval
.type low12_signed_eval, %function
low12_signed_eval:
    add w0, w0, w1
    scalar low12_8, low12_out8, ldrsb, strb, w10
    scalar low12_16, low12_out16, ldrsh, strh, w10
    adrp x9, low12_32
    ldrsw x10, [x9, :lo12:low12_32]
    adrp x9, low12_out32
    str w10, [x9, :lo12:low12_out32]
    add w0, w0, w10
    scalar low12_64, low12_out64, ldr, str, x10
    vector low12_128, low12_out128, q0, v0.s[0], w10
    ret
.size low12_signed_eval, .-low12_signed_eval

.global low12_address_eval
.type low12_address_eval, %function
low12_address_eval:
    adrp x9, low12_32
    .reloc ., R_AARCH64_ADD_ABS_LO12_NC, low12_32
    adds x9, x9, #0
    ldr w10, [x9]
    add w0, w0, w1
    add w0, w0, w10
    ret
.size low12_address_eval, .-low12_address_eval

.global low12_got_eval
.type low12_got_eval, %function
low12_got_eval:
    adrp x9, :got:low12_32
    ldr x9, [x9, :got_lo12:low12_32]
    ldr w10, [x9]
    add w0, w0, w1
    add w0, w0, w10
    ret
.size low12_got_eval, .-low12_got_eval

.global low12_jump_eval
.type low12_jump_eval, %function
low12_jump_eval:
    b low12_eval
.size low12_jump_eval, .-low12_jump_eval

.global low12_call_eval
.type low12_call_eval, %function
low12_call_eval:
    stp x29, x30, [sp, #-16]!
    mov x29, sp
    bl low12_eval
    ldp x29, x30, [sp], #16
    ret
.size low12_call_eval, .-low12_call_eval
.section .note.GNU-stack,"",%progbits
