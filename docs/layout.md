# Native storage layouts

`abi.LayoutOf(description, convention)` queries host C storage. Default/CDecl
queries require cgo, with Go or llgo; `-tags libffi` is optional.
`abi.StorageAvailable()` reports this storage capability independently of
`abi.Available()`, which reports dynamic calls. Without cgo, valid queries return
`abi.ErrUnavailable`. Invalid descriptors, void and invalid conventions fail
before backend preparation.

| Result | Meaning |
| --- | --- |
| `Layout.Size` | Storage bytes, including trailing padding |
| `Layout.Alignment` | Required alignment in bytes |
| `Layout.Offsets` | Direct struct fields or array elements, in declaration order |

Offsets are absent for scalars and pointers. For nested records, query each
member's descriptor separately. A matrix's direct offsets describe rows; querying
the row descriptor gives column offsets. The returned slice belongs to the caller;
changing it cannot modify another query, prepared call or callback. No native
allocation is retained after a query, so no `Close` operation is needed.

Pointer queries describe the native address itself. An annotated pointee is
validated logically but is not laid out or allocated by that query. Arrays can be
queried as storage, including arrays of records and pointers. C still cannot pass
or return a bare array by value. Descriptor nesting/element limits and the
backend's 64 KiB aggregate storage limit remain unchanged.

`abi.Default` and `abi.CDecl` use the host's default C ABI. `abi.StdCall` and
`abi.FastCall` require the libffi backend on Windows 386; other explicit
conventions also require libffi and its host-specific convention support. The
query does not select a foreign operating system, CPU or compiler ABI.

Use these values to compare a compiler's `sizeof`, `_Alignof` and `offsetof`
results before preparing a generated record binding. Ordinary field descriptions
cannot model unions, packed records, bitfields or custom compiler alignment.
Matching storage layout also does not establish C++ ownership, a language's
runtime contract, or the calling convention of an arbitrary export.

The [native comparison test](../native_layout_test.go) compiles
[C layout probes](../testdata/layout.c) and executes them from a raw object,
archive and OS library. It checks signed/unsigned integers, float/double, Boolean,
pointers, padded/nested records, record arrays, matrices and pointer arrays.
Existing Go/llgo native suites execute these tests, including Go Linux/Windows
386 and independent Go execution on Windows ARM64. Additional tests cover
unavailable builds, validation, concurrent independent queries, convention
selection, oversized aggregates and pointer queries with oversized pointees.

Without libffi, the implementation queries scalar `sizeof`/`_Alignof` from the
host C compiler and computes ordinary struct/array offsets and padding in Go.
It deliberately avoids Go struct alignment, which differs on some 32-bit hosts.
With libffi enabled, layout queries retain its native ABI-specific calculation;
owned storage always uses the C scalar probes and Go layout calculation. Tests
compare both results and independently dereference/mutate the allocation in C,
including nested padding. The retained-record library example is built without
`-tags libffi` on every native CI target.
