#pragma once
#include "moves.hpp"
namespace conversions {
using Number = int32_t;
using Entry = Number (*)(Number, Number);
struct Padding { virtual ~Padding() = default; Number padding = 0; };
struct Base {
    virtual ~Base() = default;
    virtual operator Number() const & { return -100; }
};
struct Converter : Padding, Base {
    Number value;
    moves::Box box;
    explicit Converter(Number value) : value(value), box(value) {}
    explicit operator bool() const { return value != 0; }
    explicit operator Number() const & override { return value; }
    explicit operator Number() && { return value + 1; }
    explicit operator double() const volatile & { return double(value) + 0.5; }
    explicit operator void *() & { return &value; }
    explicit operator moves::Box &() & { return box; }
    explicit operator moves::Box &&() && { return static_cast<moves::Box &&>(box); }
    explicit operator moves::Box() && { return moves::Box(static_cast<moves::Box &&>(box)); }
    static Number add(Number a, Number b) { return a + b; }
    explicit operator Entry() const { return &add; }
    static Number invoke(Entry fn, Number a, Number b) { return fn(a,b); }
    static Number read(const Number *p) { return p ? *p : -100; }
};
struct Hidden { private: explicit operator Number() const { return 42; } };
}
