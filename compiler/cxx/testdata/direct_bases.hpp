#pragma once
#include <stdint.h>
namespace direct_bases {
struct Padding { virtual ~Padding() = default; int32_t padding = 1; };
struct Counter {
    int32_t value;
    explicit Counter(int32_t n = 20) : value(n) {}
    virtual ~Counter() = default;
    virtual int32_t score(int32_t n) const { return value + n; }
};
struct Virtual {
    int32_t value;
    explicit Virtual(int32_t n = 20) : value(n) {}
    virtual ~Virtual() = default;
    int32_t read() const { return value * 2 + 2; }
};
struct Hidden {};
struct Secret {};
struct Selected;
using Alias = Selected;
struct Selected : Padding, public Counter, virtual Virtual, protected Hidden, private Secret {
    inline static int32_t live_count = 0;
    explicit Selected(int32_t n) : Counter(n), Virtual(n) { ++live_count; }
    ~Selected() { --live_count; }
    int32_t score(int32_t n) const override { return value() + n + 1; }
    int32_t value() const { return Counter::value; }
    static int32_t live() { return live_count; }
};
class PrivateDefault : Counter {};
class Protected : protected Counter {};
using CounterAlias = Counter;
struct Aliased : CounterAlias {};
struct Left : Counter {};
struct Ambiguous : Counter, Left {};
struct VLeft : virtual Counter {};
struct VRight : virtual Counter {};
struct Diamond : VLeft, VRight {};
template<class T> struct Box {};
using IntBox = Box<int>;
struct Templated : IntBox {};
struct Nested { struct Base {}; };
struct NestedBase : Nested::Base {};
#if __SIZEOF_POINTER__ == 4
struct Target : Counter {};
#else
struct Target : Virtual {};
#endif
#if defined(DYLIB_DIRECT_PRIVATE)
class Conditional : private Counter {};
#else
struct Conditional : Counter {};
#endif
}
namespace other_direct_bases {
struct Selected : direct_bases::Secret {};
}
