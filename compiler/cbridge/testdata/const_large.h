#ifndef CBRIDGE_CONST_LARGE_H
#define CBRIDGE_CONST_LARGE_H
#include <stdint.h>
typedef struct { const uint8_t values[65536]; } ConstLarge;
int32_t const_large(ConstLarge);
#endif
