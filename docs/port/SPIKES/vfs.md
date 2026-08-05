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
| `internal/util/tar.go` | `TarEntry`, `TarName`, `WriteTar`, `PackFilesForTar`, `PackFilesForTarMap` |
| `vfs/testdata/pysearch_matrix.json` | 522 `re.search(pattern, line)` results produced BY CPython 3.13 |
| `vfs/testdata/pyreadlines.json` | 23 `readlines()` results produced BY pyfilesystem2 |
| `internal/util/testdata/pack_*.tar` | 5 archives produced BY `utils.pack_files_for_tar` |

Tests: `vfs/{vfs,files,lines,walk,oracle}_test.go`, `internal/util/tar_test.go`.
**40 top-level test functions, 409 PASS assertions counting subtests, 0 failures**
(`vfs` 31 + 345 = 376, `internal/util` 9 + 24 = 33), clean under `-race`.

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

Two deliberate divergences, both for determinism:

- **Member order = sorted arcname.** *Ruling:* Python iterates
  `guest_to_host.items()`, i.e. dict insertion order, so its byte output depends
  on the caller's construction order (verified: `{"b":…,"a":…}` and
  `{"a":…,"b":…}` produce different archives). Tar assigns no meaning to member
  order, so sorting is inside the observable envelope and is what makes archives
  comparable. **Canonicalisation:** sort by the *canonical* (backslash-translated)
  name, since that is the only string a consumer can observe; ties broken by the
  original key so the order stays total when two distinct keys canonicalise to
  the same member (Python would emit both members too, last-one-wins on extract).
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
| `CopyDirectory` | existing destination | **merges**; same-named files overwritten, others survive; empty source dirs reproduced |
| `CreateFileFromPath` | CRLF / BOM / binary source | copied **byte-verbatim** — no normalisation on this path |
| `PackFilesForTar` | directory members | none exist — `pack_files_for_tar` writes regular files only |
| `PackFilesForTar` | empty input | a 10240-byte archive, not 1024 |

---

## 5. Divergences introduced by this spike

Sanctioned-by-determinism, all documented in code:

1. **Tar member order** is sorted, not insertion order (§2.4). Python's order is
   caller-dependent; tar has no ordering semantics.
2. **gzip MTIME zeroed** (§2.4). Python's is `time.time()`.
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
6. **`MkdirAll` permission.** Free functions pass `0o755`; pyfilesystem's
   `makedirs` takes no mode and lands on `0777 & ~umask`. Confirm `0755` is the
   wanted fixed value (it matters for `shared/` and device directories created
   under an OSDir lab).
7. **Windows `dirname`.** Python computes the parent with `os.path.dirname` on
   the **raw** path, so `create_file_from_string(c, "a\\b")` targets directory
   `a` on Windows and the root on POSIX. `CleanPath` treats `\` as a filename
   byte everywhere. Needs a ruling once the Windows build is in scope.
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
