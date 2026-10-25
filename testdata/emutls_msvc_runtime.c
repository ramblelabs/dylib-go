#include <stdint.h>
extern void tls_runtime_init(void);
extern int tls_runtime_plain(void (*)(void));
extern int tls_runtime_argument(void (*)(void *), void *, void *);
static int events;
void tls_constructor(void) { tls_runtime_init(); events = events * 10 + 4; }
static void first(void) { events = events * 10 + 1; }
static void second(void) { events = events * 10 + 2; }
static void argument(void *value) { events = events * 10 + (int)(uintptr_t)value; }
#if defined(_WIN32) && defined(__i386__)
static void __attribute__((thiscall)) this_argument(void *value) {
    events = events * 10 + (value == (void *)5 ? 5 : 0);
}
#endif
int tls_use(int a, int b) {
    (void)a; (void)b;
    tls_runtime_init();
    tls_runtime_init();
    if (tls_runtime_plain(first) || tls_runtime_argument(argument, (void *)3, 0)) return -1;
#if defined(_WIN32) && defined(__i386__)
    if (tls_runtime_argument((void (*)(void *))this_argument, (void *)5, 0)) return -1;
#endif
    if (tls_runtime_plain(second)) return -1;
    return events;
}
int tls_events(int a, int b) { (void)a; (void)b; return events; }

static unsigned char native_guard;
unsigned char *tls_native_guard(void) { return &native_guard; }
