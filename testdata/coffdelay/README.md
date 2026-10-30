# GNU delay-import controls

`windows_amd64.a` and `windows_386.a` are real GNU Binutils
2.41.90.20240122 `dlltool --output-delaylib` output for `provider.def`.
`generate.sh` uses GNU dlltool and Clang's cross assembler. Set `DLLTOOL` to
the installed GNU cross-tool path; LLVM's dlltool is not a substitute.
The checked-in archives are fixture bytes, not implementations copied from GNU.

GNU stores the descriptor in `.text$2` and uses `.idata$4/$5` for the lookup
table and initial fallback pointers. The initially empty `.idata$2` section does
not identify an ordinary import directory. Metadata tests inspect both fixtures
on every host. Windows amd64/386 execute them against a freshly compiled DLL.

Clang-generated compatible templates also cover Windows ARM64 and all three
MSVC/GNU target spellings. No GNU ARM64 delay-archive producer is claimed:
the tested dlltool accepts `-m arm64` but emits no delay archive. The generator
therefore checks that each expected output exists and is nonempty.

Native tests compile `provider.c` and `consumer.c` at `-O0` and `-O2`, covering
named/ordinal imports, floating arguments, struct values/results and pointers,
variadic doubles, repeated calls, failed-link retry and default rejection.
`control.c` reuses the independent PE fixture's delay helper for system-linked
controls. The control builder renames delay `.idata$*` sections to `.didat$*`,
separating them from LLD's synthetic ordinary kernel32 import tables without
changing instructions, symbols or relocation targets. It checks the ordinary
import directory before native loading. The helper runs only in those controls;
eager raw binding bypasses it.

`windows_386_compat.a` uses GNU Binutils 2.41.90.20240122 `dlltool` and GNU's
i386 assembler with `--compat-implib --output-delaylib`. Both legacy
`___imp_add` and modern `__imp__add` label the same IAT cell; the ordinal entry
has the corresponding pair of labels. Regenerate it separately with:

```sh
DLLTOOL=/path/to/gnu/dlltool DLLTOOL_AS=/path/to/i686-w64-mingw32-as bash testdata/coffdelay/generate.sh compat
```

Compatibility mode defaults to `i686-w64-mingw32-as`. Independent PE controls
retain GNU member-name ordering, separate delay and ordinary import tables,
and disable SafeSEH for GNU i386 objects that lack MSVC compatibility records.
Windows Go 386 executes these controls and the raw eager loader against the
same freshly compiled provider, checking actual helper calls and IAT identity.
