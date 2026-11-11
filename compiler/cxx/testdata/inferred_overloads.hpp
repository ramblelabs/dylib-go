#pragma once
namespace inferred_overloads {
using Narrow=signed char;
using Wide=unsigned long long;
enum class Code:unsigned short { zero=0,answer=42 };
struct Pair { int n; };
using Pointer=Pair*;
using ConstPointer=const Pair*;
inline Pair *pair() { static Pair p{20}; return &p; }
inline int pick(int n) {return n+2;}
inline int pick(long n) {return int(n)+3;}
inline double pick(double n) {return n+4;}
inline Narrow pick(Narrow n) {return n;}
inline Wide pick(Wide n) {return n;}
inline Code pick(Code n) {return n;}
inline int pick(Pointer p) {return p ? p->n+2 : -100;}
inline int pick(ConstPointer p) {return p ? p->n+3 : -101;}
inline int stored=0;
inline void store(int n) {stored=n;}
inline void store(double n) {stored=int(n)+1;}
inline int read() {return stored;}
inline int empty() noexcept {return 42;}
inline int empty(int n) {return n;}
inline int defaults(int n,int extra=2) {return n+extra;}
inline int variadic(int n,...) {return n;}
inline int deleted(int)=delete;
using Ref=int&;
inline int reference(Ref n) {return n;}
inline int plus_two(int n) {return n+2;}
using Entry=int(*)(int);
using OtherEntry=double(*)(double);
inline int apply(Entry fn,int n) {return fn ? fn(n) : -100;}
inline double apply(OtherEntry fn,double n) {return fn ? fn(n) : -101;}
inline Entry entry() {return &plus_two;}
inline int checked(int n) {if(n<0) throw n;return n+2;}
inline double checked(double n) noexcept {return n;}
template<class T> T generic(T n) {return n+2;}
inline double generic(double n) {return n+3;}
struct Left {virtual ~Left()=default;int padding=0;};
struct Base {virtual ~Base()=default;int base=100;virtual int score(int n) const & {return n;} virtual double score(double n) const & {return n;} int inherited(int n) const {return base+n;} double inherited(double n) const {return base+n+1;}};
struct Shared {
 int shared_state=40;
 int shared(int n) const {return shared_state+n;}
 double shared(double n) const {return shared_state+n+1;}
};
struct Receiver:Left,Base,virtual Shared {
 int state;
 explicit Receiver(int n):state(n) {}
 int plain(int n) {return state+n;}
 int plain(int n) const {return state+n+1;}
 int plain(int n) volatile {return state+n+2;}
 int plain(int n) const volatile {return state+n+3;}
 int score(int n) & {return state+n+4;}
 int score(int n) const & override {return state+n+5;}
 int score(int n) volatile & {return state+n+6;}
 int score(int n) const volatile & {return state+n+7;}
 int score(int n) && {return state+n+8;}
 int score(int n) const && {return state+n+9;}
 int score(int n) volatile && {return state+n+10;}
 int score(int n) const volatile && {return state+n+11;}
 double plain(double n) {return state+n;}
 double score(double n) const & override {return state+n+4;}
 int plain_noexcept(int n) noexcept {return state+n;}
 int plain_noexcept(int n) const noexcept {return state+n+1;}
 int plain_noexcept(int n) volatile noexcept {return state+n+2;}
 int plain_noexcept(int n) const volatile noexcept {return state+n+3;}
 int score_noexcept(int n) & noexcept {return state+n+4;}
 int score_noexcept(int n) const & noexcept {return state+n+5;}
 int score_noexcept(int n) volatile & noexcept {return state+n+6;}
 int score_noexcept(int n) const volatile & noexcept {return state+n+7;}
 int score_noexcept(int n) && noexcept {return state+n+8;}
 int score_noexcept(int n) const && noexcept {return state+n+9;}
 int score_noexcept(int n) volatile && noexcept {return state+n+10;}
 int score_noexcept(int n) const volatile && noexcept {return state+n+11;}
 void mark(int n) {state=n;}
 void mark(double n) {state=int(n)+1;}
 int read() const {return state;}
 template<class T> T mixed_template(T n) const & {return n+2;}
 double mixed_template(double n) const & {return n+3;}
 int zero() {return state+22;}
 int zero() const noexcept {return state+23;}
 int zero(int n) const {return state+n;}
 int operator()(int n) & {return state+n;}
 double operator()(double n) const && noexcept {return state+n;}
 template<class T,int N=2> T plus(T n) const & {return n+N;}
 int checked(int n) & {if(n<0) throw n;return state+n;}
 double checked(double n) const & noexcept {return state+n;}
 int native_long(int n) const {return n+2;}
 int native_long(long n) const {return int(n)+3;}
 int pointer(Pointer p) const {return p ? p->n+2 : -100;}
 int pointer(ConstPointer p) const {return p ? p->n+3 : -101;}
 int apply(Entry fn,int n) const {return fn?fn(n):-100;}
 double apply(OtherEntry fn,double n) const {return fn?fn(n):-101;}
 static int static_only(int n) {return n;}
 static double static_only(double n) {return n;}
};
using Alias=Receiver;
}

using Pointer=inferred_overloads::Pointer;
using Same=int;
using C0=int;
namespace inferred_overloads {
inline int collision(Pointer p) {return p ? p->n+22 : -1;}
inline int collision(int n) {return n+2;}
}
