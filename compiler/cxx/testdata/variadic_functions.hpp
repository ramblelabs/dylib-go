#pragma once
#include <stdint.h>
#include <stdarg.h>
namespace variadic_functions {
using Entry = int32_t (*)(int32_t, ...);
inline int32_t sum(int32_t marker, ...) {
    if (marker < 0) throw marker;
    va_list args;
    va_start(args, marker);
    int32_t a = va_arg(args, int);
    double b = va_arg(args, double);
    va_end(args);
    return marker + a + int32_t(b);
}
struct Holder {
    inline static int32_t live_count = 0;
    Entry stored;
    explicit Holder(Entry entry) : stored(entry) {
        if (!entry) throw 13;
        ++live_count;
    }
    ~Holder() { --live_count; }
    int32_t call(int32_t marker, int8_t a, float b) const { return stored(marker, a, b); }
    Entry get() const { return stored; }
    explicit operator Entry() const { return stored; }
    static Entry factory() { return &sum; }
    static int32_t invoke(Entry entry, int32_t marker, int16_t a, float b) {
        return entry ? entry(marker, a, b) : -100;
    }
    static int32_t tail(int32_t marker, ...) {
        va_list args;
        va_start(args, marker);
        Entry entry = va_arg(args, Entry);
        va_end(args);
        return entry ? entry(marker, int8_t(2), float(20)) : -100;
    }
    static int32_t live() { return live_count; }
};
}
