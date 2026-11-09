#pragma once
namespace inferred_members {
using Small=signed char; using Large=unsigned long long;
enum class Code:unsigned short { zero=0,answer=42 };
inline int plus_two(int n) { return n+2; }
using Entry=int(*)(int);
struct Shared { int shared_state=40; int shared(int n) const { return shared_state+n; } };
struct Left { virtual ~Left()=default; long padding=0; };
struct Base { virtual ~Base()=default; int base=100; int inherited(int n) const { return base+n; } virtual int score(int n) const volatile && { return n; } };
struct Receiver:Left,Base,virtual Shared {
int state; explicit Receiver(int n):state(n) {}
int q0(int n) { return state+n+0; }
int q1(int n) const { return state+n+1; }
int q2(int n) volatile { return state+n+2; }
int q3(int n) const volatile { return state+n+3; }
int q4(int n) & { return state+n+4; }
int q5(int n) const & { return state+n+5; }
int q6(int n) volatile & { return state+n+6; }
int q7(int n) const volatile & { return state+n+7; }
int q8(int n) && { return state+n+8; }
int q9(int n) const && { return state+n+9; }
int q10(int n) volatile && { return state+n+10; }
int q11(int n) const volatile && { return state+n+11; }
int n0(int n) noexcept { return state+n+0; }
int n1(int n) const noexcept { return state+n+1; }
int n2(int n) volatile noexcept { return state+n+2; }
int n3(int n) const volatile noexcept { return state+n+3; }
int n4(int n) & noexcept { return state+n+4; }
int n5(int n) const & noexcept { return state+n+5; }
int n6(int n) volatile & noexcept { return state+n+6; }
int n7(int n) const volatile & noexcept { return state+n+7; }
int n8(int n) && noexcept { return state+n+8; }
int n9(int n) const && noexcept { return state+n+9; }
int n10(int n) volatile && noexcept { return state+n+10; }
int n11(int n) const volatile && noexcept { return state+n+11; }
void mark(int n) volatile && { state=n; }
int read() const & noexcept { return state; }
int take() && { int n=state;state=-1;return n; }
int fail() && { throw state; }
long native_long(long n) const { return n; }
char character(char n) volatile & { return n; }
Small narrow(Small n) { return n; }
Large wide(Large n) const { return n; }
Code code(Code n) const { return n; }
bool boolean(bool n) const noexcept { return n; }
double mixed(float a,double b) const { return a+b; }
const volatile Receiver* self() const volatile & { return this; }
Entry entry() const { return &plus_two; }
int apply(Entry fn,int n) const { return fn?fn(n):-100; }
template<class T,int N=2> T plus(T n) const & { return n+N; }
int operator()(int n) volatile && { return state+n; }
int score(int n) const volatile && override { return state+n; }
#ifdef INFERRED_MEMBER_CONST
int changing(int n) const { return n; }
#else
int changing(int n) { return n; }
#endif
#ifdef INFERRED_MEMBER_NOEXCEPT
int changing_exception(int n) noexcept { return n; }
#else
int changing_exception(int n) { return n; }
#endif
static int static_only(int n) { return n; }
int overloaded(int n) { return n; }
double overloaded(double n) { return n; }
Receiver value(Receiver n) { return n; }
Receiver &reference(Receiver &n) { return n; }
int referred(const int &n) { return n; }
long double extended(long double n) { return n; }
int variadic(int n,...) { return n; }
int deleted(int)=delete;
using MemberPointer=int(Receiver::*)(int);
MemberPointer member_pointer() { return &Receiver::q0; }
private: int hidden(int n) { return n; }
protected: int protected_method(int n) { return n; }
};
using Alias=Receiver;
inline auto make_lambda() { return [n=20](auto v){ return n+v; }; }
using Lambda=decltype(make_lambda());
}
namespace inferred_member_bad {
struct Incomplete;
using Scalar=int;
using Pointer=inferred_members::Receiver*;
struct Hidden:private inferred_members::Base {using Base::inherited;};
struct First:inferred_members::Base {}; struct Second:inferred_members::Base {};
struct Ambiguous:First,Second {};
}
