#define _GNU_SOURCE
#include <stdint.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>

struct dwarf_eh_bases { void *text, *data, *function; };
extern void __register_frame(void *);
extern void __deregister_frame(void *);
extern void *_Unwind_Find_FDE(void *, struct dwarf_eh_bases *);

uintptr_t boundary_address;
static size_t boundary_size;

int boundary_create(int unused_a, int unused_b) {
    (void)unused_a; (void)unused_b;
#if UINTPTR_MAX > UINT32_MAX
    boundary_size = 3 * (size_t)sysconf(_SC_PAGESIZE);
    for (uintptr_t base = UINT64_C(0x100000000);
         base < UINT64_C(0x8000000000); base += UINT64_C(0x100000000)) {
        void *p = mmap((void *)base, boundary_size, PROT_READ | PROT_WRITE,
                       MAP_PRIVATE | MAP_ANONYMOUS | MAP_FIXED_NOREPLACE, -1, 0);
        if (p != MAP_FAILED) { boundary_address = (uintptr_t)p; return 0; }
    }
#endif
    return -1;
}

void boundary_close(void) {
    if (boundary_address) munmap((void *)boundary_address, boundary_size);
    boundary_address = 0;
}

static void word(unsigned char *p, uint32_t value) { memcpy(p, &value, 4); }

// A real registered CIE/FDE pair with a signed PC-relative 32-bit address.
// This queries the native unwinder, independently of dylib-go's validator.
int boundary_lookup(int shifted, int unused) {
    (void)unused;
    if (!boundary_address) return -1;
    size_t page = boundary_size / 3;
    unsigned char *base = (unsigned char *)boundary_address;
    unsigned char *pc = base + (shifted ? page : 0), *frame = base + 2 * page;
    unsigned char cie[] = {20,0,0,0,0,0,0,0,1,'z','R',0,1,0x78,16,1,0x1b,0,0,0,0,0,0,0};
#if defined(__aarch64__)
    cie[14] = 30;
#endif
    memset(frame, 0, 64);
    memcpy(frame, cie, sizeof(cie));
    word(frame+24, 20);
    word(frame+28, 28);
    word(frame+32, (uint32_t)(pc - (frame+32)));
    word(frame+36, 4);
    __register_frame(frame);
    struct dwarf_eh_bases bases;
    int found = _Unwind_Find_FDE(pc+1, &bases) != 0;
    __deregister_frame(frame);
    return found;
}
