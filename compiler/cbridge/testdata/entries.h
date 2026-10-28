#ifndef CBRIDGE_ENTRIES_H
#define CBRIDGE_ENTRIES_H
#include <stdint.h>

typedef struct __attribute__((packed,aligned(8))) {
    unsigned :3;
    const float a;
    const int32_t b;
} EntryPair;
typedef EntryPair (*PairEntry)(EntryPair);
typedef PairEntry (*EntryFactory)(void);
typedef int32_t (*TailEntry)(int32_t,...);
typedef int32_t (*ScalarEntry)(int32_t);

PairEntry entry_factory(void);
PairEntry *entry_slot(void);
EntryFactory entry_higher(void);
PairEntry entry_from_factory(EntryFactory);
EntryPair entry_apply(PairEntry,EntryPair);
EntryPair entry_indirect(PairEntry const *,EntryPair);
TailEntry entry_tail_factory(void);
int32_t entry_tail_apply(TailEntry,int32_t,int32_t,double);
int32_t entry_scalar_apply(ScalarEntry,int32_t);
int32_t entry_native_control(void);
#endif
