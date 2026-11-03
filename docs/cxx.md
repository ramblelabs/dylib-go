# Generated C++ class facades

`compiler/cxx.Generate(header, classes)` renders a C header, C++17 source, and
ordered C export names from an explicit manifest. Generation runs entirely in
Go and invokes no compiler. The matching native C++ toolchain compiles the
source against the original class header. Complete OS libraries supply C++
runtime dependencies and exception registration.

The [generation example](../examples/cxx/generate.go) selects
`example::Counter`, its constructor, `add(int32_t)`, destruction, and a derived
class with a compiler-generated base view. The
[executed native fixture](../compiler/cxx/testdata/counter.hpp) supplies the class.
Write `Facade.CHeader` and pass its path and `Facade.Functions` to
`compiler/clang.Parse`. Its result supports:

- `CgoSource` for typed Go/llgo calls without libffi.
- `LLGoSource` for typed C function-pointer calls on qualified llgo targets.
- `ForHost` and `Session.Bind` for optional libffi calls.

`Facade.CXXSource` embeds its C declarations and includes the original C++ header.
Use a forward-slash include path; quoted paths with spaces are accepted. Include
directories, defines, ABI flags and runtime linkage belong to the producer's
compiler. The original header and any linked C++ library must match that compiler
and ABI. Generation does not discover class/member declarations or demangle
symbols. Optional compiler queries can discover base relations among selected
type names, as described below.

## Manifest and C boundary

| Selection | Generated C entry |
| --- | --- |
| `Constructor{Symbol, Args}` | Fixed C arguments, followed by `void **output`; the slot must initially be null |
| `Method{Name, Symbol, Signature}` | `void *handle`, fixed C arguments, and a result pointer for non-void results |
| `Method.Const` | An exact const-qualified member pointer |
| `Method.Static` | An exact static function pointer; no receiver argument |
| `Method{Name: "operator()"}` | An exact call operator on a named callable type; `Const` selects a const call operator |
| `ObjectArgs` on a constructor/method | Pointer transports converted to selected named native pointers, class references or copied values |
| `Method.ObjectResult` | A pointer transport carrying a borrowed pointer/reference, or a newly allocated owned value |
| `FunctionArgs` on a constructor/method | Pointer transports converted to exact fixed/variadic native function pointers |
| `Method.FunctionResult` | A pointer transport carrying a borrowed native function pointer |
| `Base{Name, Symbol}` in `Class.Bases` | `void *handle, void **output`; a borrowed public base view |
| Optional `Class.Delete` | A `void **handle` slot consumed and cleared before native `delete`; omit for borrowed/static-only selections |

All C exports return `int32_t`, use fixed cdecl on Windows 386, and have explicit
export visibility. C++ definitions are `noexcept`, with catch handlers converting
native exceptions into status codes. Export names must be distinct C identifiers. Class names can
be namespace-qualified identifiers or aliases; template specializations can be
named by an alias in the original C++ header. A manifest can contain up to 256
exports, matching the declaration generator's selection limit.

Supported values are `bool`, fixed-width signed/unsigned 8/16/32/64-bit integers,
`float`, `double`, `void *`, and void method results. The C++ method pointer must
match these exact native C types; equal byte widths alone do not make distinct
C++ overload types interchangeable. Constructors use normal overload resolution
for those argument types. `Const` and `Static` select their corresponding member
forms; the compiler rejects missing/private members or mismatched signatures.

Ordinary and virtual calls stay inside C++. `Class.Bases` selects public,
unambiguous base types. Generated code assigns the typed derived pointer to a
typed base pointer, so the native compiler supplies every address adjustment,
including secondary bases and shared virtual bases in a diamond. This implicit
conversion rejects downcasts, unrelated types, private bases and ambiguous
inheritance. No C++ standard-library traits or runtime discovery are required.
A native factory can also supply an already adjusted `void *` base view.
Virtual members dispatch through native member pointers; Go performs no base
offset, vtable or member-pointer arithmetic.

Base exports require a non-null output slot and clear it before checking the
receiver. A null receiver returns `InvalidArgument`. Use separate output storage
so a borrowed view does not overwrite the owning handle. The receiver must
already have the selected native C++ type; exports do not perform RTTI checks.
A view neither allocates nor retains an object and expires when its owner is
destroyed. Destroy the original owner once; do not independently delete its
borrowed views. Deleting through a base instead requires a separate native
ownership contract, including an appropriate virtual destructor.

## Compiler-discovered base relations

`DiscoverBases(ctx, header, names, DiscoveryOptions)` returns ordered
`BaseRelation{Derived, Base}` entries among explicitly selected type names.
The [executed example](../examples/cxx/discover.go) assigns C export names and
adds these entries to each derived `Class.Bases`. Selected constructors, members
and destruction remain an application manifest. The existing generator then
emits the same compiler-adjusted base conversions as a manually selected view.

The optional Clang-compatible C++ compiler evaluates class identity, inheritance
and pointer convertibility. Results include public unambiguous transitive and
virtual bases. Private/protected and ambiguous conversions are excluded, as are
canonical self aliases. Accessible aliases to template specializations work
without parsing template expressions in Go. Selected names must denote complete
non-cv class types; scalar/pointer/cv aliases, incomplete or inaccessible types
return errors. No Go base offsets, vtable interpretation or runtime RTTI are used.

`DiscoveryOptions.Compiler` defaults to `clang++`. `Target` defaults to that
compiler's target, independently of the Go process architecture. Match `Target`
and preprocessing `Flags` to the facade producer, especially when headers select
inheritance conditionally. Relative includes retain the original header path.
The query does not establish that a resulting native library matches the host ABI.

Queries accept 1–32 distinct qualified names of at most 256 bytes each. The header,
generated query and returned AST have 16 MiB, 1 MiB and 32 MiB limits; compiler
diagnostics are bounded. The supplied context cancels the compiler process. Any
validation, compiler or metadata failure returns no relations. This is selected
type inspection. Separate namespace and member queries are described below.

## Status and ownership

| Status | Meaning |
| --- | --- |
| `cxx.Success` / `DYLIB_CXX_SUCCESS` (0) | Operation completed |
| `cxx.InvalidArgument` / `DYLIB_CXX_INVALID_ARGUMENT` (1) | Null receiver/output slot or an already occupied constructor/value-result output slot |
| `cxx.Exception` / `DYLIB_CXX_EXCEPTION` (2) | A native C++ exception was caught, or a custom allocation returned null |

Non-void method outputs are zeroed before checking the receiver or invoking C++.
A failed constructor leaves its initially empty slot empty. An occupied slot
is preserved so a second construction cannot overwrite an owned reference.
Delete clears its slot before invoking the destructor, including a destructor
that throws; deleting an empty slot is inert. A null slot address is invalid.
These rules do not roll back mutations a native method performed before throwing.
Exceptions from `noexcept` violations still terminate under the C++ contract.

## Class arguments and results

The executable [object manifest](../examples/cxx/objects.go) uses `ObjectArg` to
select a zero-based `abi.Pointer` argument and its `ObjectType{Name, Kind, Const, Volatile}`.
`ObjectPointer` accepts null. `ObjectReference` requires a non-null object address;
`ObjectRValueReference` requires a non-null address and passes the existing
object as an xvalue. `Const` and `Volatile` select the exact named pointee/referent
qualification.
`ObjectValue` copies the native object inside C++, with no Go layout or byte copy.
Constructors apply normal overload resolution; a value selection supplies an
explicit native temporary. Methods still use an exact member/function pointer.

`ObjectResult` requires `abi.Pointer` as the C result transport. Pointer/reference
results borrow the native API's lifetime; reference addresses bypass overloaded
`operator&`. Failed borrowed results clear the output slot. Native const results
retain their exact cv-qualified signature, but the opaque Go address cannot enforce read-only
access. Receivers and argument addresses must already have their selected native
type; the facade does not perform RTTI checks or infer base offsets.

A value result allocates a separate native owner. Its output slot must initially
be null; occupied slots are preserved and return `InvalidArgument`. Allocation,
copy and method exceptions return `Exception` without publishing an owner.
Value arguments/results require accessible `noexcept` destructors: a throwing
temporary destructor could otherwise invalidate publication and cleanup. Copy
constructors may throw. C++17 prvalue results also support noncopyable/nonmovable
classes through guaranteed copy elision. Destroy each returned owner using its
native type before releasing the provider; an address slot does not own its object.

The [executed move manifest](../examples/cxx/moves.go) selects a noncopyable move
constructor, an rvalue argument, all twelve pointer/lvalue/rvalue cv overloads,
and borrowed cv-qualified pointer/rvalue results. A reference result aliases its
native owner; it does not create another owner. Reference addresses still bypass
overloaded `operator&`. Moving does not clear or destroy the source handle: the
native operation defines its post-call state, and the application destroys both
owned sources and destinations explicitly. Move exceptions follow the existing
output/publication rules and do not imply native mutation rollback.

Named scalar and array rvalue references use the same opaque-address path. The
fixture mutates an `abi.NativeValue` scalar and borrows a native array without
copying C++ object bytes into Go. Generated cgo/direct llgo and dynamic libffi
callers execute optimized/unoptimized libraries, including source reuse after
moving, failed moves, occupied destinations, nulls and destructor counts.
`Const`/`Volatile` flags on value selections remain unsupported. Volatile
qualification preserves native accesses and does not add synchronization.

Delete exports are optional, allowing static-only classes with private destructors
and borrowed receivers. Omitting an export does not infer an alternative cleanup
operation for constructor/value results. Supply that native ownership contract
explicitly. A manifest must select at least one C export.

Objects and returned native addresses remain caller-owned. Pointer results are
borrowed unless the original API supplies an ownership contract. Retain code
leases for the entire object lifetime, destroy every owned object before releasing
its provider, and stop/join any asynchronous native users before cleanup. A
`Registration` can couple code, output storage and callback leases with an
explicit destruction operation. `abi.NativeValue` owns a handle slot's bytes,
not the C++ object whose address it stores. See [registration ownership](registrations.md).

## Named native pointees

`ObjectType{Kind: ObjectPointer}` also selects non-class pointees. The executable
[native pointer manifest](../examples/cxx/pointers.go) uses the same `ObjectArgs`
and `ObjectResult` fields for scalar, character, void, enum, fixed-array, union
and pointer aliases. Define their names in the original header; keyword types
such as `char` and `void` need named aliases. All C transports remain `void *`.
The C++ compiler checks exact pointer types in method signatures and performs
all dereferences, arithmetic and union-member operations.

| Native alias | `Const` | Selected native pointer |
| --- | --- | --- |
| `using Number = int32_t` | false / true | `int32_t *` / `const int32_t *` |
| `using Character = char` | true | `const char *` |
| `using Opaque = void` | false | `void *` |
| `using Slot = int32_t *` | false / true | `int32_t **` / `int32_t *const *` |
| `using Row = int32_t[2]` | false / true | `int32_t (*)[2]` / `const int32_t (*)[2]` |
| Named enum / union | false / true | Pointer to the named type / its const-qualified type |

`Const` and `Volatile` qualify the **named pointee**; they do not recursively qualify the
elements of a pointer alias. Names and argument/result pointer levels must match
the native API. The compiler rejects incompatible element types, removed const
qualifications and array/pointer-level mismatches. Nulls reach the selected API;
dereference validity and accepted ranges remain its contract.

No Go pointee layout or union active-member representation is inferred. This
does not add by-value union support to dynamic `Session.Bind`; native code owns
the union in the executed fixture. Pointer returns are borrowed unless the
original API defines ownership. Retain a provider lease for static native data,
and retain any separate storage owner for externally allocated data. Const
addresses remain opaque to Go and do not enforce read-only access. Do not delete
borrowed scalar/array/union pointers with a class destruction export. The
[dynamic execution test](../compiler/cxx/pointer_dynamic_test.go) verifies that a
borrowed native address remains usable through a code lease during session
retirement, then releases the lease before library cleanup.

## Native function pointers and Go callbacks

The executable [function manifest](../examples/cxx/functions.go) uses
`FunctionArg{Index, Signature}` on constructors/methods and
`Method.FunctionResult` to select exact native function-pointer types. Each
selected slot uses `abi.Pointer` as its C transport. Function/object selections
cannot overlap, and an output cannot select both result forms. Signatures accept
scalar/opaque-address default/cdecl arguments and results, including void
results. For a variadic function pointer, set `Variadic` and include only its
fixed prefix: `len(Args)` must equal `FixedArgs`, with at least one fixed argument.
The descriptor names a pointer type, not a concrete call shape; appended tails
are rejected. The native compiler rejects mismatched function prototypes. Null
function pointers pass through, so their validity follows the original C++ API.
Failed borrowed function results clear the output slot.

Transport uses function/object pointer conversions supported by the qualified
native compilers and targets. Returned functions borrow their provider's lifetime;
keep its symbol/function lease until the final call. A Go function value is not a
native entry: use a generated C entry or `abi.NewCallback` with the selected
signature. Dynamic Go callbacks require the optional libffi backend. The generated
cgo/direct llgo transport itself needs no libffi when using existing native entries.

The [executed variadic function manifest](../examples/cxx/variadic_functions.go)
passes and returns native ellipsis entries through constructors, methods,
conversion operators and an ellipsis tail. C++ supplies each call's concrete tail
and default promotions. Generated cgo/direct llgo callers and dynamic libffi calls
execute the same fixed C facade. Native entries borrow their code provider, which
may outlive a destroyed wrapper object; captured state still needs its own owner.
This does not create Go variadic callbacks. Use `abi.NewCallback` only with a
supported fixed callback signature.

Retained callbacks need callback and provider leases for their entire native
lifetime. `Registration` can couple those leases, handle storage, and native
unregister/join or destruction. The [executed dynamic test](../compiler/cxx/function_dynamic_test.go)
constructs a C++ object retaining a captured Go callback, reenters its C++ method,
and unregisters/destroys it while session, storage, and callback close are waiting.
Callbacks return Go errors through `Callback.Err` with a zero native return;
these are distinct from caught C++ exceptions. Do not unwind a Go panic or C++
exception across a C callback entry. Native callback ownership and threading rules
remain explicit.

## Captured lambdas and std::function

The executable [callable manifest](../examples/cxx/callables.go) selects
`Method{Name: "operator()", Signature: ...}` on named callable types. Define aliases
in the original header, such as `using Function = std::function<int32_t(int32_t,
int32_t)>` or `using CapturedAdder = decltype(makeAdder(0))`. Class names remain
qualified identifiers; arbitrary template expressions and other operator names
are not accepted. `operator()` cannot be static under the selected C++17 standard.
The native compiler checks its exact argument/result types and const qualification.
A mutable lambda requires `Const: false`; `std::function::operator()` is const even
when its stored target mutates state.

Factories return callable aliases through `ObjectResult{Kind: ObjectValue}`.
This allocates a native owner without requiring a default constructor. C++17
prvalue construction supports move-only captures; explicit reference constructors
can copy copyable closures and `std::function`. Copy semantics follow C++: scalar
captures are independent, while pointers or shared owners retain their usual
sharing behavior. The facade does not interpret the capture layout. Empty
`std::function` calls return `Exception` for `std::bad_function_call` and clear the
result; null receivers return `InvalidArgument`.

A `FunctionArg` constructor can initialize `std::function` from an existing native
entry, including an `abi.NewCallback` entry for a captured Go handler. The native
`std::function` copy does not retain the Go callback or its code provider. Keep
callback/provider leases until **all** native copies are destroyed. The
[dynamic callable test](../compiler/cxx/callable_dynamic_test.go) couples these
resources with `Registration`, invokes the capture during owner retirement, then
destroys the callable before releasing its leases. It also exercises native
entries and C++ captures; freshly generated cgo/direct llgo callers execute those
native cases without libffi. Standard-library callables require the native C++
SDK/runtime matching their producer.

## Receiver cv/ref qualifiers

`Method.Const` and `Method.Volatile` select the exact receiver cv-qualification.
`Method.Ref` accepts `RefNone` (the default), `RefLValue` for `&`, or `RefRValue`
for `&&`. All twelve cv/ref combinations use an exact native member-pointer
type, including named `operator()` selections. Static methods cannot have
receiver qualifiers. The native compiler rejects missing or mismatched overloads.

An `&&` facade casts the existing receiver to the corresponding cv-qualified
xvalue before its member-pointer call. This preserves native virtual dispatch,
including through a borrowed secondary base. The handle stays owned by the
caller: `&&` does not itself destroy an object or specify its post-call state.
An operation may move data out, mutate the object or throw according to its
native contract. Keep the object and provider alive, observe that contract and
eventually destroy the owned object. `volatile` preserves native volatile
accesses; it does not add thread synchronization.

The [executed receiver manifest](../examples/cxx/receivers.go) selects the twelve
overloads, a volatile rvalue call operator, void mutation, a qualified virtual
base call and a noncopyable owned result. Generated cgo/direct llgo callers and
dynamic libffi callers exercise `-O0`/`-O2` libraries, output clearing on errors,
exception translation, post-move receiver state and explicit destruction.

## Concrete variadic member calls

Set `Method.Signature.Variadic` and `FixedArgs` to select a native ellipsis
prototype. `Signature.Args` contains the fixed prefix followed by one concrete
scalar/pointer tail. `FixedArgs` excludes the receiver and must be at least one;
it may equal the argument count for an empty tail. Generate a separate export for
each concrete call shape.

The generated C export remains fixed, including all tail arguments and the final
result slot. The exact native member/function pointer contains only the fixed
prefix and `...`. The C++ compiler performs [default argument
promotions](https://eel.is/c++draft/expr.call): small integers and bool become
`int`, and float becomes double. The caller must match the native function's
expected count and promoted types; the facade cannot inspect a `va_list` contract.

The [executed manifest](../examples/cxx/variadics.go) covers static, cv/ref-qualified,
call-operator and virtual calls through a secondary base. Tests execute empty tails,
integer/floating register overflow, mixed 32/64-bit widths, native object and fixed
function-pointer arguments, borrowed pointer results, void results, exceptions,
nulls and owner destruction. Generated cgo and direct llgo callers need no libffi;
optional dynamic callers bind the same fixed C exports.

Named object references/values remain available in the fixed prefix. Object
arguments in the tail must use `ObjectPointer`; class values and references are
rejected because passing nontrivial objects through ellipsis is not a portable
contract. Native function-pointer descriptors contain only their fixed prefix,
while the enclosing method signature contains its concrete tail. Variadic
constructors and zero-fixed-prefix ellipsis signatures are not selected by this
manifest. Go variadic callbacks are still unsupported.

## Explicit conversion operators

`Method.Conversion` selects a non-void conversion operator to the exact native
result described by `Signature.Result`, `ObjectResult` or `FunctionResult`.
Leave `Name` empty, supply no arguments and keep `Static` false. `Const`,
`Volatile` and `Ref` select receiver qualifiers as for ordinary members.
Explicit operators participate because the facade directly invokes a selected
member pointer. The native compiler checks the result type, qualifiers and access.

Selection uses `operator` followed by the validated native result type, rather
than applying an ordinary cast to the receiver. A conversion to one integer or
floating type cannot silently satisfy a descriptor for another type through a
standard conversion chain. The existing scalar/native-address transport limits
and named object/function-pointer result contracts still apply. This does not
infer an ABI, ownership or a conversion target from native type spellings.

The [executed conversion manifest](../examples/cxx/conversions.go) covers bool,
fixed-width integer, floating, native pointer and function-pointer results,
borrowed lvalue/rvalue references, and a noncopyable owned class result. A native
virtual conversion is also called through a secondary base. Pointer/reference
results remain borrowed; value results require a separate destruction operation.
An rvalue receiver can be moved from according to its native operation; its
original handle remains caller-owned. Existing exception, null, output clearing
and occupied-slot rules apply. Native cgo/direct llgo and dynamic libffi tests
execute `-O0`/`-O2` libraries and verify ownership counts after destruction.

## Namespace class discovery

`DiscoverClasses(ctx, header, namespace, DiscoveryOptions)` lists accessible
complete, non-cv class names and aliases in one explicit namespace. Named child
namespaces, inline namespaces, public nested types, reopened declarations,
typedefs, named class-template specializations and lambda aliases participate.
Namespace aliases retain the caller's spelling. Results follow the first
declaration order and include declarations from relative/transitive includes.
Target and preprocessing flags use the same original-header compiler path as
the other discovery APIs.

The query matches the selected namespace and its reopened definitions by
declaration ID within one compiler invocation. A namespace alias with a different
target name can require a second filtered query; IDs are resolved afresh.
One final bounded compiler probe tests each candidate's class status,
completeness and non-cv object-pointer conversion. Incomplete classes, unions,
scalar/pointer/reference/function aliases therefore do not become bindings.
Anonymous namespaces, function-local types, unspecialized class/alias templates,
inherited names and using-declaration/directive imports are excluded. Empty
namespaces return no names without an error.

There are at most 256 candidate names, each at most 256 bytes, and at most three
compiler queries. The existing 32 MiB AST, node/depth, diagnostic and cancellation
bounds apply. A budget or compiler error returns no partial list. Select returned
names for `DiscoverMembers` or `DiscoverBases`, then supply explicit ABI,
ownership and export names to `Generate`. The
[native integration test](../compiler/cxx/lambda_members_dynamic_test.go)
discovers a lambda alias from its namespace, selects its member and executes the
generated facade and destruction through Go/llgo at `-O0` and `-O2`.

## Selected member discovery

`DiscoverMembers(ctx, header, name, DiscoveryOptions)` uses optional Clang C++17
queries to return `ClassDeclaration` for one explicit complete, non-cv class name
or alias. `Callables` lists directly declared public constructors, destructors,
ordinary/static methods, operators and conversions in declaration order. Implicit,
inherited, non-public and function-template declarations are excluded. Class
template specializations can be selected through named aliases. Named lambda
aliases such as `decltype(factory(0))` and `decltype(variable)` can be resolved
through their original declaration references, including forwarding factories,
static/member factories and variables initialized by a factory. The closure's
[call operator has public access](https://eel.is/c++draft/expr.prim.lambda.closure#5)
even though its compiler record has no name.
Generic lambda operators remain function templates and are excluded.

`NativeType` preserves the compiler's C++ spelling in its declaring class context,
including qualifiers and calling convention attributes. Parameters retain names,
native types and default-argument presence. Deleted declarations are reported for
diagnostics; they cannot be invoked. `Defaulted`, `Static`, `Variadic` and class
`Abstract` metadata describe declarations without promising callable bindings.
Variadic declarations require an explicit concrete tail in the facade manifest;
discovery does not infer that tail.
Type spellings are not Go signatures, canonical qualified
names, independently resolvable aliases or ownership contracts.

The [executed selection example](../examples/cxx/members.go) chooses one scalar
constructor, a const overload, static member and destruction, then supplies explicit
ABI types and export names to `Generate`. The matching native C++ compiler remains
responsible for exact prototype and access checks. Discovery does not instantiate
arbitrary function templates, infer ownership, walk all declarations or generate
exports automatically.

For named records, two bounded queries identify the selected record: the first
obtains a short name for filtering, and the second associates its alias and
complete definition by
declaration IDs from that same compiler invocation. Namespace collisions, forward
declarations and class-valued template arguments therefore do not select unrelated
classes. The original header path, target and flags are preserved as for base
discovery. Each AST is limited to 32 MiB, 150,000 nodes and 256 levels; compiler
diagnostics are bounded and cancellation covers both queries.

Anonymous closure records use at most eight filtered declaration queries after
the initial alias query. Each can follow named function/variable references from
the preceding result; repeated filters are not queried again. Every selected
alias and definition is matched by declaration ID within the same invocation.
Unevaluated expressions and template arguments cannot select the record of an
argument or receiver. Unresolvable anonymous forms and longer forwarding chains
return an error. The same AST, IO and cancellation bounds apply to every query.
Native Go/llgo tests discover a mutable captured operator, generate its explicit
C facade, and execute stateful calls and owner destruction at `-O0`/`-O2`.

## Direct base declarations

`DiscoverMembers` also returns `ClassDeclaration.Bases` in base-specifier order.
Each `BaseDeclaration` records the native type spelling, effective `Access`
(`public`, `protected` or `private`) and `Virtual` flag for that direct edge.
No base-name list is needed to inspect these declarations, and the existing
selected-record query supplies them without another compiler process. Aliases,
forward declarations and same-name classes retain the existing identity checks.

The native type spelling belongs to the declaring class's context. A relative
name, nested type, private typedef or template spelling may need an accessible
qualified alias before it can select a facade type. Metadata does not turn that
spelling into a Go signature or a safe source fragment. `Generate` and
`DiscoverBases` retain their validated-name requirements.

A public edge does not prove an unambiguous pointer conversion: a class may
contain another subobject of the same base through a separate path. Non-public
edges are returned for diagnostics; they do not authorize a cast. `Virtual`
describes the direct edge, without recursively traversing the inheritance graph
or inferring object offsets, ownership or a virtual destructor contract. The
native compiler still performs every pointer adjustment.

The [executed direct-base example](../examples/cxx/direct_bases.go) resolves its
fixture's contextual names, asks `DiscoverBases` to validate public conversions,
and generates selected views. Go/llgo tests execute secondary-base virtual
dispatch, virtual-base state, null output handling and owner destruction through
generated cgo/direct llgo and optional dynamic callers at `-O0`/`-O2`. Metadata
and facade compilation cover all eight targets, including default/private/
protected access, aliases, nested types, template bases, preprocessing,
architecture-dependent bases and ambiguous/shared-diamond conversion controls.

## Coverage and remaining work

Go and llgo CI compile the base facade's C declarations and C++ source for all
eight qualified targets. Callable fixtures use the matching host C++ SDK. Native
jobs execute unoptimized/optimized libraries: a freshly generated
cgo caller runs without libffi, llgo also executes its direct generated caller,
and optional dynamic calls cover scalar widths,
const overloads, void results, native addresses, generated secondary/virtual base
views, composed diamond conversions, class copies/references/pointers, noncopyable
value results, copy exceptions, named scalar/void/enum/array/union and multi-level
pointers, native pointer mutation and borrowed provider retirement,
overloaded `operator&`, private-destructor static
classes, native function factories/arguments/returns, mutable captured lambdas,
independent callable copies, move-only captures/destruction, empty `std::function`
exceptions, native-entry construction, retained captured callbacks,
nested C++/Go reentry during retirement, void/native-address/mixed-width callback
signatures, virtual dispatch,
constructor/method exceptions, throwing destruction, cleared outputs and nulls.
Linux 386 requires C++ multilib development files in addition to i386 libffi.
`DYLIB_CXX` selects the Clang-compatible C++ test producer executable.

Selected member queries are cross-checked for eight targets, including same-name
classes in other namespaces, forward declarations, aliases, class-template
specializations, access sections, overloads, operators, default/deleted members,
abstract classes and target-dependent declarations. Generated cgo, direct llgo
and dynamic libffi callers execute the selected member facade at `-O0`/`-O2`.

Unrestricted whole-header declaration discovery,
variadic constructors and class-value tails, recursive inheritance discovery,
downcasts/crosscasts and C++ header discovery remain outside this manifest. These
facade tests use complete libraries. Raw C++ DWARF exceptions have a separately
qualified Linux/macOS subset; see [unwind scope](unwind.md). The generator
complements llcppg and ABIBridge while keeping C++ ABI
details in their native compiler.

References: [C++ member-pointer calls](https://eel.is/c++draft/expr.mptr.oper),
[exception handlers and noexcept](https://eel.is/c++draft/except.handle), and
[Windows exports](https://learn.microsoft.com/en-us/cpp/cpp/dllexport-dllimport).
