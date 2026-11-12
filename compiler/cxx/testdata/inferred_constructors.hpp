#pragma once
namespace inferred_constructors {
using Narrow=signed char;
using Wide=unsigned long long;
enum class Code:unsigned short { answer=42 };
struct Pair { int n; };
using Pointer=Pair*;
using ConstPointer=const Pair*;
using Entry=int(*)(int);
inline int alive=0;
inline int attempts=0;
struct Owner {
 int value;
 explicit Owner(int n):value(n) {++alive;++attempts;}
 ~Owner() {--alive;}
 int read() const {return value;}
};
struct Receiver:Owner {
 Receiver():Owner(42) {}
 explicit Receiver(int n):Owner(n+2) {if(n<0) throw n;}
 explicit Receiver(long n):Owner(int(n)+3) {}
 explicit Receiver(Narrow n):Owner(n==-128 ? 42 : -1) {}
 explicit Receiver(Wide n):Owner(n==~Wide(0) ? 42 : -1) {}
 explicit Receiver(Code n):Owner(int(n)+4) {}
 explicit Receiver(bool n):Owner(n ? 42 : -1) {}
 explicit Receiver(float n):Owner(int(n)+5) {}
 explicit Receiver(double n):Owner(int(n)+6) {}
 explicit Receiver(Pointer p):Owner(p ? p->n+7 : -100) {}
 explicit Receiver(ConstPointer p):Owner(p ? p->n+8 : -101) {}
 Receiver(Entry entry,int n):Owner(entry ? entry(n) : -102) {}
 static Pointer pair() {static Pair p{20};return &p;}
 static int plus_two(int n) {return n+2;}
 static Entry entry() {return &plus_two;}
 static int live() {return alive;}
 static int count() {return attempts;}
};
struct Defaulted:Owner {explicit Defaulted(int n=40,int extra=2):Owner(n+extra) {}};
struct Deduced:Owner {template<class T> explicit Deduced(T n):Owner(int(n)+(__is_same(T,int) ? 2 : 3)) {}};
struct Wrapped {int value;Wrapped(int n):value(n) {}};
struct Converted:Owner {explicit Converted(Wrapped n):Owner(n.value+2) {}};
struct Qualified:Owner {
 explicit Qualified(int&& n):Owner(n+2) {}
 explicit Qualified(const int&& n):Owner(n+99) {}
};
template<class T> struct Box:Owner {explicit Box(T n):Owner(int(n)+2) {}};
using BoxAlias=Box<int>;
struct NullNew:Owner {
 static void* operator new(decltype(sizeof(0))) noexcept {return nullptr;}
 static void operator delete(void*) noexcept {}
 NullNew():Owner(42) {}
};
struct ThrowNew:Owner {
 static void* operator new(decltype(sizeof(0))) {throw 1;}
 static void operator delete(void*) noexcept {}
 ThrowNew():Owner(42) {}
};
using Ref=int&;
using Extended=long double;
using Nothing=void;
using Array=int[2];
using Function=int(int);
using Member=int Pair::*;
struct Generic {template<class T> Generic(T) {}};
struct Deleted {Deleted(int)=delete;};
struct Hidden {private: Hidden(int) {}};
struct Abstract {Abstract(int) {} virtual void missing()=0;};
struct Ambiguous {Ambiguous(long) {} Ambiguous(double) {}};
#ifdef INFERRED_CONSTRUCTOR_WIDE
using Changing=long long;
#else
using Changing=int;
#endif
}
// Global names must not resolve to the query's private Pointer/Unqual traits.
using Pointer=inferred_constructors::Pointer;
using Constant=const int;
