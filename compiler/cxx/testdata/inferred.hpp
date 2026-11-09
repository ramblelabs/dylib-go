#pragma once
namespace inferred_api {
using Small=signed char;
using Large=unsigned long long;
enum class Code:unsigned short { zero=0,answer=42 };
enum class Switch:bool { off=false,on=true };
enum Plain { plain_zero=0,plain_answer=42 };
inline int add(int a,int b) { return a+b; }
inline Small narrow(Small value) { return value; }
inline Large wide(Large value) { return value; }
inline long native_long(long value) { return value; }
inline char character(char value) { return value; }
inline Code code(Code value) { return value; }
inline Switch toggle(Switch value) { return value; }
inline Plain plain(Plain value) { return value; }
inline bool boolean(bool value) noexcept { return value; }
inline float floating(float value) { return value; }
inline double mixed(float a,double b) { return a+b; }
inline int checked(int value) { if(value<0) throw value; return value+2; }
inline int constant() { return 42; }
struct Pair { int value; };
inline Pair *pair() { static Pair value{20}; return &value; }
inline const volatile Pair *echo(const volatile Pair *value) { return value; }
inline void store(Pair *value,int n) { value->value=n; }
inline int read(const Pair *value) { return value->value; }
inline int plus_two(int value) { return value+2; }
using Entry=int(*)(int);
inline Entry entry() { return &plus_two; }
inline int apply(Entry fn,int value) { return fn ? fn(value) : -100; }
template<class T,int N=2> T plus(T value) { return value+N; }
struct Static { static double twice(double value) noexcept { return value*2; } };
#if defined(INFERRED_WIDE)
using Changing=long long;
#else
using Changing=int;
#endif
inline Changing changing(Changing value) { return value; }
inline Pair value(Pair p) { return p; }
inline Pair &reference(Pair &p) { return p; }
inline int referred(const int &p) { return p; }
inline long double extended(long double value) { return value; }
inline int variadic(int first,...) { return first; }
inline int overloaded(int value) { return value; }
inline double overloaded(double value) { return value; }
inline int deleted(int)=delete;
}
namespace inferred_bad {
struct Method { int call(int value) { return value; } };
inline int data=42;
}
