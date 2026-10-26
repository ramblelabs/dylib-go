.section __TEXT,__coal_dead,coalesced,pure_instructions
.p2align 2
.globl _coal_frame_deadonly
.weak_definition _coal_frame_deadonly
_coal_frame_deadonly:
.cfi_startproc
.cfi_personality 0x9b, _coal_frame_missing_personality
// Force DWARF even on ARM64; this personality must never be registered.
.cfi_escape 0x00
#if defined(__aarch64__)
b _coal_frame_missing_deadonly
#else
jmp _coal_frame_missing_deadonly
#endif
.cfi_endproc
.subsections_via_symbols
