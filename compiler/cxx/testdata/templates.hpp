#pragma once
#include <stdint.h>
#include <stdarg.h>
namespace templates {
using Scalar=int32_t;
inline constexpr int32_t offset=2;
template<class T,int N=2> T add(T value) { if(value<0) throw value; return value+N; }
template<class T> int32_t choose(T) { return 42; }
inline int32_t choose(int32_t) { return -100; }
template<class... Ts> int32_t count(Ts...) { return sizeof...(Ts); }
template<template<class> class Box,class T=int32_t> T boxed(T value) { return Box<T>{value}.value+2; }
template<class T> struct Box { T value; };
template<class,int=1> int32_t unnamed();
template<class T=int32_t> T forward(T);
template<class T> T forward(T value) { return value+2; }
inline int32_t instantiated=forward<int32_t>(40);
template<class T> T deleted(T)=delete;
template<class T> T sum(int32_t marker,...) {
 va_list args; va_start(args,marker);int a=va_arg(args,int);double b=va_arg(args,double);va_end(args);
 return static_cast<T>(marker+a+b);
}
template<long long N> int32_t signed_limit() { return N == (-9223372036854775807LL-1) ? 42 : -100; }
template<unsigned long long N> int32_t unsigned_limit() { return N == 18446744073709551615ULL ? 42 : -100; }
template<class T> T *pointer(T *value) { return value; }
struct Token { int32_t value; };
using TokenPtr=Token*;
template<class P> P identity(P value) { return value; }
template<class T> int32_t read(const T &value) { return value.value+2; }
inline Token &token() { static Token value{40}; return value; }
using Entry=int32_t (*)(int32_t);
template<class T> Entry factory() { return &add<T,2>; }
template<class T> T invoke(Entry entry,T value) { return entry ? entry(value) : -100; }
struct Counter {
 int32_t value;
 explicit Counter(int32_t value):value(value) {}
 template<class T,int N=0> T add(T a) const & { return static_cast<T>(value+a+N); }
 template<class T> static T twice(T a) { return a*2; }
 template<class T> int32_t choose(T) const { return 42; }
 int32_t choose(int32_t) const { return -100; }
};
inline auto lambda_factory() { return [](auto value) { return value+2; }; }
using Lambda=decltype(lambda_factory());
inline Lambda &lambda() { static Lambda value=lambda_factory(); return value; }
struct Sources { static Lambda &get() { return lambda(); } };
namespace child { template<class T> T echo(T value) { return value; } }
namespace { template<class T> T hidden(T value) { return value; } }
}
namespace templates { template<class T> T reopened(T value) { return value+2; } }
namespace template_alias=templates;
namespace template_empty {}
namespace template_imports { using namespace templates; using templates::add; }
