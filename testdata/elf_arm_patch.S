// ABS32 is changed to PATCHINST by the test fixture builder, so older Clang
// versions can produce the input without knowing the new relocation name.
.text
.p2align 2
#ifdef PATCH_WEAK
.weak patch_word, patch_plus, patch_last
#endif
.global patch_eval, patch_addend, patch_call, patch_sequence
.type patch_eval,%function
patch_eval:
.reloc ., R_AARCH64_ABS32, patch_word
add w0, w0, w1
ret
.type patch_addend,%function
patch_addend:
.reloc ., R_AARCH64_ABS32, patch_plus-4
add w0, w0, w1
ret
.type patch_call,%function
patch_call:
stp x29, x30, [sp, #-16]!
bl patch_helper
.reloc .-4, R_AARCH64_ABS32, patch_word
ldp x29, x30, [sp], #16
ret
.type patch_sequence,%function
patch_sequence:
.reloc ., R_AARCH64_ABS32, patch_word
.reloc ., R_AARCH64_ABS32, patch_last
add w0, w0, w1
ret
.section .note.GNU-stack,"",%progbits
