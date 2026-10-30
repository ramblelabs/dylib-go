/* IMAGE_BOUND_IMPORT_DESCRIPTOR and IMAGE_BOUND_FORWARDER_REF wire records.
 * Name offsets are relative to bound_start, never to a section or image base.
 * The same assembler output is linked into the independent PE image control.
 */
.section .bound,"dr"
.globl bound_start, bound_end
bound_start:
.long 1
.short first_name-bound_start,2
.long 2
.short kernel_name-bound_start,0
.long 3
.short second_name-bound_start,0
.long 0xffffffff
.short second_name-bound_start,0
.zero 8
first_name: .asciz "pe-first.dll"
kernel_name: .asciz "kernel32.dll"
second_name: .asciz "pe-second.dll"
bound_end:
