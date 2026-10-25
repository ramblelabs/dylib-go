extern "C" void record_event(int);
extern "C" int atexit(void (*)(void));
thread_local int marker = 3;
#if defined(_MSC_VER)
extern "C" { int _fltused = 0; }
extern "C" int __tlregdtor(void (*)(void));
#define EXIT_ARGUMENT
#define REGISTER_EXIT(function, argument) __tlregdtor(function)
static void nested() { record_event(700 + marker); }
#else
extern "C" void *__dso_handle;
#if defined(__APPLE__)
extern "C" int _tlv_atexit(void (*)(void *), void *, void *);
#define REGISTER_EXIT(function, argument) _tlv_atexit(function, argument, &__dso_handle)
#else
extern "C" int __cxa_thread_atexit(void (*)(void *), void *, void *);
#define REGISTER_EXIT(function, argument) __cxa_thread_atexit(function, argument, &__dso_handle)
#endif
#define EXIT_ARGUMENT void *
static void nested(void *p) {
    if (p != &marker) record_event(-1);
    else record_event(700 + marker);
}
#endif
struct alignas(64) Local {
    unsigned char padding[8192];
    int value;
    double fraction;
    Local *self;
    explicit Local(int initial) : value(initial), fraction(1.5), self(this) {
#if defined(DYLIB_TLS_DYNAMIC_INIT)
        record_event(1000 + initial);
#endif
    }
    ~Local() {
        if (self != this || fraction != 1.5 || padding[0] || padding[8191]) record_event(-3);
        record_event(value);
        if (REGISTER_EXIT(nested, &marker)) record_event(-2);
    }
};
static Local &first() { static thread_local Local value{7}; return value; }
static Local &second() { static thread_local Local value{9}; return value; }
extern "C" int tls_first(int a,int b) { (void)a;(void)b;return first().value++; }
extern "C" int tls_second(int a,int b) { (void)a;(void)b;return second().value++; }
extern "C" int tls_guard_probe(int a,int b) { (void)a;(void)b;return marker; }
extern "C" int tls_step(int a,int b) {
#if defined(DYLIB_TLS_WORK_CALLBACK)
    if (marker == 3) record_event(0);
#endif
    ++marker;
    Local &f=first(), &s=second();
    if(s.value!=9)return -100;
    return a+b+f.value++;
}
static void last_thread_exit(EXIT_ARGUMENT) { record_event(903); }
static void late_exit() { record_event(902); REGISTER_EXIT(last_thread_exit, nullptr); }
static void late_thread_exit(EXIT_ARGUMENT) { record_event(901); atexit(late_exit); }
extern "C" void tls_register_late() { REGISTER_EXIT(late_thread_exit, nullptr); }
