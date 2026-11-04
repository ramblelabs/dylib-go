#include <stdint.h>
typedef uint32_t Vector __attribute__((vector_size(16)));
#define DATA __attribute__((visibility("hidden")))
DATA volatile uint8_t low12_8 = 2, low12_out8;
DATA volatile uint16_t low12_16 = 4, low12_out16;
DATA volatile uint32_t low12_32 = 6, low12_out32;
DATA volatile uint64_t low12_64 = 8, low12_out64;
DATA volatile Vector low12_128 = {22, 0, 0, 0}, low12_out128;

int low12_clear(int a, int b) {
    low12_out8 = low12_out16 = low12_out32 = low12_out64 = 0;
    low12_out128 = (Vector){0, 0, 0, 0};
    return a + b;
}
int low12_stored(int a, int b) {
    return a + b + low12_out8 + low12_out16 + low12_out32 +
        (int)low12_out64 + (int)low12_out128[0];
}
int low12_compiler_eval(int a, int b) {
    uint8_t x8 = low12_8;
    uint16_t x16 = low12_16;
    uint32_t x32 = low12_32;
    uint64_t x64 = low12_64;
    Vector x128 = low12_128;
    low12_out8 = x8; low12_out16 = x16; low12_out32 = x32;
    low12_out64 = x64; low12_out128 = x128;
    return a + b + x8 + x16 + x32 + (int)x64 + (int)x128[0];
}
