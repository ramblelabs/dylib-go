#include "entries.h"

static EntryPair transform(EntryPair value) {
    EntryPair result={value.a+1,value.b+2};
    return result;
}
PairEntry entry_factory(void) { return transform; }
PairEntry *entry_slot(void) { static PairEntry entry=transform; return &entry; }
EntryFactory entry_higher(void) { return entry_factory; }
PairEntry entry_from_factory(EntryFactory factory) { return factory(); }
EntryPair entry_apply(PairEntry entry,EntryPair value) { return entry ? entry(value) : value; }
EntryPair entry_indirect(PairEntry const *slot,EntryPair value) { return slot ? (*slot)(value) : value; }
static int32_t variable(int32_t marker,...) {
    __builtin_va_list ap;
    __builtin_va_start(ap,marker);
    int32_t value=__builtin_va_arg(ap,int);
    double increment=__builtin_va_arg(ap,double);
    __builtin_va_end(ap);
    return marker+value+(int32_t)increment;
}
TailEntry entry_tail_factory(void) { return variable; }
int32_t entry_tail_apply(TailEntry entry,int32_t marker,int32_t value,double increment) { return entry(marker,value,increment); }
int32_t entry_scalar_apply(ScalarEntry entry,int32_t value) { return entry(value); }
int32_t entry_native_control(void) {
    EntryPair input={20,22};
    EntryPair output=entry_apply(entry_from_factory(entry_higher()),input);
    EntryPair indirect=entry_indirect(entry_slot(),input);
    return output.a==21 && output.b==24 && indirect.a==21 && indirect.b==24 &&
        entry_tail_apply(entry_tail_factory(),2,20,20.5)==42 ? 42 : -1;
}
