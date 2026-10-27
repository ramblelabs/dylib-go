extern void record_event(int);
long shared_owner[3] = {0, 43, 44};
__attribute__((constructor)) static void initialize(void) {
    shared_owner[0] = 42;
    record_event(1);
}
__attribute__((destructor)) static void finalize(void) {
    record_event(4);
}
