extern void record_event(int);
#define TERM_STRING_(x) #x
#define TERM_STRING(x) TERM_STRING_(x)
#ifdef DYLIB_TERM_LIFE_PAIR
#define TERM_LIFE_NAME term_zmarker
#define TERM_LIFE_VALUE 126
#else
#define TERM_LIFE_NAME term_marker
#define TERM_LIFE_VALUE 84
#endif
long TERM_LIFE_NAME[3] = {0,TERM_LIFE_VALUE+1,TERM_LIFE_VALUE+2};
__asm__(".globl _" TERM_STRING(TERM_LIFE_NAME) "_storage\n_" TERM_STRING(TERM_LIFE_NAME) "_storage = _" TERM_STRING(TERM_LIFE_NAME) "\n");
__attribute__((constructor)) static void initialize(void) { TERM_LIFE_NAME[0]=TERM_LIFE_VALUE; record_event(1); }
__attribute__((destructor)) static void finalize(void) { record_event(4); }
