#pragma once
#include "counter.hpp"
namespace hierarchy {
using Counter = example::Counter;
using Padding = example::Prefix;
struct Left : virtual Counter { explicit Left(int32_t n) : Counter(n) {} };
struct Right : virtual Counter { explicit Right(int32_t n) : Counter(n) {} };
struct Hidden {};
struct Secret {};
struct Root : Padding, Left, Right, protected Hidden, private Secret {
    explicit Root(int32_t n) : Counter(n), Left(n), Right(n) {}
    int32_t score(int32_t n) const override { return Counter::score(n) + 2; }
};
using Alias = Root;
struct PlainLeft : Counter {};
struct PlainRight : Counter {};
struct Ambiguous : PlainLeft, PlainRight {};
struct Leaf {};
using CounterAlias = Counter;
struct Aliased : CounterAlias {};
template<class T> struct Box {};
using IntBox = Box<int>;
struct Templated : IntBox {};
struct Nested { struct Base {}; };
struct NestedBase : Nested::Base {};
#if __SIZEOF_POINTER__ == 4
struct Target : Left {};
#else
struct Target : Right {};
#endif
#if defined(DYLIB_HIERARCHY_PRIVATE)
class Conditional : private Left {};
#else
struct Conditional : Left {};
#endif
}
