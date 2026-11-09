#pragma once
#include "header_hierarchy_base.hpp"
namespace branches {
struct Left:virtual GlobalCounter { explicit Left(int n):GlobalCounter(n) {} };
struct Right:virtual header_types::Counter { explicit Right(int n):Counter(n) {} };
struct Hidden {};
struct Secret {};
}
struct Root:GlobalPad,branches::Left,branches::Right,private branches::Hidden,protected branches::Secret {
 explicit Root(int n):GlobalCounter(n),Left(n),Right(n) {}
 int score(int n) const override { return seed+n+2; }
};
using RootAlias=Root;
struct PlainLeft:GlobalCounter {};
struct PlainRight:GlobalCounter {};
struct Ambiguous:PlainLeft,PlainRight {};
template<class T> struct Box {};
using IntBox=Box<int>;
struct Templated:IntBox {};
struct Nested { struct Base {}; };
struct NestedBase:Nested::Base {};
#if __SIZEOF_POINTER__==4
struct Target:branches::Left {};
#else
struct Target:branches::Right {};
#endif
#if defined(HEADER_HIERARCHY_PRIVATE)
class Conditional:private branches::Left {};
#else
struct Conditional:branches::Left {};
#endif
struct Leaf {};
inline auto factory() { return [](int n){return n+2;}; }
using Lambda=decltype(factory());
namespace { struct Anonymous {}; }
