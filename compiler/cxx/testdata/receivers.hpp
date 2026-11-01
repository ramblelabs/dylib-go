#pragma once
#include <stdint.h>
namespace receivers {
struct Result {
    int32_t value;
    explicit Result(int32_t value) : value(value) {}
    Result(const Result &) = delete;
    int32_t read() const { return value; }
};
struct Padding { virtual ~Padding() = default; int32_t padding = 0; };
struct Base {
    virtual ~Base() = default;
    virtual int32_t virtual_score(int32_t value) const volatile && { return value; }
};
struct Receiver : Padding, Base {
    int32_t state;
    explicit Receiver(int32_t value) : state(value) {}
    int32_t plain(int32_t value) { return value + state; }
    int32_t plain(int32_t value) const { return value + state + 1; }
    int32_t plain(int32_t value) volatile { return value + state + 2; }
    int32_t plain(int32_t value) const volatile { return value + state + 3; }
    int32_t score(int32_t value) & { return value + state + 4; }
    int32_t score(int32_t value) const & { return value + state + 5; }
    int32_t score(int32_t value) volatile & { return value + state + 6; }
    int32_t score(int32_t value) const volatile & { return value + state + 7; }
    int32_t score(int32_t value) && { return value + state + 8; }
    int32_t score(int32_t value) const && { return value + state + 9; }
    int32_t score(int32_t value) volatile && { return value + state + 10; }
    int32_t score(int32_t value) const volatile && { return value + state + 11; }
    void mark(int32_t value) volatile && { state = value; }
    int32_t read() const volatile & { return state; }
    int32_t fail() && { throw int32_t(42); }
    Result take() && { int32_t value = state; state = -1; return Result(value); }
    int32_t operator()(int32_t value) volatile && { return state + value; }
    int32_t virtual_score(int32_t value) const volatile && override { return state + value; }
    int32_t only(int32_t value) & { return value; }
    int32_t volatile_only(int32_t value) volatile & { return value; }
};
}
