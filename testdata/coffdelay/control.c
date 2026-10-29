/* Reuse the independent PE control's SDK/CRT-free delay helper. */
#include "../peimports/helper.c"

/* GNU delay archives have no unload table. Release the test's provider reference
 * only after the final invocation, before retiring the control image itself. */
int release_provider(int unused, int ignored) {
    int result = 0;
    (void)unused; (void)ignored;
    for (int i = 0; i < count; ++i) {
        Word *module = field(seen[i], seen[i]->module);
        if (*module) { FreeLibrary((void *)*module); *module = 0; ++result; }
    }
    return result;
}
