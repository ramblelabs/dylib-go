.text
.globl lazy_entry
lazy_entry:
movz x16,#0x6780
movk x16,#0x2345,lsl #16
movk x16,#0,lsl #32
movk x16,#0,lsl #48
ldar x17,[x16]
cbz x17,lazy_slow
br x17
.seh_proc lazy_slow
lazy_slow:
sub sp,sp,#224
.seh_stackalloc 224
stp x29,x30,[sp,#80]
.seh_save_fplr 80
add x29,sp,#80
.seh_add_fp 80
.seh_endprologue
stp x0,x1,[sp,#0]
stp x2,x3,[sp,#16]
stp x4,x5,[sp,#32]
stp x6,x7,[sp,#48]
str x8,[sp,#64]
stp q0,q1,[sp,#96]
stp q2,q3,[sp,#128]
stp q4,q5,[sp,#160]
stp q6,q7,[sp,#192]
movz x0,#0x7898
movk x0,#0x3456,lsl #16
movk x0,#0,lsl #32
movk x0,#0,lsl #48
movz x16,#0x89a8
movk x16,#0x4567,lsl #16
movk x16,#0,lsl #32
movk x16,#0,lsl #48
blr x16
mov x17,x0
ldp q0,q1,[sp,#96]
ldp q2,q3,[sp,#128]
ldp q4,q5,[sp,#160]
ldp q6,q7,[sp,#192]
ldp x0,x1,[sp,#0]
ldp x2,x3,[sp,#16]
ldp x4,x5,[sp,#32]
ldp x6,x7,[sp,#48]
ldr x8,[sp,#64]
.seh_startepilogue
ldp x29,x30,[sp,#80]
.seh_save_fplr 80
add sp,sp,#224
.seh_stackalloc 224
.seh_endepilogue
br x17
.seh_endproc
