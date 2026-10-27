# How DDL, ABIBridge, and llcppg complement each other

## Inspected versions

- [DDL `3bf531e`](https://github.com/Marenz/ddl/tree/3bf531e9701469ccecd5c3c698036ef4ef72362b): model, registry, linker, and ELF/OMF/COFF/archive components.
- [ABIBridge `4dfbda2`](https://github.com/lynnswap/ABIBridge/tree/4dfbda22afb9a1492e1c5c99933a9defbc5437a4): README, RuntimeArchitecture, CFunctionInvocation, CXXObjectInvocation, Architectures, and source responsibilities.
- [llcppg `6098773`](https://github.com/goplus/llcppg/tree/6098773c3116609e61c26968b83eec83e32a976c): configuration, Clang/mangling entry points, function/class generation, and sample output.
- Original local llgo reference `8ac217053d0337b7261cebc923bb6b17955bf49a` was a devel build. CI now pins a qualified merged revision and records its [resolved compiler revision](ci.md), including public C-export thread guards, indirect C varargs and subsequent C ABI fixes.

## Responsibilities

| Task | DDL | dylib-go | ABIBridge | llcppg |
| --- | --- | --- | --- | --- |
| Load `.o/.a` and fix addresses | Core responsibility | Core Go implementation | Primarily loaded/system-loaded Apple images, rather than a general object linker | Outside scope |
| Resolve exported symbols from declarations | D-specific mangling, templates, reflection | Exact linkage names; no general demangler | Swift/C++ source-level names and candidate selection | Generate actual mangled bindings from headers and Clang |
| Call the C ABI | D templates and function pointers | Caller-defined cgo/llgo adapters; optional scalar/ordinary-struct libffi | libffi C backend, variadic calls, and Apple authenticated pointers | Generate llgo declarations, types, and calling conventions |
| C++ objects and methods | Does not solve general C++ ABI | Explicit generated C facades: exact members, class values/references/pointers, base views, native virtual calls and ownership/status contracts | Supported receivers, methods, vtables, adapters, ownership | Clang-based classes, methods, layouts, and bindings; coverage depends on generator support |
| Swift/ObjC/SwiftUI | Outside scope | OS libraries with C facades and optional explicit thin Swift calls/owned values | Dedicated metadata, runtime, call, and value management | Mainly C/C++; no native Swift ABI adaptation |
| Library and handle lifetime | Libraries/modules | Sessions, code/value/callback leases, retained registration owners, optional retained references | Image leases, physical call plans, values, callback ownership | Primarily build/link-time semantics |

ABIBridge covers more than general libffi: Swift type metadata, generic witnesses, indirect results, async/throws, value lifetime, and authenticated pointers require language/platform knowledge. Its [runtime architecture](https://github.com/lynnswap/ABIBridge/blob/4dfbda22afb9a1492e1c5c99933a9defbc5437a4/Docs/RuntimeArchitecture.md) separates images, call plans, values, and callback ownership. This project uses a similar responsibility boundary without copying its Swift/C++ implementations.

Its [architecture documentation](https://github.com/lynnswap/ABIBridge/blob/4dfbda22afb9a1492e1c5c99933a9defbc5437a4/Sources/ABIBridge/ABIBridge.docc/Architectures.md) distinguishes metadata recognition, compilation, and device execution. Likewise, foreign-object parsing or relocation tests here do not establish execution on that CPU. ABIBridge's Apple focus does not automatically add Windows/Linux loaders.

llcppg commonly emits `//go:linkname F C.<mangled-name>` with `LLGoPackage` link flags, binding symbols when the host is built and linked. Its [function generator](https://github.com/goplus/llcppg/blob/6098773c3116609e61c26968b83eec83e32a976c/cl/func.go) distinguishes global/static functions from methods with `this`. Existing output does not establish runtime rebinding support, but its header semantics and type information are suitable inputs for dynamic adapter generation.

## Remaining gaps

The comparisons below use the pinned source snapshots, not all historical releases or project plans. DDL has not been built as part of this project's validation. The optional [ABIBridge native-call adapter](abibridge.md) builds and executes its pinned native core; broader ABIBridge capabilities below remain upstream evidence. dylib-go's own [support matrix](support.md) establishes its execution evidence.

### Compared with DDL

dylib-go implements the general object-loading path: identify content, model sections and symbols, extract archive dependencies, relocate, bind native entries, and release the image. Its eight native Go targets and six llgo targets include modern 64-bit ELF/Mach-O/COFF and i386 ELF/COFF subsets. This is not yet full feature parity with DDL's library and plugin abstractions.

| Area | Evidence in the DDL snapshot | dylib-go today / remaining work |
| --- | --- | --- |
| Legacy formats and containers | `DefaultRegistry` enables OMF object/library, ELF object, and InSituMap loaders. COFF, ar, D `.ddl`, and InSituLib registrations are commented out; ELF64 is explicitly rejected | No OMF loader or host MAP import; D `.ddl` is excluded. OMF is a legacy-format gap. Modern COFF/ar/Mach-O/ELF execution is covered by this project's tests; the historical all-format plan is not a completed DDL baseline |
| Extensible loaders and metadata | `LoaderRegistry.register` composes loaders; libraries expose attributes and optional version-checking code | Parsers are internal and selected by content. The optional `module` package supplies explicit JSON/Go dependency manifests and exact declared versions; public loader registration and automatic provider discovery remain |
| Dependency discovery | `PathLibrary` searches directories and translates D namespaces into paths; the linker can consult a registry for dependencies | Explicit inputs, lazy archive extraction and retained session-to-session function/data imports through `DefineSymbol`. Versioned manifests and optional explicit-directory catalogs discover exact-version provider graphs and retain their lifetimes; automatic system search remains |
| Lazy providers and compilation | The separate `xf/linker` layer has object/source providers, compiler configuration, and symbol-triggered library loading | Archive members are lazy; optional LLVM compilation is explicit. Retained symbol providers couple session lifetimes, while symbol-triggered compilation/loading remains separate work |
| Library/module lifecycle | `DynamicLibrary` exposes unloading; `xf/linker/LazyLinker.unload(source)` removes a source from linker indexes | A successful session is sealed, and `Close` releases the entire image. `module.Manager` replaces explicit graphs with caller-defined state migration and drains old scopes/code leases. Arbitrary in-place removal/rebinding remains unsupported; DDL's index-removal code alone is not evidence of safe hot reload |
| Typed language discovery | D mangling/demangling, template symbol binding, ClassInfo, class construction, and subclass queries | Exact symbols and explicit C signatures only. Declaration-driven C/C++ binding is a useful extension; D class reflection and runtime compatibility remain intentionally excluded |
| Initialization | `Linker.initModule` visits imported D ModuleInfo records and calls constructors; destructor registration is a TODO | Modern ELF/Mach-O/COFF tables, legacy ELF tables, Mach-O initializer offsets and session-owned C/C++ exit registrations; no D ModuleInfo. GNU ELF split CRT startup fragments are supported; explicit Clang emulated TLS and Unix native TLS with the documented C++ wrappers/thread-exit hooks are implemented; remaining TLS models and other language runtime registration remain |

Source anchors: [default registry](https://github.com/Marenz/ddl/blob/3bf531e9701469ccecd5c3c698036ef4ef72362b/ddl/DefaultRegistry.d), [ELF limits](https://github.com/Marenz/ddl/blob/3bf531e9701469ccecd5c3c698036ef4ef72362b/ddl/elf/ELFBinary.d), [loader registry](https://github.com/Marenz/ddl/blob/3bf531e9701469ccecd5c3c698036ef4ef72362b/ddl/LoaderRegistry.d), [path library](https://github.com/Marenz/ddl/blob/3bf531e9701469ccecd5c3c698036ef4ef72362b/ddl/PathLibrary.d), [lazy linker](https://github.com/Marenz/ddl/blob/3bf531e9701469ccecd5c3c698036ef4ef72362b/xf/linker/LazyLinker.d), [compiler/provider setup](https://github.com/Marenz/ddl/blob/3bf531e9701469ccecd5c3c698036ef4ef72362b/xf/linker/DefaultLinker.d), [typed library access](https://github.com/Marenz/ddl/blob/3bf531e9701469ccecd5c3c698036ef4ef72362b/ddl/DynamicLibrary.d), and [module initialization](https://github.com/Marenz/ddl/blob/3bf531e9701469ccecd5c3c698036ef4ef72362b/ddl/Linker.d).

Separately, broader raw-object compatibility still needs Mach-O coalescing, remaining relocations, remaining native TLS models, nonstandard import tables, and additional native unwind formats. Optional C frame registration and the Linux C++ DWARF and macOS C++ DWARF/compact exception subsets are implemented. ELF COMDAT, COFF selections 1–7/weak aliases, bigobj/extended relocations, and short/GNU long import libraries are implemented. The remaining items are dylib-go gaps, not a claim that DDL completely implemented them. The host OS supplies initialization, TLS, dependencies, and unwind metadata for complete shared libraries; it does not adapt an incompatible call signature or language value.

### Compared with ABIBridge

ABIBridge provides native language calls and runtime interpretation above Apple image loading. It does not replace this project's general raw `.o/.a` linker. Its [package requirements](https://github.com/lynnswap/ABIBridge/blob/4dfbda22afb9a1492e1c5c99933a9defbc5437a4/README.md) cover Apple platforms and a Swift/Xcode toolchain; that scope does not establish Linux/Windows ABI support.

| Area | ABIBridge in the inspected snapshot | dylib-go today / remaining work |
| --- | --- | --- |
| Dynamic C calls | Explicit signatures, variadic tails with C promotions, and reusable prepared call interfaces; no fixed argument-count limit | Host C ABI plus Windows 386 stdcall/fastcall; scalars, ordinary C structs, concrete variadic shapes, and owned reusable CIFs. Argument storage follows the concrete signature length; additional aggregate forms and conventions remain |
| Declaration resolution | Source-level Swift/C++ declarations, overload selection, image scopes, and shared metadata indexes | Exact linkage names; Go-style parsing supplies types but does not discover native prototypes. Header/declaration manifests or generated adapters can add validation |
| C++ receivers and virtual entries | Direct methods, owned/borrowed receiver storage, explicit base views, and adapter-described virtual tables/authentication | Explicit `compiler/cxx` manifests generate constructor/member/delete C facades, public multiple/virtual base views, native virtual dispatch, class values/references/pointers, exact native function pointers, retained Go callbacks and caught exceptions. Discovered declarations remain; nontrivial C++ values also require compiler adapters in ABIBridge |
| Swift native ABI | Supported native values, metadata/witnesses, generic binding, direct/indirect results, async/throws, and explicit value operations | Optional pinned-core adapter executes explicit synchronous thin scalar/pointer/trivial/indirect Swift calls. C facades separately cover opaque object retain/release and leased observers. Complete metadata providers and owned Copyable storage support initialization, copy/destroy and retained pointer access. Explicit guaranteed managed arguments and owned results use compiler-confirmed layouts. Generics, tasks/errors, contexts and devices remain |
| Objective-C and SwiftUI | Runtime dispatch, ownership, method hooks, and a separate SwiftUI adapter product | ObjC++ C facades explicitly manage ARC objects and leased Go observers; no built-in message-dispatch, ARC, or UI adapter. Raw registration objects are rejected and thread contracts remain adapter-specific |
| Native callbacks and replacement | Native callback entry owners, supported Swift closures, import/method/virtual hooks, and publication/retirement contracts | Fixed C callback entries support scalars, ordinary structs, Go captures, and C-created threads, with leases and explicit unregister/join/close. Explicit registration owners couple code/value/callback leases with unregister/join and cleanup retry. No import/method/virtual hooks, Swift closures, or inferred native dependency ownership |
| Values, caches, and concurrency | Image leases, receiver/value owners, bounded physical call-plan caches, and per-call storage allow reuse of prepared handles | Session lifetime guards, native temporary copies and independent leased scalar/record/array storage are implemented. Session and prepared-plan calls support concurrency and callback reentry; close retires owners and waits for active uses. Equal logical signatures share session-owned plans; compatible distinct signatures share native CIFs/layouts and bounded pools with independent validation and retirement. Registration owners acquire and couple code/value/callback leases with explicit stop operations. Owned Swift values retain complete metadata and use copy/destroy witnesses; broader language-specific ABI normalization remains |
| Authenticated Apple ABIs | arm64e pointer authentication, compiler-specific virtual-call schemas, and documented device probes | arm64e execution is rejected. Supporting it needs authenticated callable pointers and matched toolchains/runtime backends; normal arm64 execution is not evidence of arm64e compatibility |

The [C invocation guide](https://github.com/lynnswap/ABIBridge/blob/4dfbda22afb9a1492e1c5c99933a9defbc5437a4/Sources/ABIBridge/ABIBridge.docc/CFunctionInvocation.md), [C++ object guide](https://github.com/lynnswap/ABIBridge/blob/4dfbda22afb9a1492e1c5c99933a9defbc5437a4/Sources/ABIBridge/ABIBridge.docc/CXXObjectInvocation.md), [Swift invocation guide](https://github.com/lynnswap/ABIBridge/blob/4dfbda22afb9a1492e1c5c99933a9defbc5437a4/Sources/ABIBridge/ABIBridge.docc/SwiftFunctionInvocation.md), and [runtime architecture](https://github.com/lynnswap/ABIBridge/blob/4dfbda22afb9a1492e1c5c99933a9defbc5437a4/Docs/RuntimeArchitecture.md) describe these contracts and their limits. ABIBridge also requires explicit native layouts, ownership, and adapters for unsupported representations; it is not automatic adaptation of every C++ or Swift declaration.

ABIBridge reports routine runtime tests on macOS and iOS Simulator with selected Xcode versions, separate public/native consumers, and compilation checks for other Apple device SDKs. Its [architecture record](https://github.com/lynnswap/ABIBridge/blob/4dfbda22afb9a1492e1c5c99933a9defbc5437a4/Sources/ABIBridge/ABIBridge.docc/Architectures.md) documents separate arm64e device probes, arm64e.x1 compilation without runtime verification, and protected import pages that could not be mutated. These records do not establish every device/ABI combination or universal hook support, and are not dylib-go test results.

### Priorities for this project

1. Broaden general raw-object use: Long import tables, Mach-O coalescing and remaining relocations, remaining lifecycle forms, additional unwind formats/providers, then remaining native TLS models and other language runtime registration. Clang emulated TLS and the documented Unix native models share image-owned storage and nonthrowing C++ thread-exit registration. Modern lifecycle tables and session-owned C/C++ exit registrations are implemented. Keep parsing and linking in Go.
2. Broaden dynamic C interfaces: additional aggregate forms, calling conventions, and generated declaration validation. Fixed C callbacks with explicit retirement, foreign-thread integration, variadic promotion, and reusable call plans are implemented. Use llgo or small native bridges where Go cannot emit the required ABI entry points.
3. Extend optional declaration/provider packages: generated C/C++ adapters, versioned dependency manifests, exact-version provider catalogs, and managed replacement with state migration and code retirement are implemented. Version negotiation and automatic system search remain. Preserve the current explicit core API.
4. Extend the optional ABIBridge thin-Swift adapter beyond explicit guaranteed managed arguments/owned results to additional native call forms. Keep platform/runtime/device qualification separate from trivial-value invocation.

OMF could be added separately if legacy object compatibility is needed; it is not part of the current roadmap. D `.ddl` and D runtime compatibility remain outside scope. See the [roadmap](roadmap.md) for target-specific work.

## Possible integration

```text
Headers / Clang / llcppg                 Apple runtime / ABIBridge adapter
  types, prototypes, mangled names         native Swift/ObjC adaptation
                  \                       /
                 explicit declarations and ownership
                              |
             dylib-go Session + Resolve + Bind
                 /                         \
         Go object linker                OS library loader
                 \                         /
       caller-defined adapter / optional scalar/struct libffi call
```

An optional [Clang declaration generator](clang.md) now supplies target-qualified
scalar/typedef/record/pointer signatures for dynamic binding, including compiler/
backend layout validation, array members, typed struct pointers and function-pointer
parameter/result prototypes. Generated struct callbacks and native factories are
executed with Go/llgo. Fixed scalar/record/native-pointer cdecl methods can also
be generated for direct llgo calls without libffi, with Clang/llgo storage checks.
Fixed/variadic cdecl function-pointer parameters/results retain typed native prototypes and
explicit code ownership; managed captures and foreign-thread callback entries
still use owned adapters. Direct llgo methods support concrete variadic tails with
C promotions without libffi. Explicit [C++ class manifests](cxx.md) now render
small C++17 facades whose C headers reuse these adapters, including native
constructor/member/destructor calls and exception status codes.

`CgoSource` also generates typed scalar/record/native-address methods for Go and
llgo without libffi. Record conversions copy fields into independently checked C
storage, including nested arrays and large returns. Explicit packed/aligned records and bitfields use
ordinary transport records across Go/C and compiler conversions inside C, with
layout checks and no libffi dependency. Bitfield byte masks are compiler-probed
and checked before binding. Unnamed/zero-width fields preserve native allocation
boundaries without becoming public Go values. Complete struct pointees use
opaque native addresses with saved C prototypes, including returned pointers and
native function-pointer forwarding; Go struct pointer casts are not inferred.

The following integrations remain proposals:

1. Add an optional dynamic mode to llcppg. Preserve its generated types, names, and layouts, and generate binding fields owned by a session. Known signatures can use caller-defined `//llgo:type C` function types through `Symbol.WithAddress`. The loader need not duplicate header parsing.
2. Define a declaration manifest with target triple, C/C++ ABI, linkage name, arguments/results, record size/alignment, `this` and hidden result parameters, ownership, and exception boundaries. Existing `abi.Signature` covers concrete fixed/variadic scalar and ordinary C struct signatures with supported native C conventions; it does not express these additional language contracts.
3. Extend the implemented `compiler/cxx` C facades for complex C++ classes. Fixed scalar/native-address constructors and members already use a matching compiler for object calls, selected public base views, virtual dispatch and exception status codes. Class values/references/pointers use explicit native type selections; exact native function pointers, retained Go callbacks and named C++ lambda/`std::function` adapters are implemented. Class/member discovery beyond selected base names remains. Complete libraries own native runtime and exception registration.
4. The optional [ABIBridge adapter](abibridge.md) uses a small C API for explicit thin Swift calls. The separate [Apple C facades](apple-facades.md) establish opaque-handle/retain/release/observer boundaries. Broader managed Swift/ObjC values, runtime dependencies and main-thread requirements still need their own adapters and tests.
5. Use `abi.NewCallback` and its leases for fixed C callback entries. An integration must separately own the registering library, remove native registrations, and join callers before releasing the lease. Swift closures, import/method hooks, and language exception adaptation still need dedicated adapters. See [callback lifetime](callbacks.md).

C/C++ SDKs with headers, plugin engines, and numerical libraries are natural applications for generated adapters. SwiftUI, Swift generics/async, and Objective-C dispatch require dedicated language adapters and are outside current support.
