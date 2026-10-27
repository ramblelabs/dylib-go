extern void record_event(int);
long public_b[3] = {0, 85, 86};
__attribute__((constructor)) static void initialize(void) {
    public_b[0] = 84;
    record_event(1);
}
__attribute__((destructor)) static void finalize(void) {
    record_event(4);
}
