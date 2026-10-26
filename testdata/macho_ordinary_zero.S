#ifndef DYLIB_ZERO_WHOLE
.globl _ordinary_before_zero
.zerofill __DATA,__bss,_ordinary_before_zero,8,3
#endif
.globl _ordinary_zero
.weak_definition _ordinary_zero
.zerofill __DATA,__bss,_ordinary_zero,16,4
#ifndef DYLIB_ZERO_ALIASLESS
.alt_entry lzero_middle
lzero_middle = _ordinary_zero + 8
.alt_entry lzero_end
lzero_end = _ordinary_zero + 16
.data
.p2align 4
.globl _ordinary_zero_pointers
_ordinary_zero_pointers:
.quad _ordinary_zero, lzero_middle, lzero_end
#endif
.subsections_via_symbols
