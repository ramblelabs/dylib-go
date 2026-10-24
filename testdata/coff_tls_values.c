#if defined(_MSC_VER)
int _fltused;
#endif
static int anchor = 123;
static _Thread_local unsigned char zero[8192];
static _Thread_local _Alignas(64) struct Value { double real; unsigned long long integer; int *pointer; } value = {1.5, 9, &anchor};
int tls_values(int a,int b) {
    if ((__SIZE_TYPE__)&value % 64 || value.real != 1.5+(double)(unsigned char)(value.integer-9)*0.5 || value.pointer != &anchor || *value.pointer != 123 || zero[8191] != (unsigned char)(value.integer-9)) return -1;
    ++zero[8191];
    value.real += 0.5;
    return a+b+7+(int)(value.integer++-9);
}
