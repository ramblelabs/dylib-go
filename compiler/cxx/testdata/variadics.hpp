#pragma once
#include <stdint.h>
#include <stdarg.h>
namespace variadics {
using Entry = int32_t (*)(int32_t, int32_t);
struct Token { int32_t value; };
struct Padding { virtual ~Padding() = default; int32_t padding = 0; };
struct Base {
    virtual ~Base() = default;
    virtual int32_t sum(int32_t count, ...) const & { return -100; }
};
struct Counter : Padding, Base {
    inline static int32_t live_count = 0;
    int32_t value;
    Token token;
    explicit Counter(int32_t n) : value(n), token{n} { ++live_count; }
    ~Counter() { --live_count; }
    int32_t sum(int32_t count, ...) const & override {
        if (count < 0) throw count;
        va_list args;
        va_start(args, count);
        int32_t result = value;
        for (int32_t i = 0; i < count; ++i) result += va_arg(args, int);
        va_end(args);
        return result;
    }
    // A distinct fixed overload must not replace the ellipsis entry.
    int32_t sum(int32_t count) const & { return -200; }
    static double floats(int32_t count, ...) {
        va_list args;
        va_start(args, count);
        double result = 20;
        for (int32_t i = 0; i < count; ++i) result += va_arg(args, double);
        va_end(args);
        return result;
    }
    double mixed(int32_t marker, ...) const volatile & {
        va_list args;
        va_start(args, marker);
        uint32_t a = va_arg(args, uint32_t);
        int64_t b = va_arg(args, int64_t);
        uint64_t c = va_arg(args, uint64_t);
        double d = va_arg(args, double);
        double e = va_arg(args, double);
        va_end(args);
        return marker == 7 && a == UINT32_C(4000000000) && b == -INT64_C(5000000000)
            && c == UINT64_C(10000000000) && d == 1.25 && e == 2.5 ? 42 : -100;
    }
    int32_t operator()(int32_t marker, ...) && {
        va_list args;
        va_start(args, marker);
        int32_t result = value + marker + va_arg(args, int);
        va_end(args);
        return result;
    }
    static int32_t prefix(const Token &token, int32_t count, ...) {
        va_list args;
        va_start(args, count);
        int32_t result = token.value;
        for (int32_t i = 0; i < count; ++i) result += va_arg(args, int);
        va_end(args);
        return result;
    }
    static int32_t pointers(int32_t marker, ...) {
        va_list args;
        va_start(args, marker);
        const Token *token = va_arg(args, const Token *);
        Entry entry = va_arg(args, Entry);
        va_end(args);
        return token && entry ? entry(token->value, marker) : -100;
    }
    static int32_t apply(Entry entry, int32_t marker, ...) {
        va_list args;
        va_start(args, marker);
        int32_t a = va_arg(args, int), b = va_arg(args, int);
        va_end(args);
        return entry ? entry(a, b) + marker : -100;
    }
    void set(int32_t marker, ...) {
        va_list args;
        va_start(args, marker);
        value = marker + va_arg(args, int);
        va_end(args);
    }
    const Token *address(int32_t marker, ...) const {
        va_list args;
        va_start(args, marker);
        bool match = va_arg(args, int) == marker;
        va_end(args);
        return match ? &token : nullptr;
    }
    static int32_t add(int32_t a, int32_t b) { return a + b; }
    static Entry entry() { return &add; }
    static int32_t read(const Token *p) { return p ? p->value : -100; }
    static int32_t live() { return live_count; }
};
}
