.global patch_word, patch_plus
.hidden patch_word, patch_plus
.set patch_word, 0xd503201f
.set patch_plus, 0xd5032023
#ifndef PATCH_PARTIAL
.global patch_last
.hidden patch_last
.set patch_last, 0x4b010000
#endif
.section .note.GNU-stack,"",%progbits
