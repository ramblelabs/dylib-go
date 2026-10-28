#include "const_large.h"
int32_t const_large(ConstLarge value) {
    int32_t sum=0;
    for (int i=0;i<65536;++i) sum+=value.values[i];
    return sum;
}
