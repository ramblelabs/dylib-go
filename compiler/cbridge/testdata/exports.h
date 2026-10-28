#ifndef CBRIDGE_EXPORTS_H
#define CBRIDGE_EXPORTS_H
#include <stdint.h>
#include <stdbool.h>

typedef struct { unsigned :3; float value; } FloatGap;
typedef struct { uint64_t :64; int32_t value; } EmptyLane;
typedef struct { double value; unsigned :3; } FloatTail;
typedef struct __attribute__((packed,aligned(8))) {
    unsigned :3;
    int32_t a;
    unsigned flags:5;
    unsigned :0;
    unsigned :2;
    double value;
    const int32_t *pointer;
} PackedBits;
typedef struct __attribute__((aligned(16))) { double a,b,c; } Large;
typedef struct {
    FloatGap items[2];
    FloatTail tail;
    PackedBits bits;
    int16_t matrix[2][3];
    Large large;
    int32_t (*callback)(int32_t,int32_t);
} Envelope;

typedef const int32_t ConstNumber;
typedef struct { ConstNumber a; const int32_t b; } ConstPair;
typedef const int16_t ConstRow[3];
typedef struct __attribute__((packed,aligned(8))) {
    unsigned :3;
    const unsigned flags:5;
    const ConstPair pairs[2];
    ConstRow matrix[2];
    int32_t (*const callback)(int32_t,int32_t);
    const int32_t *const pointer;
} ConstPacket;
typedef struct { const int16_t values[2][65]; } ConstBlocks;

int32_t sum(int32_t,int32_t);
int32_t native_control(void);
uint64_t scalars(int8_t,uint8_t,int16_t,uint16_t,int32_t,uint32_t,int64_t,uint64_t,float,double,bool);
void notify(int32_t *);
int32_t *pointer(int32_t *);
int32_t *datum(void);
void *callback(void);
FloatGap float_gap(FloatGap,int32_t);
EmptyLane empty_lane(EmptyLane);
FloatTail float_tail(FloatTail);
PackedBits packed_bits(PackedBits);
Large large(Large);
Envelope envelope(Envelope);
FloatGap variable(FloatGap,int32_t,...);
FloatGap record_tail(int32_t,...);
double exhausted(double,double,double,double,double,double,double,double,FloatGap,FloatTail);
ConstPair const_pair(ConstPair);
ConstPacket const_packet(ConstPacket);
int32_t const_tail(int32_t,...);
int32_t const_stack(int32_t,int32_t,int32_t,int32_t,int32_t,int32_t,int32_t,int32_t,ConstPacket);
int32_t const_blocks(ConstBlocks);
#endif
