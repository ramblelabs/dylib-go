# ELF relocations

Native TLS is opt-in on Linux: amd64/386 GD/LD/TLSDESC and arm64 GD/LD/TLSDESC use image-owned
thread storage, including the documented AArch64 TLSDESC address and annotation forms. See the [TLS model and C++ runtime scope](tls.md#native-elf-dynamic-tls).

## x86 short data fields

| Target | Relocations | Value / accepted range |
| --- | --- | --- |
| amd64 | `R_X86_64_8`, `R_X86_64_16` | `S+A`; signed or unsigned 8/16-bit values |
| amd64 | `R_X86_64_PC8`, `R_X86_64_PC16` | `S+A-P`; signed 8/16 bits |
| 386 | `R_386_8`, `R_386_16` | `S+A`; signed or unsigned 8/16-bit values |
| 386 | `R_386_PC8` | `S+A-P`; signed 8 bits |
| 386 | `R_386_PC16` | `S+A-P`; signed 17 bits, retaining the low 16 bits for wrapping instruction pointers |

REL sign-extends the addend from the actual one/two-byte field; RELA replaces that field using its explicit addend. i386 address arithmetic wraps at 32 bits before the narrow range check. Every write must fit inside its input section and leaves neighboring bytes intact; overflow changes no field and prevents image publication. An absolute symbol or `Define` may supply a small numeric value. Ordinary mapped host addresses generally cannot fit an absolute short field.

Range checks follow [LLVM's amd64 implementation](https://github.com/llvm/llvm-project/blob/llvmorg-22.1.8/lld/ELF/Arch/X86_64.cpp) and [i386 implementation](https://github.com/llvm/llvm-project/blob/llvmorg-22.1.8/lld/ELF/Arch/X86.cpp). GNU ld can permit additional bitfield truncations outside these ranges. These extensions do not add 16-bit execution or short-branch expansion; unsupported displacements remain errors.

Pure Go tests inspect real Clang `.reloc` output for both targets and verify positive/negative REL/RELA addends, boundary values, one/two-byte writes at section ends and unchanged bytes after overflow. Native Linux amd64 Go/llgo and Linux Go 386 jobs execute C consumers of all four families through objects and archives, compare them with a system-linked executable, and reject high absolute providers before publication.

## i386 GOT instruction forms

`R_386_GOT32` and `R_386_GOT32X` use an owned four-byte slot containing the resolved symbol address. Both relocation codes retain the original instruction; the loader does not perform GOT32X relaxation.

| Instruction / field form | Relocated value |
| --- | --- |
| Register-relative load or indirect call/jump | `slot(S)-GOT+A` |
| Baseless memory load or indirect call/jump | `slot(S)+A` |
| Baseless `LEA ...@GOT` | `slot(S)-GOT+A`, following GNU ld |
| GOT offset fields without the baseless instruction pattern | `slot(S)-GOT+A` |

The GNU instruction convention recognizes a baseless displacement when the preceding ModRM byte has `mod=00, r/m=101`. Its LEA exception retains the offset. LLVM lld instead treats that baseless LEA as absolute; this loader follows GNU's behavior. Original input bytes determine the form, so relocation writes cannot change that classification. An addend biases the field without changing the pointer stored in the slot. REL includes its encoded word; RELA replaces the field. Arithmetic remains modulo 2^32, and four-byte writes must remain inside the section.

Unit tests cover both codes, REL/RELA addends, register bits, MOV/CALL/JMP/LEA, incomplete prefixes, and unchanged instructions. Native Go Linux 386 tests execute real Clang GOT32 and GNU assembler GOT32X output with both object orders, archives/roots, failed-link retry, OS-library and `Define` providers, plus guarded zero weak slots. They compare all instruction forms with GNU-linked fixed executables. GNU rejects baseless GOT memory accesses when building a shared library; an OS library can still provide their eagerly bound function/data targets to this raw loader.

The behavior follows [GNU i386 GOT relocation handling](https://sourceware.org/git/?p=binutils-gdb.git;a=blob;f=bfd/elf32-i386.c) and documents the [LLVM i386 instruction distinction](https://github.com/llvm/llvm-project/blob/main/lld/ELF/Arch/X86.cpp). It does not qualify llgo 386 or introduce 16-bit execution.

## amd64 GOT offsets and large code models

`GOT` is the image-owned table base and `slot(S)` is an owned pointer slot containing the resolved address `S`.

| Relocation | Computed value | Field / range |
| --- | --- | --- |
| GOT32 (3) | `slot(S)-GOT+A` | Checked signed 32 bits, matching GNU ld |
| GOTOFF64 (25) | `S+A-GOT` | 64 bits |
| GOTPC32 (26) | `GOT+A-P` | Checked signed 32 bits |
| GOT64 (27) | `slot(S)-GOT+A` | 64 bits |
| GOTPCREL64 (28) | `slot(S)+A-P` | 64 bits |
| GOTPC64 (29) | `GOT+A-P` | 64 bits |
| GOTPLT64 (30, deprecated) | `slot(S)-GOT+A` | 64 bits; GNU's compatibility behavior treats it as GOT64 |
| PLTOFF64 (31) | `S+A-GOT` | 64 bits; the session binds function providers eagerly |

RELA replaces the encoded field and applies its explicit addend. A GOT addend biases the field, never the slot's contents. Eight-byte fields preserve the full modulo-2^64 result. Data fields may be byte-aligned; bounds and checked overflow fail before publication. Existing GOTPCREL/GOTPCRELX/REX_GOTPCRELX still use owned slots without instruction relaxation.

GOTPC32/GOTPC64 have no symbol operand: the null symbol and named undefined symbols do not need resolution or introduce archive dependencies. Parsing still validates the record's symbol index. The same rule applies to i386 GOTPC, retaining its implicit REL or explicit RELA addend and modulo-2^32 arithmetic.

For all implemented ELF targets (amd64, arm64 and 386), a global undefined `_GLOBAL_OFFSET_TABLE_` resolves to the image's table. It cannot be overridden by object definitions, `Define`, or OS libraries. A requested root can resolve the base after objects have been selected, without extracting an archive member that attempts to define it. Local definitions with the same spelling retain object scope. The loader uses a single eager GOT rather than GNU's separate `.got`/`.got.plt` output layout.

PLTOFF64 uses a directly resolved function address because session providers are fixed before publication; there is no dynamic preemption or lazy PLT. Its full-width offset can reach a distant OS function without a 32-bit branch thunk. Missing weak symbols remain zero, including zero-filled owned GOT slots. Direct offsets to a missing function do not make that function callable.

Unit tests cover all eight types, signed bounds, full-width wrapping, unaligned field replacement, slot reuse, field biases, missing weak values, ignored GOTPC symbols, and reserved bases on all CI hosts, including 386. Native Linux amd64 Go and llgo tests execute every family with both object orders, archives/roots, failed-link retry, an OS library and `Define`. Actual GCC and Clang `-mcmodel=large -fPIC` objects are inspected and executed, then compared with OS-linked libraries; GCC's PLTOFF64 and Clang's GOT function call both run. Linux Go 386 also executes PIC C code with an explicitly rooted owned base and an unused archive redefinition.

These contracts follow the [x86-64 psABI relocation tables](https://gitlab.com/x86-psABIs/x86-64-ABI/-/blob/master/x86-64-ABI/object-files.tex), [GNU x86-64 relocation handling](https://sourceware.org/git/?p=binutils-gdb.git;a=blob;f=bfd/elf64-x86-64.c), and [LLVM's linker-owned symbols](https://github.com/llvm/llvm-project/blob/main/lld/ELF/Writer.cpp). TLS, IFUNC, dynamic-loader relocations and APX instruction qualification remain separate extensions.

## Symbol-size relocations

`R_X86_64_SIZE32`, `R_X86_64_SIZE64`, and `R_386_SIZE32` write the selected symbol's size plus the relocation addend. They do not write a process address or a section size.

| Target | Relocation | Addend and result |
| --- | --- | --- |
| Linux amd64 | SIZE32 | Explicit RELA addend; result must fit unsigned 32 bits, matching GNU ld |
| Linux amd64 | SIZE64 | Explicit RELA addend; modulo-2^64 result |
| Linux 386 | SIZE32 | Implicit REL word or explicit RELA addend; modulo-2^32 result |

The implementation uses the existing definition selection: strong definitions override weak definitions, and local symbols retain object scope. A selected common symbol uses the maximum allocated size of its tentative definitions. Undefined weak symbols contribute zero when no external address resolves their name. The ELF null symbol also has size zero.

Strong undefined symbols fail. `Define` and OS-library symbol lookup supply addresses without ELF size metadata, so they cannot satisfy a size relocation. This also applies to a weak reference whose name resolves to an external address. Load a matching object definition with recorded size instead. Failure leaves the image unpublished; a missing object can be added before retrying `Link`.

Size references participate in ordinary archive dependency extraction even when there is no address relocation to their target. Unreferenced archive members remain unloaded. Writes outside the section, invalid symbol indexes, unloaded definitions, and amd64 SIZE32 overflow fail before publication.

`TestELFSizeRelocationArithmetic` covers unsigned bounds, negative addends, 64-bit and i386 wrapping, and REL/RELA behavior. `TestELFSizeObjectMetadata` inspects compiler/assembler-produced records. `TestNativeELFSizeRelocations` executes objects, weak overrides, archive dependencies/roots, retry, and a system-linker comparison on Linux amd64 and Go 386. Linux amd64 llgo runs the same tests through the existing native suite.

GNU `as --64/--32` generates the fixtures in native Linux x86 CI. Other hosts inspect checked-in snapshots with a [reproduction script](../testdata/elfsizes/generate.sh). Explicit `.reloc` directives preserve local symbol references: LLVM versions can rewrite even explicit local references into section references, while GNU as normally folds local `@SIZE` expressions. LLVM's i386 assembler does not implement `@SIZE`. Unit arithmetic and snapshot inspection run on all CI hosts; actual execution remains limited to matching Linux x86 targets.

The independently written implementation follows the [GNU x86-64 relocation definitions](https://sourceware.org/git/?p=binutils-gdb.git;a=blob;f=bfd/elf64-x86-64.c) and [GNU i386 size relocation handling](https://sourceware.org/git/?p=binutils-gdb.git;a=blob;f=bfd/elf32-i386.c). It does not add dynamic-loader relocations, TLS, IFUNC, or new host architectures.

## AArch64 instruction relocations

Instruction places must be four-byte aligned. ELF low12 relocations distinguish
ADD/ADDS from unsigned-offset loads/stores, even for byte accesses whose scale is
also zero. Load/store relocations require the declared datum width and a valid
integer/SIMD encoding. Reserved signed-load and SIMD combinations are rejected.
JUMP26 requires B and CALL26 requires BL. ELF GOT page/low12 instruction checks
run before slot allocation, preserving the original word and GOT cursor on an
invalid site. Page range checks, low12 addends and target alignment retain their
existing behavior.

The shared ADRP/branch checks and the Mach-O/COFF instruction paths also reject
unaligned instruction places, including ARM64 COFF section-relative ADD/load
fields. Data fields keep their byte-aligned contracts, including ELF ABS/PREL,
COFF address/relative data and Mach-O unsigned pointers.

Portable tests cover valid ADD/ADDS, signed and unsigned integer loads, SIMD
8/16/32/64/128-bit loads/stores, prefetch, encoded-field replacement, invalid
sites, reserved forms and byte-aligned data. Genuine Clang assembler/C output
checks the relocation families at `-O0`/`-O2`. Native Linux arm64 Go/llgo tests
execute those producers through objects, reversed objects, lazy archives and
archive roots; verify copied scalar/vector data; compare a system-linked shared
library; and retry after missing dependencies or selecting an invalid archive
root. An unused invalid member remains unselected. These checks follow the
[AAELF64 static instruction and load/store field contracts](https://github.com/ARM-software/abi-aa/blob/main/aaelf64/aaelf64.rst#static-aarch64-relocations).

| Relocation | Instruction | Valid displacement |
| --- | --- | --- |
| ADR_PREL_LO21 | ADR | -1 MiB through +1 MiB minus one byte; byte offsets allowed |
| LD_PREL_LO19 | Integer/SIMD LDR, LDRSW, PRFM literal | -1 MiB through +1 MiB minus four bytes; multiple of four |
| CONDBR19 | B.cond, CBZ, CBNZ | Same signed, aligned 1 MiB range |
| TSTBR14 | TBZ, TBNZ | -32 KiB through +32 KiB minus four bytes; multiple of four |
| GOT_LD_PREL19 | 64-bit LDR literal from an image-owned pointer slot | Same signed, aligned 1 MiB range |

ELF RELA uses the explicit addend and replaces the encoded immediate. Local symbol scope, selected definitions, archives, GOT ownership, and publication rollback follow the ordinary linker rules. ADR and conditional branch encoders are shared with COFF, which first supplies its implicit instruction addend. These ADR, literal and conditional instructions must be aligned to four bytes and use the expected opcode. Literal and conditional references outside their ranges fail; the loader does not synthesize arbitrary branch islands or rewrite loads into different instruction sequences.

AArch64 ELF GOT instruction relocations require zero addends, including the existing GOT page/load pair. The loader rejects nonzero addends before allocating the slot. `GOTPCREL32` data instead applies its explicit field bias after selecting the slot, as shown below. Checked ADRP relocations retain their signed page range check; `ADR_PREL_PG_HI21_NC` truncates to the encoded field without a range diagnostic, as its non-checking contract requires. Unchecked encoding does not prove that the resulting instruction sequence addresses the caller's intended memory.

`TestARM64ELFInstructions` verifies opcodes, explicit-addend replacement, byte/scaled offsets, signed limits, register/bit-index preservation, bounds, and alignment. `TestARM64ELFGOTAndUncheckedPage` covers pointer slots, zero-addend validation, and checked/non-checking ADRP. `TestNativeARM64ELFInstructions` executes Clang-produced ADR/literal/GOT loads and both sides of B.cond/CBZ/CBNZ/TBZ/TBNZ branches on Linux arm64, including reversed objects, archive extraction/roots, unused members, and dependency retry. Both Go and llgo run these through the existing native CI suite.

The encoding and addend contracts follow Arm's [AAELF64 specification](https://github.com/ARM-software/abi-aa/blob/main/aaelf64/aaelf64.rst#static-aarch64-relocations). Other AArch64 relocation families, TLS, and authenticated pointers remain outside this extension.

## AArch64 wide moves and narrow data

| Relocation family | Value and encoding | Overflow rule |
| --- | --- | --- |
| MOVW_UABS_G0/G1/G2/G3, with G0/G1/G2_NC | Select one 16-bit part of `S+A`; retain MOVZ/MOVK | Checked lower groups require an unsigned 16/32/48-bit value; NC and G3 truncate |
| MOVW_SABS_G0/G1/G2 | Select one part of `S+A`; choose MOVZ for nonnegative values or MOVN with a complemented field for negative values | `-2^n <= X < 2^n`, with n = 16/32/48 |
| MOVW_PREL_G0/G1/G2/G3, with G0/G1/G2_NC | Select one part of `S+A-P`; checked forms choose MOVZ/MOVN, NC forms retain MOVK | Same signed lower-group limits; NC and G3 truncate |
| ABS16 / ABS32 | Write `S+A` to a byte-aligned data place | `-2^15 <= X < 2^16` / `-2^31 <= X < 2^32` |
| PREL16 | Write `S+A-P` to a byte-aligned data place | `-2^15 <= X < 2^15` |

These are ELF64 RELA relocations: encoded immediates do not add to the explicit addend. A MOVW relocation chooses source bits independently of the instruction's destination shift; the loader retains the valid instruction width, shift, and register. It rejects incompatible move-wide opcodes, invalid W-form shifts, misaligned instruction places, and checked overflows before changing the instruction. ABS16/PREL16 write exactly two bytes, including an unaligned place at the end of a section. ABS32 accepts the specification's negative range as well as unsigned positive values.

Both AArch64 null relocation codes, 0 and the older 256, are accepted. Null references do not inspect ignored symbol/offset fields or pull unused archive members into the image. The same no-op handling applies to code 0 on the other ELF targets.

`TestARM64ELFMOVWMetadata` checks every implemented MOVW form in real assembler output and a Clang `-mcmodel=large -fno-pic` C object. `TestNativeARM64ELFMOVW` executes all those forms, signed SHN_ABS constants, and narrow data on Linux arm64 with Go and llgo. Object order reversal exercises negative relative values; archive roots, unused dependencies, and failed-link retry exercise ownership and selection. Arithmetic, encoded-field replacement, invalid inputs, byte bounds, and both null encodings are also tested on the other CI hosts, including 386.

TLS and authenticated relocations are not added by this extension.

## AArch64 GOT offsets and function data

`GOT` below is the image's owned table base; `slot(S)` is the address of an owned pointer slot holding `S`.

| Relocation | Computed value | Encoding / range |
| --- | --- | --- |
| MOVW_GOTOFF_G0/G1/G2/G3, with G0/G1/G2_NC (300–306) | `slot(S)-GOT` | Signed MOVZ/MOVN groups and MOVK NC groups, using the same signed bounds as MOVW_PREL |
| GOTREL64 / GOTREL32 (307/308) | `S+A-GOT` | Byte-aligned 64-bit data / checked signed 32-bit data; these reference the symbol rather than its slot |
| LD64_GOTOFF_LO15 (310) | `slot(S)-GOT` | 64-bit unsigned-offset load/store, aligned to four bytes; offset is a multiple of eight in `[0,32768)` |
| LD64_GOTPAGE_LO15 (313) | `slot(S)-Page(GOT)` | Same scaled offset contract; `Page` uses the ABI's 4 KiB page |
| PLT32 (314) | `S+A-P` | Byte-aligned signed 32-bit function offset; distant functions use a nearby tail stub with the original field bias |
| GOTPCREL32 (315) | `slot(S)-P+A` | Byte-aligned signed 32-bit data; the addend biases the field, never the slot's target |

GOT instruction/MOVW forms require zero addends. The 15-bit forms check the whole offset, including offsets beyond 4 KiB; they do not truncate to PAGEOFF12. Out-of-range data references still fail. PLT32's addend is retained when a function thunk is needed, rather than moving the thunk's jump target into the function.

An undefined global `_GLOBAL_OFFSET_TABLE_` resolves to this image's GOT using the [shared ELF base rules](#amd64-got-offsets-and-large-code-models). It introduces no archive dependency and cannot be overridden by object, host or OS providers.

Missing weak GOT targets stay zero inside owned slots. GOTREL data retains `S=0`; its 32-bit form can overflow when zero is far from the table. For unresolved weak PLT32 data, the loader chooses `S=P`, matching LLVM lld; AAELF64 leaves that function-offset case unspecified. A selected absolute-zero definition remains a real definition. No missing weak function is executed by the tests.

`TestARM64ELFGOTMetadata` checks all added types in real assembler output. Unit tests cover group encodings, replacement, signed boundaries, field biases, 15-bit ranges, distinct GOT/page bases, opcodes/alignment, zero definitions, missing weak slots, reserved bases, and far thunks, including 386 execution of the Go tests. Native Linux arm64 tests execute every added family with both object orders, archives/roots, failed-link retry, an OS library, and `Define` providers. They also load actual GCC `-fpic` output using GOTPAGE_LO15 and compare its result with a GCC system-linked library. Existing Go and llgo CI suites run these tests.

These contracts follow [AAELF64 GOT-relative data](https://github.com/ARM-software/abi-aa/blob/main/aaelf64/aaelf64.rst#got-relative-data-relocations), [GOT-relative instructions](https://github.com/ARM-software/abi-aa/blob/main/aaelf64/aaelf64.rst#got-relative-instruction-relocations), and [LLVM lld function offsets](https://github.com/llvm/llvm-project/blob/main/lld/ELF/Arch/AArch64.cpp). TLS, authenticated relocations, and newer specialized instruction/initialization relocations remain separate extensions.

## AArch64 unresolved weak references

| Reference | Selected symbol value or action |
| --- | --- |
| Absolute data/MOVW | `S=0`, followed by the ordinary addend and encoding |
| PREL data, ADR/ADRP, literal loads, conditional/test-bit branches, MOVW_PREL | `S=P`, followed by the ordinary addend and encoding |
| CALL26 / JUMP26 | Validate the aligned BL/B instruction, then replace it with NOP |
| GOT | Allocate an owned slot holding zero; keep the slot's ordinary relative address |
| PLT32 data | `S=P`, followed by the field bias; this unspecified ABI case follows LLVM lld |

These rules apply only when a weak reference remains undefined after selecting definitions and checking external providers. A weak or strong SHN_ABS definition at zero is still a definition. A selected object, explicitly rooted archive member, `Define`, or OS library uses its resolved address normally. Weak references alone do not extract archive members.

CALL26's no-op behavior follows AAELF64 for a link without dynamic preemption. JUMP26 follows GNU ld's compatible extension; the ABI leaves that case unspecified. Conditional/test-bit branches use GNU ld's `P+A` rule, including a self-target for zero addends; LLVM lld instead selects `P+4+A`. The loader does not invent a return value for a missing weak function.

Native Linux arm64 tests execute an unconditional weak C call with no provider, unselected/rooted archives, both object orders, an OS library, and an explicitly defined provider. A counter verifies that provided calls actually run. Separate native tests verify PREL16/32/64 and absolute pointer data with missing and selected weak definitions. Unit tests cover all implemented relative families, zero definitions, GOT ownership, addends, invalid opcodes, and alignment. The behavior follows [AAELF64 weak references](https://github.com/ARM-software/abi-aa/blob/main/aaelf64/aaelf64.rst#weak-references) and the [GNU AArch64 linker](https://sourceware.org/git/?p=binutils-gdb.git;a=blob;f=bfd/elfnn-aarch64.c).
