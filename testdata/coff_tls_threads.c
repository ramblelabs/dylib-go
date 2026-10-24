#include <windows.h>
#include <stdatomic.h>
static HANDLE threads[4];
static atomic_int ready, run;
static int count, result;
static int (*step)(int,int);
static DWORD WINAPI worker(void *unused) {
    (void)unused;
    atomic_fetch_add_explicit(&ready,1,memory_order_release);
    while (!atomic_load_explicit(&run,memory_order_acquire)) Sleep(1);
    if (step) for (int i=0;i<100;i++) if (step(20,22)!=49+i) return 1;
    return 0;
}
__declspec(dllexport) void tls_existing_start(void) {
    count=0;result=0;atomic_store(&ready,0);atomic_store(&run,0);
    for (int i=0;i<4;i++) {
        threads[i]=CreateThread(0,0,worker,0,0,0);
        if (!threads[i]) { result=-1;break; }
        count++;
    }
    while (atomic_load_explicit(&ready,memory_order_acquire)!=count) Sleep(1);
}
__declspec(dllexport) void tls_existing_run(const void *fn) {
    step=(int (*)(int,int))fn;
    atomic_store_explicit(&run,1,memory_order_release);
    for (int i=0;i<count;i++) {
        DWORD status=1;
        if (WaitForSingleObject(threads[i],INFINITE)!=WAIT_OBJECT_0 || !GetExitCodeThread(threads[i],&status) || status) result=-2;
        CloseHandle(threads[i]);
    }
    count=0;step=0;
}
__declspec(dllexport) int tls_existing_result(int a,int b) { (void)a;(void)b;return result; }
