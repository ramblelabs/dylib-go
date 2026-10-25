# Raw-object TLS

Unix native and Clang emulated TLS use image-owned OS-thread values and the same
cleanup registry. Native COFF TLS uses an OS-loaded minimal DLL. Parsing,
layout, validation, archive selection and relocation stay in Go. Complete shared
libraries continue to use their OS loader's TLS support.

| Raw input model | Option | Qualified scope |
| --- | --- | --- |
| Native Mach-O TLV | `Options.NativeTLS` | C values and documented C++ wrappers, macOS amd64/arm64; initialized, zero-filled and aligned values |
| Clang emulated TLS | `Options.EmulatedTLS` | Linux/macOS/Windows targets below, including documented C++ initialization/destruction |
| Native ELF dynamic TLS | `Options.NativeTLS` | C values and documented C++ wrappers, Linux amd64/386 GD/LD/TLSDESC and arm64 GD/LD/TLSDESC |
| Native COFF TLS | `Options.NativeTLS` | Windows amd64/arm64/386: constant/zero/aligned C TLS from Clang MSVC/GNU, and documented Clang GNU/MSVC C++ initialization and thread-exit hooks |
| Native ELF static TLS, other Darwin models | None | Unsupported; use emulated TLS or a complete OS library |

## Native Mach-O TLV

Compile ordinary C macOS objects with native TLS (Clang's default for the
supported targets, or explicit `-fno-emulated-tls`) and enable
`Options{NativeTLS: true}`. All participating TLS definitions must be loaded into
the same image, including lazily selected archive members. Failed links before
native initialization permit adding a missing provider and retrying.

The loader accepts `S_THREAD_LOCAL_REGULAR`, `S_THREAD_LOCAL_ZEROFILL` and
`S_THREAD_LOCAL_VARIABLES`. It validates each three-word TLV descriptor and its
bootstrap/template relocations before native initialization. Templates are
packed into one bounded, aligned module template after ordinary relocations,
including initialized structs, arrays and native pointers. Every native thread
receives an independent copy. The allocation budget is shared with emulated
TLS when both options are enabled; the existing image and alignment limits
also apply. At most 4096 native TLV descriptors are accepted per image.

Clang's `__thread_vars` can declare byte alignment. Private image pages and
24-byte descriptor strides still guarantee pointer-aligned runtime storage;
that normal compiler output is accepted. Zero-fill sections never read object
header bytes from their on-disk zero offset.

The linker binds `_tlv_bootstrap` to an image-owned entry generated in Go. The
entry calls the existing native per-thread allocator, then adds the validated
variable offset. It preserves Darwin's additional TLS registers: all GPRs
except RDI/RAX on amd64, and X1-X15 plus all 128 bits of Q0-Q31 on arm64, along
with ordinary callee-saved registers. No Go callback, private llgo runtime hook,
new C runtime interface or dyld TLS-module registration is needed. The original
compiler-emitted TLV callers remain intact.

Supported instruction relocations are zero-addend RIP-relative 64-bit MOV loads
(`X86_64_RELOC_TLV`) and zero-addend ADRP/LDR references
(`ARM64_RELOC_TLVP_LOAD_PAGE21/PAGEOFF12`). Targets must be complete selected
image-owned TLV descriptors. Ordinary references to template/descriptor bytes,
foreign or OS-library TLV descriptors, nonzero keys, bootstrap overrides,
unsupported opcodes/addends and partial/overlapping descriptors fail before
publication. Thread-local pointer and loader-managed initializer sections,
authenticated arm64e remain unsupported. Compiler-emitted
C++ initialization wrappers and destructor registrations use the scope below.
Linux ELF models have the separate scope below.

`Lookup`, `Resolve` and `Bind` reject raw TLS variable names: their linker address
is a descriptor, not a process-global variable address. Export a native C getter
or function that performs the TLS access instead. `DefineSymbol` can retain such
a function provider and its TLS owner across sessions; it does not transfer TLS
descriptors between images. Pin Go callers with `runtime.LockOSThread` for any
sequence that requires stable TLS identity, including initialization/finalization
when relevant. Stop native users and retain the corresponding code leases for
as long as borrowed TLS pointers remain in use.

Native tests cover O0/O2 objects/archives, retry, register and indirect-result
preservation, independent images, struct/pointer/array templates, mixed emulated
TLS, C-created threads, pinned Go threads, constructor/exit-hook/finalizer access,
retained function providers and owner retirement before/concurrent with thread
exit. Both architectures compare generated entry bytes with an independent LLVM
assembly reference. Native register probes check the full additional GPR/vector
contract, including both 64-bit lanes of every ARM64 vector register.

## Native ELF dynamic TLS

Enable `Options{NativeTLS: true}` for ordinary plain C Linux PIC objects and
archives. Clang and GCC global-dynamic/local-dynamic and GNU2 TLSDESC output is supported on
amd64/386. AArch64 supports the descriptor model emitted by both compilers
and traditional TLSGD ADR, page/add and GOT-relative MOVW sequences. GCC
`-mtls-dialect=trad` C fixtures emit page/add; the additional forms are qualified
with independent assembly fixtures. All
TLS definitions must be selected into the same image. Ordinary object/archive
dependency resolution, missing-provider retry and retained function providers
work as for Mach-O. Raw TLS symbol names cannot be bound as ordinary variables;
use a native getter, pin multi-call Go sequences to an OS thread and retain code
leases while borrowing returned TLS pointers.

| Architecture | Implemented static relocations | Address-entry convention |
| --- | --- | --- |
| amd64 | `TLSGD`, `TLSLD`, `DTPOFF32`, `DTPOFF64` | `__tls_get_addr`, ordinary System V C ABI |
| 386 | `TLS_GD`, `TLS_LDM`, `TLS_LDO_32` | GNU `___tls_get_addr`, index in EAX, result in EAX |
| amd64 | `GOTPC32_TLSDESC`, `TLSDESC_CALL` | Descriptor in RAX, result relative to `FS:0`; preserves every other GPR and OS-enabled user register state |
| 386 | `TLS_GOTDESC`, `TLS_DESC_CALL` | Descriptor in EAX, result relative to `GS:0`; preserves every other GPR and OS-enabled user register state |
| arm64 | `TLSGD_ADR_PREL21`, `TLSGD_ADR_PAGE21`, `TLSGD_ADD_LO12_NC`, `TLSGD_MOVW_G1`, `TLSGD_MOVW_G0_NC` | `__tls_get_addr`, ordinary AAPCS64 C ABI, index in X0 and absolute address in X0 |
| arm64 | `TLSLD_ADR_PREL21`, `TLSLD_ADR_PAGE21`, `TLSLD_ADD_LO12_NC`, `TLSLD_MOVW_G1`, `TLSLD_MOVW_G0_NC`, `TLSLD_LD_PREL19` | Shared module-base index; ordinary `__tls_get_addr`; literal form copies the 16-byte index with a Q-register LDR |
| arm64 | `TLSLD_MOVW_DTPREL_G2/G1/G0` and lower `_NC` groups; `TLSLD_ADD_DTPREL_HI12/LO12/LO12_NC`; checked/unchecked `TLSLD_LDST8/16/32/64/128_DTPREL_LO12` | Packed module offsets applied to canonical MOVW, ADD, unsigned integer/SIMD LDR/STR instructions |
| arm64 | `TLSDESC_LD_PREL19`, `TLSDESC_ADR_PREL21`, `TLSDESC_ADR_PAGE21`, `TLSDESC_LD64_LO12_NC`, `TLSDESC_ADD_LO12_NC`, `TLSDESC_OFF_G1/G0_NC`, `TLSDESC_LDR/ADD/CALL` | Descriptor in X0, result relative to `TPIDR_EL0`; preserves X1-X29 and every bit of Q0-Q31 |

The loader retains TLS symbol types, packs writable allocated `SHF_TLS` data and
zero-fill sections into one bounded aligned template, and applies ordinary
relocations before copying that template. Each native thread receives its own
module allocation through the existing image owner. Private GD/LD indices and
TLSDESC descriptors refer to that owner; they never contain host dynamic-loader
module IDs. The linker generates all entry instructions in Go, including the
386 register-to-cdecl adapter and AArch64's additional register saves and
thread-pointer subtraction. No glibc DTV mutation, static TLS reservation,
private llgo runtime access or new C runtime interface is required.

Address relocations require the corresponding zero-addend instruction forms
(RIP-relative LEA uses the ABI's `-4` field addend). AArch64 register operands
remain those selected by the compiler, including GCC's alternate call register.
TLS definitions, offsets, template extents, instructions, alignment, reserved
helpers and allocation limits are validated before publication. Up to 4096
private address records are permitted; the 64 MiB per-thread allocation budget
is shared with emulated TLS. Compiler-produced local-dynamic module-base indices
are image-wide, and DTP-relative values use the packed template offsets.

x86 TLSDESC accepts the canonical zero-addend indirect call through `(E)RAX`.
Its address instruction uses a RIP-relative LEA with addend -4 on amd64 or a
zero-addend EBX GOT-relative LEA on i386. `_TLS_MODULE_BASE_` is a reserved,
undefined TLS symbol for the image's packed module base; callers may combine it
with DTP-relative offsets. GD/LD and TLSDESC references share one allocation per
image and OS thread, including when producers or models are mixed.

The Go-generated x86 descriptor entry saves caller-clobbered GPRs, uses
CPUID/XGETBV to size and save all enabled user state with XSAVE, and restores it
before returning. Without OSXSAVE it uses FXSAVE (the native i386 baseline is
SSE2). A separate aligned area holds i386 cdecl arguments. The adapter preserves
the full x87 stack; its internal ordinary C call starts with an empty x87 stack.
Native probes use a hostile C helper to check all GPRs, XMM registers and eight
x87 values, exercise the FXSAVE fallback, and check YMM/ZMM/opmask state when the
runner's CPU and OS enable it. AMX/APX hardware is not independently qualified.
The [glibc x86 register contract](https://github.com/bminor/glibc/blob/glibc-2.42/sysdeps/x86/sysdep.h)
and [x86-64 descriptor entry](https://github.com/bminor/glibc/blob/glibc-2.42/sysdeps/x86_64/dl-tlsdesc-dynamic.h)
document why an ordinary C call alone cannot implement this entry.

GCC AArch64 O0/O2 global-dynamic and local-dynamic fixtures both emit TLSGD
page/add pairs. Their calls use an ordinary C entry, independently of the
register-preserving descriptor entry; both models share each definition's
private address record and one thread allocation. Mixed GCC traditional/Clang
descriptor objects and archives, native thread exit, C++ initialization and
destructor retirement, pointer/aggregate values and retained providers are tested.
TLSGD ADR checks its signed 21-bit PC displacement. The canonical 64-bit MOVW
pair uses G1 MOVN/MOVZ with shift 16 and G0 MOVK with shift 0; G1 selects the sign
and checks the GOT-relative value lies in [-2^32, 2^32). Its private index address
is relative to the image-owned GOT, independently of the target variable offset.
Nonzero encoded immediates, addends, wrong widths/shifts and out-of-range values
are rejected before publication. Alternative operand forms remain unqualified.

AArch64 TLSLD references share one module index with byte offset zero, including
references to different symbols. DTP-relative operands use each selected symbol's
packed section offset plus its explicit addend. Address construction allows a
one-past section offset; loads/stores require the entire datum to fit. Checked
MOVW/ADD/load-store ranges, encoded zero immediates, instruction widths/shifts,
scaled alignment and zero-addend module-index references are validated. Negative
addends within the same template section and fields inside a record are tested.
GD, LD and TLSDESC still allocate one module value per image and OS thread.

Independent assembly executes all 24 implemented TLSLD relocation kinds with
Clang TLSDESC/GCC traditional C providers and nonthrowing C++ providers. C getters
cross-check 8–128-bit data, FP fields and zero fill beyond 64 KiB. GNU ld system
controls exercise page/add module indices and DTP-relative operations; GNU ld
2.40 does not recognize module-index MOVW/literal and 128-bit DTPREL load/store
records, so its control uses ordinary Q accesses through relocated ADD pointers.
Those additional records are qualified by raw native execution, ABI field/range
checks and compiler-produced getters, not claimed as GNU ld execution coverage.

AArch64 TLSDESC supports all ten relocation kinds with the qualified operand
forms. A zero-immediate 64-bit LDR literal loads the descriptor entry, and ADR
constructs its address; both enforce signed 21-bit displacement limits (the
literal displacement must be divisible by four). Canonical G1 MOVN/MOVZ plus G0
MOVK construct descriptor-minus-GOT, including negative offsets, with the same
checked range as TLSGD. These references address the descriptor half of each
private record, independently of its GD/LD index.

The LDR/ADD/CALL relaxation annotations validate and preserve their instructions;
the loader does not relax them to static TLS. LDR annotations accept zero-offset
or unshifted register-offset 64-bit pointer loads. ADD annotations accept
zero-immediate or unshifted-register 64-bit ADD. Nonzero relocation addends,
other widths, shifts and addressing forms remain unqualified. Value and full
register-preservation probes share the independent address sequences; literal,
ADR, MOVW and page forms execute as raw objects/archives and GNU ld/OS TLS controls.
Clang/GCC C and nonthrowing C++ providers test shared storage and thread exit.
Observed Clang 22/GCC 12 `tiny` C output still used page/load/add; no broader
compiler-emission claim is inferred from the hand-written sequences.

Other AArch64 instruction forms, sign-extending loads and prefetch,
initial-exec/local-exec offsets,
loader-owned dynamic TLS relocations, external/OS-library TLS definitions,
loader-managed TLS initialization and SVE/SME calling variants
remain unsupported. They need separate ABI/runtime qualification. Complete OS
libraries continue to use their loader. Native ELF tests cover the same values,
thread isolation, cleanup, lifetime and lifecycle cases as Mach-O, plus GCC
O0/O2 calls and explicit rejection of static TLS models. Independent assembly
references cover ordinary entries on all three architectures and their
separate descriptor entries; an AArch64 probe checks X1-X18 and all 128
bits of Q0-Q31.

ABI references: [AArch64 ELF TLS relocations](https://github.com/ARM-software/abi-aa/blob/main/aaelf64/aaelf64.rst),
[LLVM AArch64 ELF TLS register contract](https://github.com/llvm/llvm-project/blob/llvmorg-22.1.8/llvm/lib/Target/AArch64/AArch64CallingConvention.td),
[GNU i386 address-entry convention](https://github.com/bminor/glibc/blob/master/sysdeps/i386/dl-tls.h)
and [AArch64 descriptor resolver](https://github.com/bminor/glibc/blob/master/sysdeps/aarch64/dl-tlsdesc.S).

## Native C++ initialization and thread exit

On the documented native TLS models, `NativeTLS` also enables the common
C++ ABI thread-exit registrations. It does not enable emulated descriptors or
the emulated address helper; mixed native/emulated inputs still require both
options. The native test fixtures use Clang C++17 `-fno-emulated-tls`,
`-fno-exceptions` and `-fno-rtti`; initialized values, compiler-generated
nonthrowing per-translation-unit wrappers, function-local `thread_local` objects
and nonthrowing destructors are
qualified separately from ordinary C values.

| Native object ABI | Architecture | Image-owned thread-exit hooks |
| --- | --- | --- |
| ELF / Linux | amd64, arm64, 386 | `__cxa_thread_atexit`, `__cxa_thread_atexit_impl` |
| Mach-O / macOS | amd64, arm64 | `_tlv_atexit`; explicit C++ ABI registration uses the same owner |
| COFF / Windows GNU ABI | amd64, arm64, 386 | Clang `__cxa_thread_atexit`; values and guards remain in OS-owned native TLS |
| COFF / Windows MSVC ABI | amd64, arm64, 386 | Clang `.CRT$XDU`, `__dyn_tls_on_demand_init` and `__tlregdtor`, with an image-owned native guard |

Compiler-generated wrappers retain their original native guard variables and
initialization order; Go does not reproduce C++ initialization logic. Register
calls bind to the existing image-owned native thread registry. Unused archive
members defining those hooks are not extracted, and selected definitions or
external overrides cannot replace them. Manual registrations also work without
any compiler TLS descriptors, using the explicitly selected image owner.

Construction can call a leased Go callback and reenter native TLS on the same
thread. The compiler's guard controls reentry; recursive access to an object
whose construction has not finished is not made safe by the loader. Plain
constant values and independent images keep their normal thread identities.
Keep a code lease while native workers can execute constructors or other image
code. Closing a session waits for that lease; after calls stop, queued/executing
thread destructors retain the image and produce `ErrPendingThreadDestructors`
until those threads exit and cleanup is retried. Native wrappers themselves do
not add a separate loader-owned active-constructor counter.

The shared native/emulated test suite covers O0/O2 object/archive calls, dynamic
constructor order, reverse/reentrant destructors, per-image isolation, manual
registration and the 4096-entry callback limit, late thread/global registrations,
failed global initialization cleanup and pending/active thread-exit retirement.
Optional libffi tests call captured Go handlers during ordinary C-thread work,
constructors and thread exit; constructor callbacks reenter native TLS. Native
constructor retirement tests retain code leases until the workers have joined.

Native exceptions must not escape constructors, destructors or callback entries.
Additional Darwin pointer/initializer sections and cross-image TLS-variable
providers remain outside
this C++ scope. Windows GNU tests also create native C threads before loading the
carrier and verify destructor receivers and nested registrations during FLS exit,
before Windows releases that thread's native TLS storage.

## Function-local C++ TLS

Nonthrowing function-local `static thread_local` objects are qualified separately
from namespace-scope TLS on the documented native and emulated targets. Clang
retains each variable's own guard and constructor; resolving an entry or reading
an independent constant TLS value does not construct its local objects. Separate
function entries construct their own values on first use, with destructors in
reverse construction order. Both first-use orders are tested.

Windows MSVC local objects register plain `__tlregdtor` entries without requiring
a module-wide `__tls_guard` or `.CRT$XDU` list. GNU and Darwin local objects use
the existing context destructor hooks. Local values, their compiler guards and
all callbacks remain image-owned; close/retry follows the same code-lease and
pending-destructor rules above. Keep code leases through local constructors:
these compiler-generated paths do not enter the MSVC module initializer's
active-constructor counter. Recursive construction of the same object and
exceptions crossing native/Go boundaries remain outside the qualified subset.

The fixtures use 64-byte-aligned objects with zero-filled data beyond a page,
double fields and self pointers checked during destruction. O0/O2 objects and
lazy archives cover per-thread/image identity, native C threads, constructor
callback reentry, reverse/nested destruction, failed global initialization,
late thread/global registrations and owner retirement. Every qualified native
job also verifies native/emulated compiler output for eleven target triples.

## Native COFF TLS

On Windows amd64/arm64/386, `Options{NativeTLS: true}` accepts `.tls` and `.tls$`
sections from Clang MSVC/GNU C producers. Dollar suffixes are sorted lexically;
equal names retain input order. Alignment and initialized/zero bytes form a
single bounded template. COFF SECREL and ARM64 SECREL_HIGH12A/LOW12A/LOW12L use
packed module offsets. Ordinary process-address relocations to TLS variables
are rejected; call a native getter on the owning OS thread instead.

Go generates a minimal per-image PE TLS carrier in a private temporary directory.
Its leaf `DllMain` returns TRUE without calling Go or other functions; its only import is the uncalled public `kernel32.GetLastError`, with no exports or TLS callbacks. Windows
assigns its native TLS module index and provides storage for existing/new OS
threads. The raw object's `_tls_index` references image-owned storage containing
that index; i386 `_tls_array` uses the ABI's FS-relative `0x2c` constant. No private
TEB mutation, loader API or additional C/LLVM build step is used.

The carrier is created after raw relocation and protection validation, before
user initialization. Symbol/code leases retain it with the image; native
finalizers run before DLL unloading and temporary-file removal. Failed unloads
retain ownership for `Close` retry. Multiple images own distinct OS TLS indices.
The existing raw-address rules apply: stop/join native users or retain code and
pointer leases before closing a session. OS-owned thread storage is independent
of the emulated TLS registry and does not expose `LiveValues` counts.

The qualified subset includes scalar/struct values, native-pointer constants,
64-byte alignment, offsets beyond a page, input/archive extraction, missing
provider retry, C-created and pinned Go threads that predate DLL loading, new C
threads, independent modules, global lifecycle and code-lease retirement.
Native GNU/MSVC C++ nonthrowing initialization and image-owned thread-exit
registrations use the shared C++ scope above. `.CRT$XL*` loader callbacks, Windows
fibers and external TLS definitions remain unqualified. Use
the documented emulated mode
or a complete DLL for those runtime contracts. Templates remain bounded by
64 MiB and 4096-byte alignment; individual instruction offset ranges also apply.

MSVC native C++ uses a private one-byte `__tls_guard` in the native module
template. Input definitions, aliases and imported guards are rejected. A
Go-generated C entry reads its address using the ordinary Windows TLS ABI on
amd64/arm64/386. Constructors execute from a validated snapshot of `.CRT$XDU`
outside DLL entry points. The native helper marks native and emulated guards
before calling constructors, so mixed MSVC modes share one initialization pass
and permit same-thread constructor reentry. Active constructors and pending or
active destructors retain image ownership for `Close` retry. This does not
add an emulated value descriptor for the native guard.

## Emulated TLS

Compile every participating raw C translation unit with Clang `-femulated-tls`
and create the session with `Options{EmulatedTLS: true}`. This flag replaces
native TLS accesses with ordinary descriptor references and calls to
`__emutls_get_address`; it does not require new TLS relocation instructions.
The parser, archive selection, descriptor validation and relocation remain in
Go. A small native helper supplies allocation and thread-exit cleanup for both
Go and llgo hosts without private runtime imports. Cleanup itself needs no Go
callback; native destructor bodies can use retained `abi.NewCallback` entries.

| Input / runtime feature | Support |
| --- | --- |
| ELF amd64/arm64/386, Mach-O amd64/arm64, COFF amd64/arm64/386 | Explicit Clang emulated TLS in raw objects and selected archive members |
| Initialized / zero-filled variables | Independent template copy / zeroed storage for each native thread |
| Local / shared external variables | Retained image descriptors; ordinary symbol resolution and lazy archive dependencies |
| Alignment | Positive power of two; allocation budget includes alignment padding |
| Clang C++ TLS | Constant values, nonthrowing constructors and the thread-exit registrations below |
| Other native TLS models / OS TLS registration | Outside this emulated mode; plain C Mach-O TLV and the documented ELF dynamic models use `NativeTLS` above, and complete shared libraries use the OS loader |
| External emutls or thread-registration runtime overrides | Rejected; callbacks and descriptors must use the helper retained by this image |
| Windows fiber switching | Not qualified; storage and cleanup use Windows FLS |

| Object ABI / platform | Architectures | Thread-exit interface |
| --- | --- | --- |
| ELF / Linux | amd64, arm64, 386 | `__cxa_thread_atexit`, `__cxa_thread_atexit_impl` |
| Mach-O / macOS | amd64, arm64 | Clang `_tlv_atexit`; explicit C++ ABI registration also uses the same image owner |
| COFF / Windows GNU ABI | amd64, arm64, 386 | Clang `__cxa_thread_atexit` |
| COFF / Windows MSVC ABI | amd64, arm64, 386 | Clang emulated TLS `.CRT$XDU`, `__dyn_tls_on_demand_init` and zero-argument `__tlregdtor` callbacks |

This emulated MSVC ABI mode requires Clang `-femulated-tls`; native plain C TLS
uses the separate COFF mode above. Input PE TLS directories and `.CRT$XL*` loader
callbacks are not executed by the emulated mode. `.CRT$XDU` entries
are validated and snapshotted separately from global constructors. On the first
access by a native thread, the runtime marks its image-owned byte guard before
calling that image's selected thread initializers. Reentry observes that guard;
other images and threads have separate guards. The private descriptor stays in
writable image data near the code. There are at most 4096 initializer entries.
Active constructors retain the image during close retry, even before they have
registered a destructor. Plain `void(void)` and C++ ABI `void(void*)` destructors
keep their actual function types and share the same native LIFO registry.
On Windows i386, Clang GNU registers automatic destructors with `thiscall`,
while manual C registrations use `cdecl`. A shared native adapter supplies
the single context in both ECX and the stack, aligns the call, and restores its
frame. Global C++ ABI exit registration uses the same adapter. No convention
is guessed from symbol names.

macOS C++ destructor execution is qualified with LLVM 22. Older producers can
mix native TLS accesses into `-O0 -femulated-tls` output: Apple Clang 17 and
upstream LLVM 18 can leave an undefined `__tls_guard` and native TLV relocations
alongside emulated descriptors. These older mixed outputs still require both TLS options and separate producer
qualification; the native C++ tests use a qualified native producer. The native tests use `DYLIB_LLVM_CLANG` on macOS
when provided, leaving the other C fixtures on their original compiler.

Descriptors contain four native words: size, alignment, a mutable runtime word,
and a template pointer. Before publishing code or running constructors, the
linker requires correctly aligned writable non-executable descriptor storage,
an unused runtime word, bounded size/alignment and a template contained in
retained image data. Descriptors cannot overlap. There are at most 4096 distinct
descriptors and at most 64 MiB of allocation including padding per native thread,
shared with the native TLS module when both modes are enabled.
Validated size/alignment/template pointers are copied into private native controls.
Templates may contain ordinary relocated native pointers; their pointees still
need the appropriate lifetime and synchronization.

`__emutls_get_address` always binds the host-owned helper. Descriptors cannot be
supplied with `Define`/`DefineSymbol` or imported from a DLL, and the helper
cannot be replaced by an archive or host definition. The flag is explicit so a
previously unresolved TLS helper cannot accidentally select a process-global
runtime with incompatible ownership.

Values belong to native threads. A Go goroutine can move between native threads;
use `runtime.LockOSThread` around a sequence that requires stable TLS identity.
Initializers and finalizers execute on their invoking threads; callers requiring
one thread across initialization, use and close must pin that whole sequence.
This API does not turn native TLS into goroutine-local storage.

The helper uses one host-lifetime pthread key or FLS slot. Thread exit drains
native callbacks in LIFO order before releasing values. A callback is detached
before invocation and runs outside the registry lock, so callbacks can read TLS
and register more callbacks. The key continues to identify the same storage
during this dispatch. Each native thread permits up to 4096 queued callbacks
across images. Native registration returns `-1` for null functions, allocation
failure or that limit. Registration hooks require `EmulatedTLS` or the documented Unix `NativeTLS`
mode, including manual calls with no compiler descriptors; they never select a
process-global thread runtime. The address helper still requires validated image descriptors.

Session close retires calls and waits for code leases, then drains this image's
callbacks on the invoking native thread before global finalizers. Cleanup remains
on one native thread across its native calls. Global exit hooks and thread
callbacks can register each other; cleanup drains new registrations before
releasing either registry. If another thread still has queued or executing
callbacks or active MSVC thread initializers, `Close` returns
`ErrPendingThreadDestructors`, retains the image,
unwind tables, TLS values and providers, and rejects new session calls. Join
those threads and retry `Close`; completed global finalizers do not repeat.
Keep callback/value/code leases needed by destructor bodies until their cleanup
has finished. Native exceptions must not escape these callbacks.

After callbacks finish, close releases all values for the image, including
trivial values on still-live threads. Thread state retains no retired descriptor,
template or code pointer. Its small empty record survives until thread exit and
is reused by later images. Native thread exit and owner retirement share a lock;
pending and executing callbacks prevent their owner from being freed. Native
allocation failure during TLS access aborts, as in the compiler runtime; owner
creation failures return a link error before publication.

A TLS pointer borrowed by native code requires a code lease for as long as it or
the object can be used. Stop/unregister/join native users before releasing that
lease. `Close` can reclaim values after native code has stopped even if an OS
thread remains alive; it cannot make a stale borrowed TLS pointer valid.

`TestEmulatedTLSAcrossTargets` compiles all eight targets and Windows GNU variants.
MSVC fixtures inspect all three Windows targets at O0/O2 and execute in the native
Windows Go and llgo jobs. Native tests cover O0/O2 object/archive
calls, shared external descriptors, constant/zero values, 64-byte alignment,
dependency retry, repeated load/close, C-created threads, thread-exit reclamation,
constructor/exit-hook/finalizer access, retained code leases and owner retirement
before or concurrently with native thread exit. C++ cases include nonthrowing
construction, reverse destruction order, recursive registration, reentrant TLS
reads, per-image isolation, cleanup retry during queued/executing callbacks,
manual registrations and the callback budget. The optional ABI suite calls
captured Go handlers during ordinary foreign-thread work and again during
thread exit, exercising runtime reentry after native thread cleanup has begun.
Pure Go tests validate 32/64-bit descriptors and metadata without executing code.
MSVC cases also cover multi-object/archive initializer tables, per-thread shared
guards, constructor callbacks reentering Go and raw code, and retirement while
constructors are blocked. A host C fixture independently exercises the common
zero-argument initializer helper, recursive guard use and mixed destructor types.

ABI references: [Clang emulated TLS option](https://clang.llvm.org/docs/UsersManual.html#cmdoption-femulated-tls)
and [LLVM 22 descriptor/runtime ABI](https://github.com/llvm/llvm-project/blob/llvmorg-22.1.8/compiler-rt/lib/builtins/emutls.c).
The image-owned runtime here is separate from compiler-rt's process-global owner.

Native TLV ABI references: LLVM 22 [Darwin AArch64 register mask](https://github.com/llvm/llvm-project/blob/llvmorg-22.1.8/llvm/lib/Target/AArch64/AArch64CallingConvention.td) and [Darwin x86-64 register mask](https://github.com/llvm/llvm-project/blob/llvmorg-22.1.8/llvm/lib/Target/X86/X86CallingConv.td).
