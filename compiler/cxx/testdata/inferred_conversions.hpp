#pragma once
namespace inferred_conversions {
using Narrow=signed char;
using Wide=unsigned long long;
enum class Code:unsigned short { answer=42 };
using IntPointer=int*;
using ConstPointer=const int*;
using Opaque=void*;
using Entry=int(*)(int,int);
inline int add(int a,int b) { return a+b; }
inline int live_forms=0;
struct Plain {
    int value; explicit Plain(int n):value(n) { ++live_forms; }
    ~Plain() { --live_forms; }
    explicit operator int() { return value; }
    explicit operator int() const { return value+1; }
    explicit operator int() volatile { return value+2; }
    explicit operator int() const volatile { return value+3; }
};
struct Qualified {
    int value; explicit Qualified(int n):value(n) { ++live_forms; }
    ~Qualified() { --live_forms; }
    explicit operator int() & { return value+4; }
    explicit operator int() const & { return value+5; }
    explicit operator int() volatile & { return value+6; }
    explicit operator int() const volatile & { return value+7; }
    explicit operator int() && { return value+8; }
    explicit operator int() const && { return value+9; }
    explicit operator int() volatile && { return value+10; }
    explicit operator int() const volatile && { return value+11; }
};
struct NPlain {
    int value; explicit NPlain(int n):value(n) { ++live_forms; }
    ~NPlain() { --live_forms; }
    explicit operator int() noexcept { return value; }
    explicit operator int() const noexcept { return value+1; }
    explicit operator int() volatile noexcept { return value+2; }
    explicit operator int() const volatile noexcept { return value+3; }
};
struct NQualified {
    int value; explicit NQualified(int n):value(n) { ++live_forms; }
    ~NQualified() { --live_forms; }
    explicit operator int() & noexcept { return value+4; }
    explicit operator int() const & noexcept { return value+5; }
    explicit operator int() volatile & noexcept { return value+6; }
    explicit operator int() const volatile & noexcept { return value+7; }
    explicit operator int() && noexcept { return value+8; }
    explicit operator int() const && noexcept { return value+9; }
    explicit operator int() volatile && noexcept { return value+10; }
    explicit operator int() const volatile && noexcept { return value+11; }
};
struct Padding { virtual ~Padding()=default; long padding=0; };
struct Base {
    int base=40; virtual ~Base()=default;
    virtual explicit operator int() const & { return -100; }
    explicit operator long() const { return base+2; }
};
struct Shared { int shared=42; explicit operator double() const noexcept { return shared+0.5; } };
struct Receiver:Padding,Base,virtual Shared {
    int value; explicit Receiver(int n):value(n) {}
    explicit operator int() const & override { if(value<0) throw value; return value; }
    explicit operator int() && { int n=value; value=-1; return n+1; }
    explicit operator bool() const noexcept { return value!=0; }
    explicit operator Narrow() const { return Narrow(-128); }
    explicit operator Wide() const { return ~Wide(0); }
    explicit operator Code() const { return Code::answer; }
    explicit operator Entry() const { return &add; }
    explicit operator IntPointer() & { return &value; }
    explicit operator ConstPointer() const & { return &value; }
    explicit operator Opaque() & { return this; }
    static int forms_alive() { return live_forms; }
    int apply(Entry fn,int a,int b) const { return fn?fn(a,b):-100; }
    int read() const { return value; }
};
struct Template {
    template<class T> explicit operator T() const noexcept { return T(42); }
};
struct Changing {
#ifdef INFERRED_CONVERSION_CONST
    explicit operator int() const { return 42; }
#else
    explicit operator int() { return 42; }
#endif
};
}
using Pointer=inferred_conversions::IntPointer;
using C0=int;
namespace inferred_conversion_bad {
using Reference=int&;
using Extended=long double;
using Nothing=void;
struct Record { int value; };
using Member=int(inferred_conversions::Receiver::*)() const;
struct Unsupported {
    explicit operator Reference() const;
    explicit operator Extended() const;
    explicit operator Nothing() const;
    explicit operator Record() const;
    explicit operator Member() const;
    explicit operator char() const=delete;
private: explicit operator bool() const;
};
struct Hidden:private inferred_conversions::Base { using Base::operator int; };
struct First:inferred_conversions::Base {};
struct Second:inferred_conversions::Base {};
struct Ambiguous:First,Second {};
}
