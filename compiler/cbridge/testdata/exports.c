#include "exports.h"
#include <stdarg.h>
static int32_t number=40;
static int32_t add(int32_t a,int32_t b) { return a+b; }
int32_t sum(int32_t a,int32_t b) { return a+b; }
uint64_t scalars(int8_t a,uint8_t b,int16_t c,uint16_t d,int32_t e,uint32_t f,int64_t g,uint64_t h,float i,double j,bool k) {
    return a==-128 && b==255 && c==-32768 && d==65535 && e==INT32_MIN && f==UINT32_MAX &&
        g==INT64_MIN && h==UINT64_MAX && i==20.5f && j==22.5 && k ? 42 : 0;
}
void notify(int32_t *p) { if(p) *p=42; }
int32_t *pointer(int32_t *p) { return p; }
int32_t *datum(void) { return &number; }
void *callback(void) { return (void *)&add; }
FloatGap float_gap(FloatGap x,int32_t y) { x.value+=y; return x; }
EmptyLane empty_lane(EmptyLane x) { x.value+=2; return x; }
FloatTail float_tail(FloatTail x) { x.value+=1.5; return x; }
PackedBits packed_bits(PackedBits x) { x.a+=2; x.flags=(x.flags+1)&31; x.value+=1.5; return x; }
Large large(Large x) { x.a+=1; x.b+=2; x.c+=3; return x; }
Envelope envelope(Envelope x) {
    x.items[0].value+=1; x.items[1].value+=2; x.tail.value+=3;
    x.bits=packed_bits(x.bits); x.matrix[1][2]+=4; x.large=large(x.large);
    if(x.callback) x.bits.a=x.callback(x.bits.a,2);
    return x;
}
FloatGap variable(FloatGap x,int32_t count,...) {
    va_list a; va_start(a,count);
    if(count==2) { int y=va_arg(a,int); double z=va_arg(a,double); x.value+=y+(float)z; }
    va_end(a); return x;
}
FloatGap record_tail(int32_t marker,...) {
    va_list a; va_start(a,marker); FloatGap x=va_arg(a,FloatGap); va_end(a); x.value+=marker; return x;
}
double exhausted(double a,double b,double c,double d,double e,double f,double g,double h,FloatGap x,FloatTail y) {
    return a+b+c+d+e+f+g+h+x.value+y.value;
}

/* An independent original-prototype caller compiled with this same producer. */
int32_t native_control(void) {
    FloatGap a={0}; a.value=20; a=float_gap(a,22);
    EmptyLane b={0}; b.value=40; b=empty_lane(b);
    FloatTail c={0}; c.value=40.5; c=float_tail(c);
    PackedBits p={0}; p.a=40; p.flags=3; p.value=40.5; p=packed_bits(p);
    Large l={1,2,3}; l=large(l);
    FloatGap v={0}; v.value=20; v=variable(v,2,20,2.0);
    FloatGap t={0}; t.value=40; t=record_tail(2,t);
    return a.value==42 && b.value==42 && c.value==42 && p.a==42 && p.flags==4 && p.value==42 &&
        l.a==2 && l.b==4 && l.c==6 && v.value==42 && t.value==42 ? 42 : 0;
}
