# Spike: the `vfs` package (+ deterministic tar packing)

Phase 2 dependency spike for PORT_SPEC §6 — "`fs` (pyfilesystem2) → custom `vfs`
package on `io/fs`. **Hard, must be designed.**"

Status: code compiles, passes `gofmt`/`go vet`/`errcheck`/`staticcheck`/`-race`,
and every behavioural claim below was pinned against the real Kathara 3.8.3 +
pyfilesystem2 stack through `/root/kathara/pyvenv`. Still subject to full §10
adversarial review in Phase 3; open items are in §7.

---

## 1. What landed where

| File | Contents |
|---|---|
| `vfs/vfs.go` | `FS` (spec §6 verbatim), `Appender`/`TypeNamer` optional interfaces, `InvocationError` + sentinels, `CleanPath`, `Type`, `Path`, `Exists`, `IsDir`, `IsEmpty`, `ReadFile` |
| `vfs/osdir.go` | `OSDir(path)` — the `osfs://` implementation |
| `vfs/memory.go` | `Memory()` — the `mem://` implementation, mutex-guarded |
| `vfs/sub.go` | `Sub(fsys, dir)` — `fs.opendir`, used by `model/Machine.py:80` |
| `vfs/files.go` | `CreateFileFromString/List/Path/Stream`, `UpdateFileFromString/List`, `CopyDirectory` |
| `vfs/lines.go` | `SplitLines`, `pySearch`, `WriteLineBefore`, `WriteLineAfter`, `DeleteLine` |
| `vfs/walk.go` | `Walk`, `Files`, `Dirs` |
| `internal/util/tar.go` | `TarEntry`, `TarName`, `WriteTar`, `PackFilesForTar` |
| `vfs/testdata/pysearch_matrix.json` | 522 `re.search(pattern, line)` results produced BY CPython 3.13 |
| `vfs/testdata/pysearch_divergent.json` | 140 more, produced the same way, concentrated on the **mid-pattern `$`** class the matrix has none of (§5.9) |
| `vfs/testdata/pyreadlines.json` | 23 `readlines()` results produced BY pyfilesystem2 |
| `internal/util/testdata/pack_*.tar` | 5 archives produced BY `utils.pack_files_for_tar` |

Tests: `vfs/{vfs,files,lines,walk,oracle,review}_test.go` — **51 top-level test
functions, 439 PASS assertions counting subtests, 0 failures**, clean under
`-race`. (`review_test.go` holds the Phase 3 regression set, §8.) The tar tests
live in `internal/util/tar_test.go` and are counted by that package.

All three oracle tables were re-verified against the live stack during the
Phase 3 review: 522 / 140 / 23 rows, **0 disagreements**.

**Package placement note.** The tar helper is in `internal/util`, not in `vfs`.
PACKAGE_GRAPH.md §2 row 2 assigns "tar packing" to `internal/util` (`tar.go`),
and the packer needs nothing from `vfs` — it takes bytes. `internal/util` may
import `kerrors` but not `vfs`, so this keeps the frozen dependency table
intact. `vfs` itself imports **stdlib only**, per PACKAGE_GRAPH §1.2.

---

## 2. Design decisions

### 2.1 `FS` stays exactly as the spec froze it

`FS` is `fs.FS` + `Create`/`MkdirAll`/`Remove`/`SysPath`, character for
character. Three things the port needs but the spec's interface does not carry
are handled as **optional interfaces** rather than by widening `FS`:

- `Appender` (`Append(name) (io.WriteCloser, error)`) reproduces
  `fs.open(p, "a")` for `UpdateFileFrom*`. Both implementations provide it; a
  third-party `FS` gets a read-modify-rewrite fallback with the same result.
- `TypeNamer` (`TypeName() string`) backs `FilesystemMixin.fs_type()` →
  `vfs.Type(fsys)` returning `"os"` / `"memory"` / `""`.
- `fs.StatFS` and `fs.ReadDirFS` are implemented by both so `fs.WalkDir` and
  `fs.Stat` take the fast path and the "missing vs is-a-directory" distinction
  can be made without opening the file.

### 2.2 Path model

`CleanPath` maps a pyfilesystem path onto an `io/fs` path: leading slashes
dropped (`"/a/b"` and `"a/b"` are the same resource), `.`/`..` resolved,
traversal **clamped at the root** (`"../../x"` → `"x"`), root spelled `"."`.
Backslashes are ordinary bytes — pyfilesystem does not translate them on POSIX,
and only `pack_file_for_tar` rewrites them.

**Corrected in Phase 3.** An earlier draft of this section claimed the clamping
was "exactly like `fs.path.normpath` on an absolute path". That is **false**:
`normpath` *raises* `fs.errors.IllegalBackReference` for any `..` that escapes
the root, it does not clamp. The clamping is therefore a divergence, §5.11.

`Create` deliberately does **not** create parent directories, because
`fs.open(p, "w")` does not either; every `create_file_from_*` helper calls
`MkdirAll(dirname)` first, exactly as the Python does.

### 2.3 Line editing: three non-obvious rules

These are the reason §6 is labelled "hard". All three were discovered by probing,
not by reading.

1. **pyfilesystem opens text files with `newline=""`.** That is universal-newline
   *splitting* with **no** translation: a line keeps its original `\r\n` / `\r` /
   `\n` terminator, and the pattern is matched against that raw line. `SplitLines`
   reproduces it and is pinned against 23 oracle-produced `readlines()` results.
2. **The write-back normalisation is per line, and is `\r\n`→`\n` only.** Python's
   `line.replace("\n\r","\n").replace("\r\n","\n")` collapses CRLF and leaves a
   lone CR alone. It runs on *every* line, so **a CRLF file is rewritten as LF
   even when nothing matched and the function returns 0.** The first replacement
   (`"\n\r"`) is dead code at line granularity — a `\n` can only be a line's last
   byte, so it can never be followed by `\r` inside one line. It is kept for
   line-for-line readability, and is genuinely live in
   `utils.convert_win_2_linux`, which runs on whole-file content.
3. **Python's `$` ≠ Go's `$`.** Python's `$` matches at end-of-string *or*
   immediately before a trailing newline; Go's `$` (no `m` flag) is `\z`.
   `pySearch` retries the match against the line with exactly one trailing `\n`
   removed. This cannot invent matches: the shorter string is a prefix of the
   longer one, so any unanchored match in it also exists in the original. Pinned
   against a 522-row `pattern × line` matrix computed by CPython — 0 disagreements.

   **Corrected in Phase 3.** The retry covers a `$` that the whole match *ends
   at* — which is every `$` the 522-row matrix contains, so "0 disagreements"
   was true but vacuous for the rest of the class. It does **not** cover a `$`
   that sits mid-pattern with something after it that consumes the trailing
   newline: `re.search("b$\n", "b\n")` is `True` in Python, and `pySearch`
   returns false. The "cannot invent matches" half stands and is now asserted
   directly; the miss is a residual gap, §5.9.

`editLines` also preserves Python's **error ordering**: nil-FS guard →
`re.compile` → file access. A bad pattern therefore reports itself even when the
target file does not exist.

### 2.4 Tar packing

Header fields are `tarfile.TarInfo`'s defaults, which `pack_file_for_tar` never
overrides — verified directly: `mode 0644, uid 0, gid 0, mtime 0, typeflag '0',
uname '', gname ''`. **`pack_files_for_tar` emits regular files only**; it never
adds a directory member, so the "files vs dirs" question the brief raised has no
second case to reproduce. Arcnames get `\` → `/` and nothing else — a leading `/`
is preserved verbatim.

**Member order = caller order.** *Superseded in Phase 3.* This spike originally
sorted members by canonical arcname for determinism. `ORDERING.tsv` row
`utils.py:452` is binding and reads "API takes ordered pairs; Python client must
preserve caller order", and the sort could not honour it: it also flipped the
last-wins winner for two keys that canonicalise to the same member (`etc\x` and
`etc/x`). `WriteTar` now emits `[]TarEntry` in slice order and the map-shaped
`PackFilesForTarMap` entry point is gone, since a Go map has no caller order to
preserve.

One deliberate divergence remains, for determinism:

- **gzip MTIME zeroed.** Python's `w:gz` stamps `time.time()` there — verified:
  two `pack_files_for_tar` calls on identical input are **not** byte-identical,
  while the tar payload underneath is. `gzip.Header{OS: 255}` with the zero
  `ModTime` extends the determinism to the wrapper.

Record padding is reproduced: CPython pads to a multiple of `RECORDSIZE` (10240);
Go's `archive/tar` stops after the two zero blocks. Without the pad, the empty
archive would be 1024 bytes instead of Python's 10240.

---

## 3. Python-behaviour probes

Every row was executed against `Kathara 3.8.3` / `pyfilesystem2` on CPython
3.13.5 via `/root/kathara/pyvenv/bin/python`.

### 3.1 Create vs update vs truncate vs append

| Probe | Call | Result |
|---|---|---|
| P10 | `create_file_from_string("b", …)` over `"aaaaaaaaaa"` | `b"b"` — **truncate** |
| P11 | `update_file_from_string("bb", …)` over `"aa"` | `b"aabb"` — **append**, no separator |
| P6 | `update_file_from_string` on a missing file, existing dir | file **created**, `b"hello"` |
| P6b | `update_file_from_string` on `/nodir/new.txt` | `fs.errors.ResourceNotFound` — update does **not** makedirs |
| P7 | `create_file_from_string("x", "test.txt")` | OK — `dirname("test.txt")` is `""`, and `makedirs("")` is the root |
| P7b | `create_file_from_string("x", "/a/b/c.txt")` | parents created |
| P12 | `create_file_from_list(["a","b"])` then `update_file_from_list(["c"])` | `b"a\nb\nc\n"` — every element gets a terminator |
| P13 | `create_file_from_string` onto a directory | `fs.errors.FileExpected` |
| P13b | `update_file_from_string` onto a directory | `fs.errors.FileExpected` |
| MD1 | `create_file_from_string("y", "/a/b.txt")` where `/a` is a file | `fs.errors.DirectoryExpected` |

### 3.2 create_file_from_path / _from_stream / copy_directory_from_path

| Probe | Call | Result |
|---|---|---|
| CP1 | `create_file_from_path` with `b"\x00\x01CRLF\r\nend"` | byte-verbatim — **no** CRLF collapse, **no** BOM strip, **no** binary sniff |
| CP2 | missing source | `FileNotFoundError` |
| CS1 | `create_file_from_stream` with a **text** handle over `b"a\r\nb\n"` | `b"a\nb\n"` — the host `open()`'s universal-newline translation, not the mixin's |
| CS2 | same source, **binary** handle | `b"a\r\nb\n"` verbatim |
| CD1 | `copy_directory_from_path` into an existing `/dst` holding `top.txt` (`"PRE"`) and `keep.txt` | merges; `top.txt` overwritten to `b"top"`; `keep.txt` survives; empty source dirs reproduced (`/dst/empty`) |
| CD2 | destination does not exist | created |
| CD3 | source does not exist | `fs.errors.CreateFailed` |

### 3.3 Line splitting (`fs.open(p,"r").readlines()`, `newline=""`)

| Input bytes | `readlines()` |
|---|---|
| `b"a\r\rb"` | `['a\r', '\r', 'b']` |
| `b"a\r\r\nb"` | `['a\r', '\r\n', 'b']` |
| `b"a\n\rb"` | `['a\n', '\r', 'b']` |
| `b"a\r\n\r\nb"` | `['a\r\n', '\r\n', 'b']` |
| `b""` | `[]` |
| `b"\n"` | `['\n']` |

Full 23-row table is checked into `vfs/testdata/pyreadlines.json` and replayed by
`TestSplitLinesAgainstOracleReadlines`.

### 3.4 Regex anchoring

| Probe | Pattern / file | `write_line_before` count |
|---|---|---|
| D3 | `b$` on `b"a\nb\nd\n"` | **1** |
| D1 | `b$` on `b"a\r\nb\r\nd\r\n"` | **0** |
| D2 | `b$` on `b"a\rb\rd\r"` | **0** |
| D4 | `b$` on `b"a\nb"` (no terminator) | **1** |
| D5 | `^.$` on `b"a\nb\n"` | **2** |
| D6 | `b\n$` on `b"a\nb\n"` | **1** |
| N7 | `^$` on `b"\n"` | **1** |
| P9 | `delete_line` with `b\n` on `b"a\nb\nd"` | **1** |
| R1 | `delete_line` with `[` | `re.PatternError` **before** any I/O |

Full 522-row matrix in `vfs/testdata/pysearch_matrix.json`.

### 3.5 Whole-vector replay

All 47 `WriteLineBefore` / `WriteLineAfter` / `DeleteLine` table rows were
exported from the Go tests and re-executed through the real `FilesystemMixin`:
**47 vectors, 0 mismatches** on both the return count and the resulting bytes.

### 3.6 Tar

| Probe | Result |
|---|---|
| `TarInfo()` defaults | `mode 0o644, uid 0, gid 0, mtime 0, type b'0', uname '', gname ''` |
| `tarfile.DEFAULT_FORMAT` | `PAX_FORMAT` (2) |
| arcname | `sub\win\bom.txt` → `sub/win/bom.txt`; `/zz/last.txt` keeps its leading `/` |
| member order | dict insertion order (`{"b","a"}` → `['b','a']`; `{"a","b"}` → `['a','b']`) |
| determinism | two calls on identical input are **not** byte-identical (gzip MTIME); the inner tar **is** |
| empty input | 10240-byte tar (record padding) |
| gzip header | `1f8b 08 08 <mtime> 02 ff` — FNAME set, XFL 2, OS 255 |
| text-mode stream member | `OSError: unexpected end of data` — a **Python bug**, see §6 |
| Python reading Go's output | all five Go archives parse, with the exact expected header fields, and `1f8b0800 00000000 00 ff` |

---

## 4. Pinned-behaviour table (the subtle cases)

| Function | Subtle case | Python-verified outcome |
|---|---|---|
| `UpdateFileFromString` | create vs truncate vs append | **append** (`"aa"` + `"bb"` → `"aabb"`); creates a missing file; errors on a missing parent dir |
| `CreateFileFromString` | second write | **truncate** |
| `CreateFileFromString` | bare filename (`"test.txt"`) | OK — `makedirs("")` is the root |
| `CreateFileFromList` | last element | still gets a `\n` |
| `WriteLineBefore` | **no match** | returns **0, not an error**, and the file is still **rewritten** — an all-LF file is byte-identical, a CRLF file comes back LF (`"a\r\nb\r\n"` → `"a\nb\n"`) |
| `WriteLineBefore` | indented match `\t\t\td`, pattern `d` | matches (`re.search` on the raw line, **not** a stripped `re.match` — the EXPECTATIONS-core.md wording is wrong, see §6); inserted line is unindented `c\n` |
| `WriteLineBefore` | match on a last line with no terminator | line stays terminator-less: `"a\nb\nd"` → `"a\nb\nc\nd"` |
| `WriteLineBefore` | `line_to_add` = the pattern (`d` before `d`) | no loop — 2 inserts for 2 original lines |
| `WriteLineBefore` | empty pattern | matches every line (3 inserts on a 3-line file) |
| `WriteLineBefore` | file is exactly `"\n"`, pattern `^$` | **1** insert → `"X\n\n"` |
| `WriteLineBefore` | `line_to_add` containing `\n` | written verbatim + one `\n`: `"X\nY"` → `"X\nY\n"` |
| `WriteLineAfter` | last line has no terminator | matched line gains `\n`, then the new line: `"…\t\t\td"` → `"…\t\t\td\nc\n"` |
| `WriteLineAfter` | last line ends in a **bare CR** | the `endswith("\n")` test sees the **raw** line, so the extra `\n` is emitted: `"a\r\nd\r"` → `"a\nd\r\nX\n"` |
| `WriteLineAfter` | matched line ends `\r\n` | no extra `\n`: `"a\r\nd\r\n"` → `"a\nd\nX\n"` |
| `WriteLineAfter` | `line_to_add` = the pattern (`b` after `b`) | 1 insert, no loop — matching is over the ORIGINAL line list |
| `DeleteLine` | delete the terminator-less last line | `"a\nb\nd"` → `"a\nb\n"` |
| `DeleteLine` | `first_occurrence` | later matches are detected but kept |
| all three | bare CR line endings | **not** normalised — only `\r\n` is (`"a\rb\rd\r"` + insert before `b` → `"a\rc\nb\rd\r"`) |
| all three | pattern `b$` vs a CRLF/CR line | **no match** (Python's `$` only looks past a trailing `\n`) |
| all three | invalid regex + missing file | the **regex** error wins — `re.compile` runs before the open |
| all three | missing path / directory path / nil FS | `ResourceNotFound` / `FileExpected` / `InvocationError` |
| `CreateFileFrom*` | `dst_path` with a **trailing slash** | `os.path.dirname` runs on the RAW path, so `dirname("/a/b/")` is `/a/b`: makedirs creates the **directory** `/a/b`, then the open raises `FileExpected`. With `/a/b` already a file the makedirs fails first — `DirectoryExpected`. `UpdateFileFrom*` has no makedirs and pyfilesystem normalises the slash away, so `update("bb", "/a/")` **appends** to the file `/a` |
| `CopyDirectory` | existing destination | **merges**; same-named files overwritten, others survive; empty source dirs reproduced |
| `CopyDirectory` | **symlinks** in the source | **followed**, both kinds: `copy_dir` walks an OSFS and `OSFS.scandir` classifies with `os.DirEntry.is_dir()`, which follows. `linkdir -> real/` yields `/dst/linkdir/f.txt`; `linkfile -> plain.txt` yields `/dst/linkfile`. A broken link is an error; a loop dies on `ELOOP` |
| any path arg | `..` **escaping the root** (`"/../../esc.txt"`) | `fs.path.normpath` raises `IllegalBackReference` and **nothing is written**, on both backends. The port clamps to `esc.txt` and succeeds — divergence §5.11, pinned not fixed |
| `CopyDirectory` | missing **source** | `fs.errors.CreateFailed` (from `open_fs`). The port has no equivalent and reports `fs.ErrNotExist` — §5.12 |
| `CopyDirectory` | **broken symlink** in the source | errors (`ResourceNotFound`) on both backends, but the partial state left behind **differs between Python's own backends** (`mem` leaves nothing, `osfs` leaves an empty `/dst/zbroken`), so partial state is unspecified — the port errors with the same class and is not held to a byte-exact partial tree |
| `Remove` | the filesystem **root** | refused on both spellings — `removedir("/")` is `RemoveRootError`, `remove("/")` is `ResourceNotFound` (mem) / `FileExpected` (osfs). Never deletes the backing host directory |
| `Remove` | non-empty directory | `DirectoryNotEmpty` on both backends |
| any op | a path **component** that is a file | `ResourceNotFound` for the file-flavoured operations (`getinfo`, `openbin` in every mode, `remove`, and so the whole line-edit family), `DirectoryExpected` for the directory-flavoured ones (`listdir`, `makedir`, `removedir`) — `fs/error_tools.py` keeps two errno tables and only `DIR_ERRORS` maps `ENOTDIR` to `DirectoryExpected` |
| `IsEmpty` | on a file | `DirectoryExpected` on both backends |
| `Memory` | entry lifetime | the entry is created — and, for `"w"`, truncated — at **open** time, not on close: the file exists and reads `b""` immediately, and `removedir` on its parent then fails `DirectoryNotEmpty` |
| `Sub` | `opendir("")` / `opendir("/")` | still a `SubFS`, so `fs_type()` is `"sub"`, not the parent's name |
| `CreateFileFromPath` | CRLF / BOM / binary source | copied **byte-verbatim** — no normalisation on this path |
| `PackFilesForTar` | directory members | none exist — `pack_files_for_tar` writes regular files only |
| `PackFilesForTar` | empty input | a 10240-byte archive, not 1024 |

---

## 5. Divergences introduced by this spike

All documented in code:

1. ~~**Tar member order** is sorted, not insertion order.~~ **Withdrawn in
   Phase 3**: it contradicted the binding `ORDERING.tsv` row for `utils.py:452`.
   Members are emitted in caller order and `PackFilesForTarMap` is deleted
   (§2.4).
2. **gzip MTIME zeroed** (§2.4). Python's is `time.time()`, and its `FNAME` is a
   random temp-file basename, so two Python calls on identical input already
   differ. Recorded in `PROPOSED-DIVERGENCES.md`.
3. **`OSDir(path)` does not fail eagerly** on a missing directory, because the
   §6 signature returns no error; `open_fs("osfs://missing")` raises
   `CreateFailed`. The first operation fails with `fs.ErrNotExist`.
   `model.NewLab` should stat the directory itself to keep the error site.
4. **`vfs.Path` strips the trailing separator** that `getsyspath("")` returns
   (`"/tmp/x"` vs `"/tmp/x/"`). The Python test itself `normpath`s before
   comparing.
5. **Non-UTF-8 file content is handled, not rejected.** Python's line helpers
   raise `UnicodeDecodeError` on a file containing `\xff` (verified U1); the Go
   port operates on bytes and succeeds. This is strictly more permissive. It has
   no reachable call site inside Kathara (line editing targets `.startup` and
   config files) but is an API-surface difference — flagged for a Phase 3 ruling.
6. **`CreateFileFromStream` has no text branch.** Python switches on
   `stream.mode`; Go readers carry no mode. EXPECTATIONS-core.md §5 already
   classes the write-only-stream test as DROP.
7. **Tar device fields.** CPython **3.13** writes NULs into `devmajor`/`devminor`
   for non-device members (`tarfile._create_header`'s `has_device_fields`
   branch, new in 3.13); CPython 3.9–3.12 and Go's `archive/tar` write octal
   zeros. The 3.8.3 goldens are therefore oracle-version dependent in those 16
   bytes and the header checksum that covers them (delta exactly `14 × 0x30 =
   672`). `TestPythonParity` masks those two windows and `TestPythonParityDivergentFields`
   asserts both sides explicitly; **everything else is byte-identical**.
8. **Long / non-ASCII arcnames use a different PAX encoding.** CPython emits a
   `././@PaxHeader` extended block; Go splits into the ustar prefix field (names
   >100 bytes) or emits its own `PaxHeaders.0/` block (non-ASCII). Bytes differ,
   members do not — asserted both ways by `TestPythonPaxReadable`, and CPython's
   `tarfile` reads Go's archives with the right names.

Added in Phase 3 — both are residual gaps with no fix available inside the
frozen §6 interface, so they are pinned by tests rather than closed:

9. **A mid-pattern `$` in a caller-supplied `searched_line` is missed.**
   Python's `$` is the lookahead `(?=\n?\z)`; RE2 has no spelling for it, and
   `pySearch`'s strip-one-`\n` retry only reaches a `$` the match *ends at*
   (§2.3 rule 3). When something after the `$` consumes the trailing newline the
   Go side returns no match where Python matches — `write_line_before(f, "X",
   "b$\n")` on `b"a\nb\nd\n"` is `1` / `b"a\nX\nb\nd\n"` in Python and `0` /
   unchanged here. The failure is **one-sided**: the emulation can only ever
   miss, never invent, which is what keeps it safe for the `$`-anchored patterns
   that do work. No in-tree pattern puts `$` anywhere but in terminal position,
   so this is reachable only through the §7 client API. Pinned by
   `vfs/testdata/pysearch_divergent.json` (140 CPython-produced rows) and
   `TestPySearchDivergentCorpus`, which fails loudly both if the emulation ever
   invents a match and if the gap ever closes. Same family as the `\Z` and
   Unicode-`\b`/`\w` gaps in §7.5.
10. **Filenames that are not valid UTF-8 are rejected, not carried.** `io/fs`
    requires `fs.ValidPath`, which requires valid UTF-8, so `CleanPath` turns a
    latin-1 lab filename into `fs.ErrInvalid`; pyfilesystem's OSFS carries
    arbitrary POSIX filename bytes through `surrogateescape` (verified:
    `b"caf\xe9.txt"` lists as `'/caf\udce9.txt'`). Divergence 5 above covers
    non-UTF-8 *content*, which the port handles; this is about *names*, and it
    is strictly less permissive. Not fixable while `FS` embeds `fs.FS`.
    Reachable with a Windows-authored lab directory.
11. **A `..` that escapes the root is clamped, where pyfilesystem refuses it.**
    *Found in Phase 3 by the fixer's Go-vs-Python differential; neither §10
    review raised it.* `fs.path.normpath` **raises**
    `fs.errors.IllegalBackReference` for any path whose back-references leave
    the filesystem root — verified live: `normpath("/../../x")`,
    `normpath("..")`, `normpath("/..")` and `normpath("a/../..")` all raise, and
    `create_file_from_string("x", "/../../esc.txt")` therefore writes
    **nothing** and raises on both `mem://` and `osfs://`. `CleanPath` clamps
    instead (`"/../../esc.txt"` → `"esc.txt"`), so the same call **succeeds**
    and creates the file at the root under a name the caller never asked for.
    Both behaviours are *contained* — neither can escape the filesystem root —
    so this is a silent wrong-target write, not a traversal hole. Reachable
    only through a caller-supplied path on the §7 client API
    (`model/Machine.py:638` and its siblings pass `dst_path` straight through).
    **Not fixed**: refusing would add an exported sentinel and flip a
    path-model contract that both §10 reviews read and accepted, so it needs a
    ruling — §7.11. Pinned by `TestBackReferenceIsClampedNotRefused`; the false
    "exactly like `fs.path.normpath`" claim is corrected in §2.2 and in
    `CleanPath`'s doc comment.
12. **A missing `copy_directory_from_path` source is a different error class.**
    Python opens the source with `open_fs()`, so a missing directory is
    `fs.errors.CreateFailed` (verified live, both backends). The port has no
    `CreateFailed` equivalent — `OSDir(path)` cannot fail eagerly, divergence 3
    — so the `os.Stat` error surfaces as `fs.ErrNotExist` instead. Both error;
    only the class differs. Pinned by `TestCopyDirectoryMissingSourceErrorClass`.

---

## 6. Python bugs / doc errors found

1. **`EXPECTATIONS-core.md` §5 mis-describes the line matching.** It says
   `searched_line` is "matched against each line **stripped** and matched with
   `re.match`". The source is `pattern.search(line)` on the **raw** line,
   terminator included. The indentation test passes under both readings, which
   is presumably how the inference went wrong, but the two differ observably:
   `re.match` anchors at position 0, `re.search` does not (`"b"` against
   `"\t\tb\n"` matches under `search`, not under `match`), and `search` sees the
   trailing terminator (`delete_line(…, "b\n")` deletes — probe P9). The port
   follows the **source**. Suggest correcting the analysis doc.
2. **`pack_file_for_tar` is broken for text-mode streams** (`utils.py:434-439`).
   It computes the member size with `seek(0,2)/tell()` (**bytes**) but the
   content with `read().encode()` after the host `open()` already collapsed
   CRLF (**fewer bytes**). A CRLF text handle raises
   `OSError: unexpected end of data`. Not reachable from the CLI — every caller
   passes host paths — but it is reachable from the §7 client API. Candidate for
   `DIVERGENCES.md`; the Go packer takes bytes and cannot express the bug.
3. `line.replace("\n\r", "\n")` in the three line helpers is dead code (§2.3
   rule 2). Harmless.

---

## 7. Open items for Phase 3

1. **`vfs` cannot import `kerrors`** (PACKAGE_GRAPH §1.2 lists its imports as
   "none") but PACKAGE_GRAPH §row 142 wants `Invocation` code parity. Resolved
   here by defining `vfs.InvocationError` with the two frozen Python message
   constants and letting `model`/`cmd` map it. **Needs a reviewer ruling** that
   this is the intended shape rather than an edge `kerrors` import.
2. **Append atomicity.** `UpdateFileFrom*` uses a real `O_APPEND` handle on both
   implementations (via `Appender`), but the fallback path for a third-party
   `FS` is read-modify-rewrite. Decide whether the fallback should exist at all.
   *Phase 3:* the fallback was **unreachable through `Sub`** — a `subFS` always
   satisfies the `Appender` assertion, so a Sub of a non-`Appender` parent
   errored out instead of falling back. Both paths now route through
   `openAppend`, so the documented contract holds either way. The
   should-it-exist question is unchanged.
3. **Line editing is read-all / rewrite-all**, like Python's
   `readlines + seek(0) + truncate`. Neither is crash-atomic. If a reviewer wants
   write-to-temp-and-rename, it is a behaviour change (the file's inode and any
   open handles change) and needs a ruling.
4. **Symlink containment.** `CleanPath` clamps `..` at the root, but a symlink
   inside an `OSDir` tree can still point outside it — same as pyfilesystem's
   OSFS. Go 1.24+ `os.Root` would close this; it needs the directory to exist at
   construction time, which conflicts with the `OSDir(path) FS` signature
   (divergence 3). Worth revisiting together.
5. **RE2 vs `re` beyond `$`.** PORT_SPEC records that no in-tree pattern uses
   lookaheads or backreferences, and the 522-row matrix found no other
   disagreement. Two residual gaps for **user-supplied** patterns through the
   §7 client API: `\Z` (Python) has no RE2 spelling, and Python's `\b`/`\w` are
   Unicode-aware for `str` patterns while Go's are ASCII. Decide whether to
   translate, reject, or document.
   *Phase 3:* a **third** gap joined them — `$` itself, when it is not in
   terminal position (§5.9). Documented and pinned, not translated: the faithful
   rewrite is a lookahead. Whatever ruling covers `\Z` should cover this too,
   since both are end-anchor semantics RE2 cannot express.
6. **`MkdirAll` permission.** Free functions pass `0o755`; pyfilesystem's
   `makedirs` takes no mode and lands on `0777 & ~umask`. Confirm `0755` is the
   wanted fixed value (it matters for `shared/` and device directories created
   under an OSDir lab).
7. **Windows `dirname`.** Python computes the parent with `os.path.dirname` on
   the **raw** path, so `create_file_from_string(c, "a\\b")` targets directory
   `a` on Windows and the root on POSIX. `CleanPath` treats `\` as a filename
   byte everywhere. Needs a ruling once the Windows build is in scope.
   *Phase 3:* the raw-path part is now reproduced — `prepareCreate` runs
   `posixDirname` on the untouched `dst_path` (§5 / §4, the trailing-slash row),
   which is `posixpath.dirname` byte for byte. What is left for the ruling is
   purely the separator: `ntpath.dirname` also splits on `\`, `posixDirname`
   never does.
8. **`internal/util/tar.go` ownership.** Another Phase 2 spike may also be
   creating `internal/util`. Merge conflict risk on the package doc comment
   only; the file itself is self-contained.
9. **`convert_win_2_linux` is not ported here.** The tar packer takes
   already-normalised bytes. The binary sniff (`binaryornot`), UTF-8 BOM strip
   and CRLF/CR collapse belong to `internal/util/binary.go` and are a separate
   spike. Verified reference behaviour for whoever takes it:
   `b"hello\r\nworld\r\n"` → `b"hello\nworld\n"`; `b"\xef\xbb\xbfBOM here\n"` →
   `b"BOM here\n"`; `b"a\rb\r"` → `b"a\nb\n"`; `b"a\n\rb"` → `b"a\n\nb"` (the
   `"\n\r"` replace **is** live at file granularity); `b"caf\xe9\n"` (latin-1)
   and `b"\x00\x01\x02\xff"` pass through byte-verbatim.
10. ~~`fs_type()` for a sub-filesystem.~~ **Resolved by probe.** `opendir`
    returns a `SubFS`, so `fs_type()` is `"sub"` on both a `mem://` and an
    `osfs://` parent, while `fs_path()` still resolves to the real host
    subdirectory (`/tmp/…/pc1`) for the OSFS case and `None` for memory.
    `vfs.Sub` reproduces exactly that. Reviewers should note `Lab.is_local()`
    (`model/Lab.py:425`) compares `fs_type() == "os"` on the **Lab's own** FS,
    never on a device sub-FS, so nothing changes behaviourally — but
    `Machine.fs_type()` returning `"sub"` is now correct rather than accidental.
11. **Back-references that escape the root: clamp or refuse?** (§5.11, new in
    Phase 3.) pyfilesystem raises `IllegalBackReference`; `CleanPath` clamps and
    the write silently lands somewhere else. Refusing is the faithful choice and
    is a ~4-line change, but it adds an exported sentinel
    (`ErrIllegalBackReference`), changes `CleanPath`'s contract for every free
    function and both write-side implementations, and flips three existing test
    rows — a design decision outside the fixer's work order, especially as
    `ERROR_CODES.md` is frozen and `Exists`/`IsDir` return no error to carry the
    new class. Left as-is and pinned; **needs a ruling**. Note the current
    behaviour is contained, so this is a faithfulness question, not a security
    one.

---

## 8. Phase 3 review outcome

Two independent §10 adversarial reviews ran against this package. Everything
below was re-derived against the live Kathara 3.8.3 / pyfilesystem2 stack on
**both** backends before it was changed, and the fixes were then validated by a
26-scenario differential harness (Go dump vs `FilesystemMixin` dump, mem:// and
osfs://): **0 mismatches**. Regression tests live in `vfs/review_test.go`.

| # | Finding | Disposition |
|---|---|---|
| 1 | `dst_path` with a trailing slash created a file where Python creates a directory and raises `FileExpected` | **Fixed** — `prepareCreate` takes the parent from `posixDirname` on the RAW path (§4) |
| 2 | `CopyDirectory` aborted on symlinks and left a partial file; Python follows them | **Fixed** — `copyHostDir` classifies with a following `os.Stat` (§4) |
| 3 | `OSDir.Remove("")` deleted the lab's own host directory; Memory refused | **Fixed** — both refuse the root with the new `ErrRemoveRoot` (`fs.errors.RemoveRootError`) |
| 4 | Raw `syscall.ENOTDIR` escaped from `osDir`, matching no `errors.Is` target; the backends disagreed | **Fixed** — `osDir.convert` reproduces `error_tools.py`'s FILE/DIR errno tables. Note the two backends legitimately **stay** different for `listdir` on a path under a file (`ResourceNotFound` on mem, `DirectoryExpected` on osfs) — that is Python, not a port bug |
| 5 | `memFS` created the node at Close, so `Remove(parent)` then `Close` left an orphan reachable by `Exists` but invisible to `ReadDir` | **Fixed** — the entry is created (and truncated) at open time, as pyfilesystem does |
| 6 | `Sub(fsys, "")` / `Sub(fsys, "/")` returned the parent, so `Type()` was `"os"`/`"memory"` | **Fixed** — always wrapped; `prefix` may now be `"."` |
| 7 | `subFS.Append` made the documented read-modify-rewrite fallback unreachable | **Fixed** — both paths go through `openAppend` (§7.2) |
| 8 | `subFS.Open`/`Stat`/`ReadDir` accepted names `fs.ValidPath` rejects, unlike the other two implementations | **Fixed** — io/fs contract enforced on the read side; the write side still takes pyfilesystem paths. `FS`'s doc comment corrected: it is not true that "every entry point runs CleanPath first" |
| 9 | Mid-pattern `$` silently returns the wrong count | **Documented + pinned**, not fixed — §5.9. No faithful RE2 emulation exists |
| 10 | Non-UTF-8 *filenames* are rejected where OSFS carries them | **Documented**, not fixable inside `fs.FS` — §5.10 |

Two review suggestions were **not** taken, with evidence:

- *"Make `copyInto` not commit on a failed copy, or remove the partial file."*
  Python leaves partial state on a mid-copy failure too — `fs.upload` has no
  rollback — so discarding the buffer would be a divergence, not a fix. The
  symptom the reviewer saw (an empty regular file at `dst/linkdir`) was the
  symlink bug, and it is gone.
- *"Give `osDir.ReadDir` the same error as `memFS.ReadDir` for a path under a
  file, for Memory/OSDir parity."* The two pyfilesystem backends genuinely
  disagree there (`listdir("/afile/child")` is `ResourceNotFound` on MemoryFS
  and `DirectoryExpected` on OSFS), so forcing parity would break faithfulness.
  Each side now matches its own oracle. Parity **was** restored everywhere
  Python has it, which is every other row of the table above.

### 8.1 Independent re-verification by the fixer

Every one of the 10 dispositions above was re-derived from scratch against the
live stack before being accepted — none was taken on trust:

- All 10 findings **confirmed genuine and correctly fixed**; none rejected.
- `posixpath.dirname` re-checked on 11 inputs including the `"//a"` → `"//"` and
  `"///"` → `"///"` corners; `posixDirname` is byte-exact.
- The FILE/DIR errno split re-probed on both backends: the code matches each
  backend's own oracle, including the two rows where the backends legitimately
  disagree (`listdir` under a file: `ResourceNotFound` on mem, `DirectoryExpected`
  on osfs; likewise `removedir`). The "don't force parity" call was right.
- All three oracle tables **replayed against live CPython/pyfilesystem2**:
  522 / 140 / 23 rows, **0 mismatches**. The divergent corpus really does target
  the gap — 120 of its 140 rows carry a non-terminal `$`, a class the 522-row
  matrix contains **zero** of, confirming the original "0 disagreements" was
  true but vacuous.
- A fresh **36-scenario × 2-backend differential** (Go dump vs `FilesystemMixin`
  dump) was run: **72 comparisons, 8 mismatches**, all four scenarios accounted
  for — 2 are the pinned §5.9 `$` gap, 2 are §5.12 (`CreateFailed`), 2 are
  §5.11 (newly found, above), and 2 are `copydir` **partial state after a broken
  symlink**, where Python's own two backends disagree with each other (`mem`
  leaves nothing, `osfs` leaves an empty `/dst/zbroken`), so partial state on a
  mid-copy failure is genuinely unspecified in Python and is not a port bug.
  This independently vindicates the "don't roll back `copyInto`" call above.

Harnesses: `scratchpad/{fix_verify.py,diff_scen.py,goscen/main.go}`.
