# Mach-O coalescing, aliases, pointers, and stubs

## Coalesced weak atoms

Raw macOS amd64/arm64 objects support bounded weak atoms in ordinary code,
data and zero-fill sections (`S_REGULAR`, `S_ZEROFILL`, `S_GB_ZEROFILL`) and
explicit `S_COALESCED` sections, in objects and lazy archives.
`MH_SUBSECTIONS_VIA_SYMBOLS` and non-alternate nlist boundaries identify
independent atoms; a single whole-section `S_COALESCED` atom does not require
that flag. Eligible groups have named public owners and at least one public
weak definition. Local aliases and local `N_ALT_ENTRY` names share the group's
storage identity.

At one boundary, a strong public name owns the storage. An all-weak group uses
the lexicographically last raw Mach-O name, before C underscore removal or
`debug/macho` dotted-name normalization. Symbol-table order does not change
that owner. This strong-owner policy is fixed across hosts. Older Apple
linkers can move a strong name with a replaced weak name when the weak name
sorts last; standalone controls observe and validate that older policy as well
as newer Apple behavior. Loader tests always require the strong name's original
storage. Other public names forward to their owner while retaining their own
weak or strong definition priority: replacing one alias does not automatically
replace the storage. Public `N_ALT_ENTRY` names strictly inside an atom, or
one-past its final section boundary, forward to the owner's selected address
plus their original offset. A selected provider must have compatible data or
function-entry offsets. Offset/address overflow and nonzero offsets into
native TLS are rejected; `N_INDR` chains retain the accumulated offset.

Ordinary sections require `MH_SUBSECTIONS_VIA_SYMBOLS`. Strong/private
neighbors retain their bytes and fixups; zero-fill atoms need no backing file
bytes. A wholly discarded BSS section needs no storage, and an entirely
redundant archive member remains unselected. Missing owners, colliding
normalized API names, public alternate entries at another non-alternate
boundary (including a shared start), and zero-size unmarked weak definitions
remain outside bounded explicit coalescing. Ordinary inputs with these forms
retain their prior storage/fixup behavior. Frame, compact-unwind and LSDA
sections use associated-record pruning; native TLS and literal section models
are unchanged.

Among selected definitions, a strong definition wins over a weak one and the
first selected weak definition wins a weak/weak tie. Mach-O real definitions,
including weak ones, win over tentative common storage in either input order.
ELF/COFF common selection is unchanged. Explicit host definitions retain the
library's host/object conflict rule; they do not silently interpose a selected
weak object definition.

Selection drops a losing atom's relocations before dependency discovery, so
its missing providers do not trigger archive extraction. Surviving private and
weak atoms retain their fixups. Original section/symbol indices and staged
inputs remain stable across failed-link retries. Entirely discarded sections
need no storage; partially retained sections keep dead bytes as unused padding.
This is coalescing, not general reachability-based dead stripping.

Nlist references, section-relative pointer fixups, local alternate entries and
explicit SUBTRACTOR pairs bind to the winning identity and interior offset.
AMD64 local SIGNED/1/2/4 fields include their trailing immediate-byte bias.
One-past pointers to a final weak atom are supported. Local nlist aliases also
work through AMD64 GOT_LOAD/GOT and ARM64 GOTPAGE/GOTPAGEOFF/POINTER_TO_GOT,
indirect pointers and regenerated stubs. The alias offset is applied to the
selected target before allocating its GOT cell or branch island; it does not
change the relocation's field addend. Repeated references to the same interior
address share a cell. Section-ordinal local GOT/instruction encodings remain
unsupported. Constants already resolved by the assembler remain numeric
constants; missing relocations cannot be reconstructed from their contents.

Ordinary initialization/finalization arrays remain independent sections and
call their selected functions. With `Options{RegisterUnwind: true}`, losing
code also discards its DWARF FDEs, unused CIEs and compact unwind entries before
dependency discovery. Retained records keep corrected CIE back-references,
pointer fields and relocations, including AMD64 implicit PC/LSDA pointers and
ARM64 subtractor anchors. Exception tables referenced only by losing frames
drop their fixups and private definitions; their bytes remain unused padding.
Shared live exception tables retain their storage. Frame PCs referring to a
public alias bind to the original storage owner before independently overridden
weak aliases lose their dependencies; personality pointers retain public-name
binding. Default loading continues to discard optional unwind metadata. Weak bodies in ordinary regular sections
use the same discard and unwind rules when their boundaries qualify above.

Pruning uses the existing supported DWARF/compact encodings and at most 65,536
records per section. Nonoverlapping original sections and the scratch GOT
must fit the 64 MiB image limit. A frame cannot straddle a losing atom boundary;
discarded LSDA needs private symbol-delimited table boundaries. Incoming live
references into discarded LSDA, external references into shrinking metadata,
sized frame symbols and unsupported section-local frame references are rejected.
Native C++ exception tests cover retained catch/cleanup frames and removed
RTTI/personality dependencies, both input orders, objects/archives, failed-link
and registration retry, retirement and Apple system-linked controls.

Tests cover both Mach-O architectures on every native CI host and execute
O0/O2 objects/archives with Go and llgo on macOS. They check strong/weak/common
selection, reversed order, private/local/one-past pointer identity, explicit
subtractor pairs, lazy poison dependencies, retry, lifecycle and unwind limits.
Apple system-linked executables use the same probes, including equivalent
section-relative encodings. Apple `ld -dead_strip` supplies independent native
controls, with the mixed-owner distinction above: LLVM 22 LLD retains the original location for the tested named local
alternate-entry pointer, while Apple rebinds it to the selected atom.

Interior-alias probes additionally check named field/one-past pointers, an
alternate function entry, both LOCAL-marker and nlist indirect-pointer forms,
archive selection, failed-link retry and retained providers. Portable checks
include near/distant short and padded stubs and target-address overflow. The
Apple interior-alias control uses equivalent ordinary pointer storage and a
GOT tail call because the system linker rejects these raw symboled indirect
section inputs. It executes the same pointer-identity and function-call probe;
the loader's indirect-table tests execute the original raw sections.

Ordinary-section tests reuse these pointer, subtractor, GOT/stub, archive,
lifecycle and C++ unwind probes with genuine regular-section producers. BSS
tests additionally cover retained zeros, strong initialized replacements,
private interior/one-past pointers, whole-section removal, large-zero-fill
section headers and independent Apple-linked execution. Boundary tests keep
unqualified alias-bearing storage and its missing dependencies intact.
Zero-fill archive tests wrap the compiler-produced objects in an indexless ar
container because LLVM and older Apple archivers reject `S_GB_ZEROFILL`.
Their Apple-linked value/pointer controls use equivalent C-produced weak zero
storage because older Apple linkers reject raw weak BSS replacements; loader
object/archive calls keep the original zero-fill sections and local aliases.

Public-group tests additionally cover same-address weak/mixed definitions,
interior and one-past public names, reversed nlist order, native function-entry
calls, pointers/GOT cells and explicit cross-section subtractors. A real
`N_INDR` chain checks a forwarded interior address. Go and llgo execute both
input orders, objects/archives, dependency retries, constructor/destructor
ordering, leased data after provider retirement and C++ RAII unwinding. Apple
system-linked controls run the same value/address/call probes. Constants
already folded by the assembler do not become interposable relocations.

## Forwarding aliases

Raw macOS amd64/arm64 objects support external `N_INDR` forwarding definitions. An alias owns its public name and resolves to its target's address. `n_value` is an offset into the object's string table, not a native address. The parser validates that offset, the terminating NUL, the nonempty target name, and the zero section ordinal. Names omit exactly one leading Mach-O linker underscore, including names containing dots.

Alias chains resolve through selected global definitions, common/absolute symbols, explicitly defined host targets, or session-owned libraries. An alias target is an external name; a same-named local symbol cannot satisfy it. Aliases without an existing target symbol receive a synthetic undefined external after the original symbol table, preserving relocation indices. Selected aliases introduce dependencies even when they have no relocations: an archive alias member pulls its target member, and providers initialize before their consumers. Unused archive members remain unselected.

Aliases are strong definitions, following Apple ld64's behavior. `N_WEAK_DEF` on the alias or a weak definition of its target does not weaken the alias's public name. Duplicate strong names fail; a host definition of the alias's name conflicts with the object definition. Host addresses can supply the target's name instead. Private-external `N_PEXT | N_EXT` aliases participate in linking and explicit `Lookup`, which does not implement Mach-O dylib export visibility. Non-external `N_INDR` records are ignored. COFF weak fallback and archive search rules remain separate.

Alias cycles and unresolved selected targets fail before image publication. Missing dependencies can be added before retrying `Link`. The tests compile genuine Mach-O symbols for both architectures, preserve alias chains that LLVM's assembler normally flattens, and execute object/archive calls with Go and llgo on macOS. They also check data relocations to alias addresses, private-external names, common/absolute/host targets, lazy extraction, malformed records, and retry.

## Indirect pointer and stub tables

The Go parser reads `reserved1`/`reserved2` from raw section headers and validates their ranges in `LC_DYSYMTAB`'s indirect symbol table. Original nlist indices remain intact when forwarding aliases add synthetic targets. Table entries become explicit fixups before dependency discovery, so their targets participate in archive extraction and initializer ordering.

| Section type | Raw-object behavior |
| --- | --- |
| `S_NON_LAZY_SYMBOL_POINTERS` | Resolve each symbol and write its address before execution |
| `S_LAZY_SYMBOL_POINTERS` | Eagerly replace the initial resolver/helper word with the target address |
| `S_LAZY_DYLIB_SYMBOL_POINTERS` | Same eager binding, using already loaded session libraries or host definitions |
| `S_SYMBOL_STUBS`, amd64 width 5 | Regenerate `jmp rel32`, with an image-owned absolute island for a distant target |
| `S_SYMBOL_STUBS`, amd64 width 6 or larger | Regenerate `jmp [rip + displacement]` through an image-owned GOT slot; NOP padding retains entry spacing |
| `S_SYMBOL_STUBS`, arm64 width 4 or 8 | Regenerate `b`, with an image-owned absolute island for a distant target; retain NOP padding |
| `S_SYMBOL_STUBS`, arm64 width 12 or larger, multiple of 4 | Regenerate `adrp x16; ldr x16; br x16` through an image-owned GOT slot; retain NOP padding |

Pointer binding overwrites placeholder words rather than adding them to the resolved address. Original pointer fixups are retained only for local markers; global table entries and supported stubs replace their old fixups. Stub regeneration preserves parameter registers, stack arguments, floating-point registers, and the caller's return address. ARM64 uses the ABI's interprocedural scratch register `x16`, leaving the hidden result pointer in `x8` intact. ARM64 stub storage must have at least four-byte alignment. Binding is complete before W^X protection and publication; no dyld lazy resolver is installed.

Non-lazy `INDIRECT_SYMBOL_LOCAL` pointers use an existing pointer relocation or rebase their stored original address to an unambiguous retained section of the same object. `INDIRECT_SYMBOL_LOCAL | INDIRECT_SYMBOL_ABS` retains a numeric address without relocation. Missing targets, invalid symbol indices/ranges, partial entries, zero stub widths, misplaced markers, and invalid local addresses fail before execution. Unimplemented stub widths remain inspectable but block raw linking. Failure before initialization allows adding dependencies or host target definitions and retrying.

Tests compile both target formats on every CI host. Native macOS Go and llgo tests call pointers and stubs from objects/archives, through forwarding aliases and host targets, and with stripped local fixups and lazy-dylib pointer types. A 24-byte C struct parameter/result with a floating-point argument exercises stack/register arguments and hidden aggregate-result state.

Short/padded-stub tests compile two adjacent entries at four widths per architecture. Native macOS Go/llgo tests execute objects, lazy archives and retained symbol providers at O0/O2, including 24-byte indirect results and enough integer/floating arguments to exhaust registers. Portable tests check distant-target islands, full addresses, padding, entry boundaries and malformed instruction alignment.

General coalescing beyond the bounded atoms above, stubs smaller than one branch instruction, authenticated arm64e symbols, TLS forms beyond [plain C native TLV](tls.md#native-mach-o-tlv), and language runtime registration still require additional support. Complete `.dylib` images continue to use the OS loader.

## Format references

- [LLVM subsection parsing](https://github.com/llvm/llvm-project/blob/llvmorg-22.1.8/lld/MachO/InputFiles.cpp): symbol boundaries, `MH_SUBSECTIONS_VIA_SYMBOLS`, and `N_ALT_ENTRY`.
- [Apple ld64 Mach-O parser](https://github.com/apple-oss-distributions/ld64/blob/main/src/ld/parsers/macho_relocatable_file.cpp): `appendAliasAtoms` decodes external `N_INDR` records and their target names.
- [LLVM alias-symbol tests](https://github.com/llvm/llvm-project/blob/main/lld/test/MachO/alias-symbols.s): alias/private-external cases and the difference between ld64's strong alias definitions and LLD's inherited target strength.
- [LLVM Mach-O definitions](https://github.com/llvm/llvm-project/blob/llvmorg-22.1.8/llvm/include/llvm/BinaryFormat/MachO.h): symbol flags and structures.
- [Apple Mach-O headers](https://github.com/apple-oss-distributions/xnu/blob/main/EXTERNAL_HEADERS/mach-o/loader.h): section types, indirect-table indexing, and local/absolute markers.
- [Arm procedure call standard](https://github.com/ARM-software/abi-aa/blob/main/aapcs64/aapcs64.rst): interprocedural scratch registers and the indirect result location register.
