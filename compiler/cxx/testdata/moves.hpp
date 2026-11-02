#pragma once
#include <stdint.h>
namespace moves {
using Number = int32_t;
using Row = int32_t[2];
struct Box {
    inline static int32_t count = 0;
    int32_t value;
    Row values{20, 22};
    explicit Box(int32_t value) : value(value) { ++count; }
    Box(const Box &) = delete;
    Box(Box &&other) : value(other.value) {
        if (value == 13) throw value;
        other.value = -1;
        ++count;
    }
    ~Box() noexcept { --count; }
    static int32_t live() { return count; }
    void *operator&() { return nullptr; }
    int32_t read() const volatile { return value; }
    static int32_t consume(Box &&other) {
        if (other.value == 13) throw other.value;
        int32_t value = other.value; other.value = -1; return value;
    }
    static int32_t probe(Box *p) { return p ? p->value : -100; }
    static int32_t probe(const Box *p) { return p ? p->value + 1 : -101; }
    static int32_t probe(volatile Box *p) { return p ? p->value + 2 : -102; }
    static int32_t probe(const volatile Box *p) { return p ? p->value + 3 : -103; }
    static int32_t probe(Box &p) { return p.value + 4; }
    static int32_t probe(const Box &p) { return p.value + 5; }
    static int32_t probe(volatile Box &p) { return p.value + 6; }
    static int32_t probe(const volatile Box &p) { return p.value + 7; }
    static int32_t probe(Box &&p) { return p.value + 8; }
    static int32_t probe(const Box &&p) { return p.value + 9; }
    static int32_t probe(volatile Box &&p) { return p.value + 10; }
    static int32_t probe(const volatile Box &&p) { return p.value + 11; }
    Box &&self() { return static_cast<Box &&>(*this); }
    const Box &&const_self() const { return static_cast<const Box &&>(*this); }
    volatile Box &&volatile_self() volatile { return static_cast<volatile Box &&>(*this); }
    const volatile Box &&cv_self() const volatile { return static_cast<const volatile Box &&>(*this); }
    static Box *pointer(Box *p) { return p; }
    static const Box *const_pointer(const Box *p) { return p; }
    static volatile Box *volatile_pointer(volatile Box *p) { return p; }
    static const volatile Box *cv_pointer(const volatile Box *p) { return p; }
    static Number &&number(Number &&n) { n += 2; return static_cast<Number &&>(n); }
    Row &&row() { return static_cast<Row &&>(values); }
    static int32_t sum(Row &&r) { return r[0] + r[1]; }
};
}
