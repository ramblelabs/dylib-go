#pragma once
#include <stdint.h>
#include <stdarg.h>
namespace member_templates {
template<class T> struct Box { T value; };
struct Base { template<class T> T inherited(T value) const { return value+1; } };
class Counter: public Base {
 int32_t seed;
 template<class T> T hidden(T value) { return value; }
protected:
 template<class T> T protected_member(T value) { return value; }
public:
 inline static int32_t live_count=0;
 template<class T> explicit Counter(T value): seed(static_cast<int32_t>(value)) { ++live_count; }
 ~Counter() { --live_count; }
 template<class T,int N=2> T add(T value) const & { if(value<0) throw value; return static_cast<T>(seed+value+N); }
 template<class T> T add(T value) && { return static_cast<T>(seed+value); }
 template<class T> static T twice(T value) { return value*2; }
 template<class T> operator T() const { return static_cast<T>(seed); }
 template<class T> T deleted(T value)=delete;
 template<class T,template<class> class B=Box> T boxed(T value) const { return static_cast<T>(seed+B<T>{value}.value); }
 template<class... Ts> int32_t count(Ts...) const { return seed+sizeof...(Ts); }
 template<class T> T sum(int32_t marker,...) const {
  va_list args;va_start(args,marker);int a=va_arg(args,int);double b=va_arg(args,double);va_end(args);
  return static_cast<T>(seed+marker+a+b);
 }
 static int32_t live() { return live_count; }
 struct Nested { template<class T> T nested(T value) { return value; } };
};
using Alias=Counter;
inline int32_t instantiated() { Counter value(int32_t(20)); return value.add<int32_t,2>(20); }
template<class T> struct Holder {
 T seed;
 template<class U> explicit Holder(U value):seed(static_cast<T>(value)) {}
 template<class U> U add(U value) const { return static_cast<U>(seed+value); }
};
using Selected=Holder<int32_t>;
inline auto factory() { return [seed=20](auto value) { return seed+value; }; }
using Lambda=decltype(factory());
inline auto stored=[seed=20](auto... values) { return seed+sizeof...(values); };
using Stored=decltype(stored);
inline auto unnamed() { return [](auto, auto&&... values) { return sizeof...(values); }; }
using Unnamed=decltype(unnamed());
struct Sources { static Lambda &lambda() { static auto value=factory();return value; } };
struct Empty {};
}
