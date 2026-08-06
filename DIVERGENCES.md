# DIVERGENCES

Python bugs found during the port. Behaviour is ported as-is (spec §0.1); fixes are post-port commits.

The final section is the exception: it records places where the **port** deliberately or unavoidably diverges from Python, rather than Python bugs reproduced faithfully.

## From Layer B vector generation (parser, validated against 3.8.3)

All ported as-is; vectors in `labfile/testdata/vectors/` pin the behaviour.

1. **`bridged_iface` in lab.conf is always fatal.** Stored as string, then `check()` compares it against ints → `TypeError`; with no interfaces → `NonSequentialMachineInterfaceError`. Docstrings imply it fills an interface slot; it cannot.
2. **Ulimit meta errors name the meta, not the device** — `add_meta` interpolates its `name` parameter instead of `self.name`.
3. **`pc1[ipv6]=false` stores the string `"false"` (truthy)**, while the API path stores a real bool for the same key. lab.conf and API disagree on the type.
4. **Trailing `#` comments only work after quoted values**; after unquoted values the comment text is swallowed into the value (or errors on interface lines).
5. **`LAB_*` values containing `=` crash** with an uncaught `ValueError: too many values to unpack`.
6. **A UTF-8 BOM breaks parsing of line 1** (`﻿` survives `strip()`), rejecting Windows "UTF-8 with BOM" lab.conf files.
7. **One badly-named directory (e.g. `Docs/`) kills the whole FolderParser run** — `lstart -F` fails outright on labs with any non-device folder next to the devices.
8. Cosmetic: invalid-volume-mode message ends with a trailing space; `OptionParser` errors embed CPython-runtime text (only the outer frame is portable — noted in ERROR_CODES).
9. **`utils.pack_file_for_tar` corrupts on text-mode CRLF streams** — sizes via `seek(0,2)/tell()` (raw bytes) but reads via `read().encode()` after universal-newline collapse, so a CRLF text handle raises `OSError: unexpected end of data`. Unreachable from the CLI; reachable from the Python-client API. The Go packer takes bytes and cannot express it.
10. **CPython 3.13 tarfile writes NUL `devmajor`/`devminor` for non-device members** (3.9-3.12 wrote octal zeros); Go matches the 3.9-3.12 form. 16 bytes/header + checksum delta; Python reads both fine. Masked in tar parity tests, asserted explicitly.

## From `utils.py` (validated against 3.8.3, `internal/util/testdata/utils/expected.json`)

All ported as-is; the fixture tables in `internal/util/*_test.go` pin the behaviour.

11. **`get_absolute_path` returns a *relative* path for a symlink loop** (`utils.py:60-62`). `os.path.realpath(..., strict=False)` gives up on a loop and returns the path unchanged, so the `os.path.islink(abs_path)` tail is true and the function returns `os.readlink(abs_path)` — the link's target as stored. With `loop1 -> loop2 -> loop1`, `kathara lstart -d loop1` resolves the lab path to the bare string `"loop2"`. The function's whole contract is "return an absolute path" and this is the one input class where it does not. Pinned by `TestGetAbsolutePathSymlinkLoopReturnsRelative`.
12. **`human_readable_bytes` picks the unit with floating-point arithmetic and is sometimes wrong** (`utils.py:421`). `int(math.floor(math.log(size_bytes, 1024)))` rounds `size_bytes` onto the nearest double first, so `2**50 - 1` divides to exactly 5.0 and 1125899906842623 bytes render as `1.0 PB` instead of `1024.0 TB`. Same artefact at `2**60 - 1`. The Go expression is spelled the same way so the artefact survives; pinned by the 2,900-value sweep in `TestHumanReadableBytesBulk`.
13. **`parse_docker_engine_version` deletes from the middle instead of truncating** (`utils.py:479`). Each dot-separated part contributes its *leading* digit run and the loop keeps going, so `20.10-beta.3` becomes `20.10.3` — a version that was never released and that compares as newer than `20.10.2`. Conversely a part with no leading digit stops the loop outright, so `v20.10.14` and ` 1.2` both yield `""`, which is then an uncaught `ValueError` inside `version.parse`.
14. **`get_executable_path` ignores the execute bit** (`utils.py:68`). The first branch tests only `os.path.exists and os.path.isfile`, so a readable non-executable file at the given path wins over a working `kathara` on `PATH`, and the terminal spawn then builds a command line that cannot run.

## From the `vfs` spike + Phase 3 review (port divergences, not Python bugs)

These are places where the Go port's observable behaviour differs from
pyfilesystem2, rather than Python bugs reproduced as-is. The full list with
probe evidence is `docs/port/SPIKES/vfs.md` §5; the three that are reachable
from the §7 client API and cannot be closed inside the frozen §6 interface are
registered here because both §10 reviews asked for them to be discoverable
outside the spike document.

15. **A mid-pattern `$` in a caller-supplied `searched_line` is missed.**
    Python's `$` is the lookahead `(?=\n?\z)`; RE2 has no spelling for it, so
    `pySearch`'s strip-one-`\n` retry only reaches a `$` the match *ends at*.
    `write_line_before(f, "X", "b$\n")` on `b"a\nb\nd\n"` is `1` /
    `b"a\nX\nb\nd\n"` in Python and `0` / unchanged in Go. The failure is
    **one-sided** — the emulation can only ever miss, never invent — which is
    what keeps it safe for the terminal-`$` patterns that do work. No in-tree
    pattern puts `$` anywhere but in terminal position. Pinned by
    `vfs/testdata/pysearch_divergent.json` (140 CPython-produced rows, 120 of
    them mid-`$`) and `TestPySearchDivergentCorpus`, which fails loudly both if
    the emulation ever invents a match and if the gap ever closes.
    Same family as the `\Z` and Unicode-`\b`/`\w` gaps (SPIKES/vfs.md §7.5).
16. **Filenames that are not valid UTF-8 are rejected, not carried.** `io/fs`
    requires `fs.ValidPath`, which requires valid UTF-8, so `CleanPath` turns a
    latin-1 lab filename into `fs.ErrInvalid`; pyfilesystem's OSFS carries
    arbitrary POSIX filename bytes through `surrogateescape` (`b"caf\xe9.txt"`
    lists as `'/caf\udce9.txt'`). Strictly *less* permissive, and not fixable
    while `FS` embeds `fs.FS`. Reachable with a Windows-authored lab directory.
    Divergence 5 in the spike covers non-UTF-8 *content*, which the port
    handles; this is about *names*.
17. **A `..` that escapes the filesystem root is clamped, not refused.**
    `fs.path.normpath` **raises** `fs.errors.IllegalBackReference`, so
    `create_file_from_string("x", "/../../esc.txt")` writes nothing and raises
    on both backends; `vfs.CleanPath` clamps to `esc.txt` and the call
    succeeds, creating the file at the root under a name the caller never
    asked for. Both behaviours are **contained** — neither can escape the
    filesystem root — so this is a silent wrong-target write, not a traversal
    hole. Found in Phase 3 by a Go-vs-Python differential (neither §10 review
    raised it); left unfixed pending a ruling because refusing adds an exported
    sentinel and changes `CleanPath`'s contract for every caller
    (SPIKES/vfs.md §7.11). Pinned by `TestBackReferenceIsClampedNotRefused`.
