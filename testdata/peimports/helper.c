/* Minimal documented delay helper for executable PE fixture controls.
 * No CRT, SDK, exceptions or Go decoder is involved in resolving the imports. */
typedef __UINTPTR_TYPE__ Word;
typedef unsigned int U32;
typedef unsigned short WChar;
struct Delay { U32 attrs, name, module, iat, names, bound, unload, stamp; };
extern unsigned char __ImageBase;
__declspec(dllimport) void *__stdcall LoadLibraryW(const WChar *);
__declspec(dllimport) U32 __stdcall GetModuleFileNameW(void *, WChar *, U32);
__declspec(dllimport) void *__stdcall GetProcAddress(void *, const char *);
__declspec(dllimport) int __stdcall FreeLibrary(void *);
static struct Delay *seen[4];
static int count, calls;
static void *field(struct Delay *d, U32 value) {
    return value ? (d->attrs & 1 ? &__ImageBase + value : (void *)(Word)value) : 0;
}
void *__stdcall __delayLoadHelper2(struct Delay *d, Word *slot) {
    Word *module = field(d, d->module);
    if (!*module) {
        WChar path[1024];
        U32 n = GetModuleFileNameW(&__ImageBase, path, 1024), end = 0;
        const unsigned char *name = field(d, d->name);
        if (!n || n >= 1024) return 0;
        for (U32 i = 0; i < n; ++i) if (path[i] == '/' || path[i] == '\\') end = i + 1;
        while (*name && end < 1023) path[end++] = *name++;
        if (*name) return 0;
        path[end] = 0;
        *module = (Word)LoadLibraryW(path);
        if (!*module) return 0;
        int found = 0;
        for (int i = 0; i < count; ++i) if (seen[i] == d) found = 1;
        if (!found) { if (count == 4) return 0; seen[count++] = d; }
    }
    Word *iat = field(d, d->iat), *names = field(d, d->names);
    Word lookup = names[slot - iat];
    Word ordinal = (Word)1 << (sizeof(Word) * 8 - 1);
    const char *name = lookup & ordinal ? (void *)(lookup & 0xffff) :
        (d->attrs & 1 ? (char *)&__ImageBase + lookup + 2 : (char *)lookup + 2);
    void *entry = GetProcAddress((void *)*module, name);
    if (entry) { *slot = (Word)entry; ++calls; }
    return entry;
}
int observed(int a, int b) { (void)a; (void)b; return calls; }
int unload(int a, int b) {
    int result = 0; (void)a; (void)b;
    for (int i = 0; i < count; ++i) {
        struct Delay *d = seen[i]; Word *module = field(d, d->module);
        if (!*module) continue;
        Word *iat = field(d, d->iat), *original = field(d, d->unload);
        if (!original) return -1;
        for (int j = 0; original[j]; ++j) iat[j] = original[j];
        FreeLibrary((void *)*module); *module = 0; ++result;
    }
    return result;
}
