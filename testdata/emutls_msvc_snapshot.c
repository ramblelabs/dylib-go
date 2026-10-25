extern void __dyn_tls_on_demand_init(void);
#if defined(DYLIB_NATIVE_MSVC_TLS)
__declspec(thread) extern unsigned char __tls_guard;
#endif
static int counter;
static void initialize(void) { ++counter; }
__attribute__((used, section(".CRT$XDU")))
static void (*volatile table)(void) = initialize;
int tls_snapshot_value(int a, int b) { (void)a; (void)b; return counter; }
int tls_snapshot_step(int a, int b) {
    table = (void (*)(void))1;
#if defined(DYLIB_NATIVE_MSVC_TLS)
    if (!__tls_guard) __dyn_tls_on_demand_init();
    if (!__tls_guard) __dyn_tls_on_demand_init();
#else
    __dyn_tls_on_demand_init();
    __dyn_tls_on_demand_init();
#endif
    return counter + a + b;
}
