#include <stdarg.h>
int _fltused;
struct Pair { int a, b; };
static int calls;
int add(int a, int b) { ++calls; return a + b; }
double mix(double a, int b) { return a * 2 + b; }
struct Pair pair(struct Pair p) { p.a++; p.b += 2; return p; }
int indirect(const struct Pair *p) { return p->a + p->b; }
double varmix(int count, ...) {
    va_list ap; va_start(ap, count);
    double sum = 0;
    for (int i = 0; i < count; i++) sum += va_arg(ap, double);
    va_end(ap); return sum;
}
int provider_calls(int unused, int ignored) { (void)unused; (void)ignored; return calls; }
