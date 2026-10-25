long coal_value = 42;
long coal_record[2] = {11, 31};
long coal_drop = 5;
int coal_func(int a, int b) { return a + b; }
#ifdef DYLIB_COAL_LIFECYCLE
extern void record_event(int);
void coal_init(void) { record_event(100); }
void coal_fini(void) { record_event(200); }
#endif
