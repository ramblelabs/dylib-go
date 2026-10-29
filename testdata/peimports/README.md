# PE import controls

`helper.c` implements the documented delay-helper entry for test DLLs without
CRT or SDK dependencies. `consumer.c` calls two provider DLLs using names and an
ordinal. Go fixtures compile these sources and link real PE32/PE32+ images with
LLD. Generated inputs are kept in test temporary directories.

Assembler sections hold bound-directory records, stale bound-delay addresses and
unload storage. A real function reference causes the linker to emit each unload
slot's image-base relocation; after linking, the slot is filled with the original
IAT word. Header fields select the bound and auxiliary delay tables. Legacy PE32
controls use a fixed low image base and VA descriptor/lookup entries.

LLVM independently reads the resulting imports and base relocations. Windows
execution checks first resolution, cached calls and unloading/reloading through
this C helper. The helper performs no Go metadata decoding.
