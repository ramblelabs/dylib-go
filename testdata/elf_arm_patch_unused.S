.text
.p2align 2
.global patch_unused_invalid
.type patch_unused_invalid,%function
patch_unused_invalid:
.reloc ., R_AARCH64_ABS32, patch_word
.reloc ., R_AARCH64_CALL26, patch_helper
.inst 0x94000000
ret
.section .note.GNU-stack,"",%progbits
