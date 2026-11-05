#pragma once
#include <stdint.h>
#include <stdarg.h>
#include "free_included.hpp"
inline int32_t free_global(int32_t a, int32_t b) { return a + b; }
namespace free_catalog {
inline int32_t add(int32_t a, int32_t b) { return a + b; }
inline double add(double a, double b) { return a + b; }
int32_t forward(int32_t);
inline int32_t forward(int32_t value) { return value + 2; }
inline int32_t defaults(int32_t value = 40) { return value + 2; }
static int32_t internal(int32_t value) { return value + 2; }
void deleted(int32_t) = delete;
template<class T> T generic(T value) { return value; }
struct Hidden { static int member(int x) { return x; } };
namespace { inline int secret(int x) { return x; } }
namespace child { inline int32_t add(int32_t value) { return value + 2; } }
inline namespace v1 { inline int32_t versioned(int32_t value) { return value + 2; } }
extern "C" { inline int32_t c_entry(int32_t value) { return value + 2; } }
inline int32_t sum(int32_t marker, ...) {
 if (marker < 0) throw marker;
 va_list args; va_start(args,marker);
 int a=va_arg(args,int); double b=va_arg(args,double);
 va_end(args); return marker+a+int32_t(b);
}
using Entry = int32_t (*)(int32_t,int32_t);
inline Entry factory() { return static_cast<Entry>(&add); }
inline int32_t invoke(Entry entry,int32_t a,int32_t b) { return entry ? entry(a,b) : -100; }
struct Token { int32_t value; };
inline Token &borrowed() { static Token token{20}; return token; }
inline int32_t read(const Token &token,int32_t value) { return token.value+value; }
inline void assign(Token *token,int32_t value) { if (token) token->value=value; }
#ifdef FREE_CATALOG_EXTRA
inline int32_t conditional(int32_t value) { return value+2; }
#endif
}
namespace free_catalog { inline int32_t reopened(int32_t value) { return value+2; } }
namespace free_alias=free_catalog;
namespace free_decoy { inline int32_t wrong(int32_t value) { return value; } }
namespace free_empty {}
namespace free_imports { using namespace free_catalog; using free_catalog::add; }
