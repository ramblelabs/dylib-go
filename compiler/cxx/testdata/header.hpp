#pragma once
#include <stdint.h>
#include "header_child.hpp"

class HeaderCounter {
 int32_t seed;
 struct Hidden {};
public:
 struct Nested {};
 inline static int32_t live_count=0;
 explicit HeaderCounter(int32_t value):seed(value) { ++live_count; }
 ~HeaderCounter() { --live_count; }
 int32_t add(int32_t value) const { return seed+value; }
 static int32_t live() { return live_count; }
 template<class T> T twice(T value) const { return value*2; }
};
using CounterAlias=HeaderCounter;
inline auto header_factory() { return [seed=20](auto value) { return seed+value; }; }
using GlobalLambda=decltype(header_factory());
template<class T> struct HeaderBox { T value; };
using SelectedBox=HeaderBox<int32_t>;
struct Sources { static GlobalLambda &get() { static auto value=header_factory();return value; } };
struct Forward;
union HeaderUnion { int value; };
using Scalar=int32_t;

inline int32_t global_add(int32_t a,int32_t b) { struct Local {}; return a+b; }
inline int32_t global_choose(int32_t value) { return value+2; }
inline double global_choose(double value) { return value+2; }
static int32_t local_global(int32_t value) { return value+2; }
extern "C" { inline int32_t header_c(int32_t value) { return value+2; } }
template<class T,int N=2> T header_plus(T value) { if(value<0) throw value; return value+N; }
template<class... Ts> int32_t header_count(Ts...) { return sizeof...(Ts); }
namespace hdr {
struct Entry {};
inline int32_t scale(int32_t value) { return value*2; }
template<class T> T identity(T value) { return value; }
inline namespace inlined { inline int32_t offset(int32_t value) { return value+2; } }
}
namespace alias_hdr=hdr;
using hdr::Entry;
using hdr::scale;
using namespace hdr;
namespace { struct Anonymous {}; inline int ignored() { return 1; } template<class T> T hidden(T v) { return v; } }
#if __SIZEOF_POINTER__ == 4
struct Width32 {};
#else
struct Width64 {};
#endif
#if defined(HEADER_FEATURE)
struct Feature {};
inline int feature() { return 42; }
#endif
