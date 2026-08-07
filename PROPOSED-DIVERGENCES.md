# PROPOSED-DIVERGENCES

Redesign ideas outside the §0.2 sanctioned list, plus behaviour deltas that are already in the
tree and need a human to confirm or reverse. Each entry says which it is. A human decides,
possibly after 1.0.

## Deterministic MACs via kathara.machine driver opt
The network plugin derives deterministic MACs only when the `kathara.machine`+`kathara.iface` driver opts are sent; Kathara 3.8.3 sends `kathara.iface`+`kathara.link`, so default-path MACs are random per deploy. Adding `kathara.machine` would make MACs deterministic and match the (incorrect) claim in PORT_SPEC §9, but changes on-the-wire behaviour vs 3.8.3. Not implemented.

## FolderParser machine order (OQ-15a ruling applied)
Python's conf-less machine order is `glob` order = readdir order (ext4 hash order, effectively random per filesystem). Go sorts names. Any fixed order is within Python's observable envelope; vectors are marked order-insensitive. Recorded here because it is technically a behaviour change on any single given filesystem.
**Implemented** (`labfile/folder.go`, `machineFolders`). Everything else about the glob is reproduced rather than tidied: the leading-dot skip, the single level, `os.path.isdir`'s symlink following (a symlink to a directory IS a device, under both names — oracle-verified), the swallowed listing error, and the string-concatenated pattern — which makes an empty path glob the filesystem root *and* makes the scenario path itself part of the pattern. That last one is a real behaviour, not a curiosity: with `lab1/` next to `lab[1]/`, `glob(".../lab[1]/*/")` lists the subdirectories of the SIBLING `lab1` and never opens the bracketed directory, so `lstart -F` in a path containing `[`, `]`, `*` or `?` builds its devices from somewhere else while the scenario filesystem stays rooted at the literal path (oracle-verified). `globDirs`/`fnTranslate` reproduce CPython's `glob` + `fnmatch` for it rather than reading the directory literally, which would have been a silent improvement §0.1 forbids; the translation targets RE2, which differs from CPython's `re` in three places the function documents (no lookaround for the `(?!)` empty range, `]`/`[` escaping inside a class, no atomic groups) and in none that change which names match — 36,500 differential cases against `fnmatch.filter` and ~1,200 against `glob` agree exactly. Pinned by `TestFolderParserSortsNames`, `TestFolderParserFollowsSymlinks`, `TestFolderParserGlobsTheScenarioPath`, `TestGlobPatternMatching` and the nine `labfolder/` vectors (which cannot reach the path expansion: the harness materialises every scenario into a `t.TempDir()`, whose name never carries a metacharacter).

## Python `set` repr quoting in the plural MachineNotFound message
`DockerManager.py:151,155` / `KubernetesManager.py:111,115` interpolate a `set` of the argv
words that did not name a device. Those words are unvalidated (`LstartCommand.py:139` is an
`nargs='*'` positional with no type check), so CPython applies full `repr` quoting to them:
`kathara lstart "it's"` prints `{"it's"}` (double quotes), a backslash is doubled, control
characters become `\xNN`. The frozen ruling (`ERROR_CODES.md` §0.2, `JSON_CLI_CONTRACT.md`
§5.3) pins "elements single-quoted", so `kerrors.pythonSet` always single-quotes and the two
renderings differ for names outside `^[a-z0-9_]{1,30}$`. Identical for every name that
matches the device-name regex, i.e. for every case the frozen examples cover. Not
implemented; changing it would relitigate a frozen ruling. Pinned by
`TestMachineSetErrorQuotingIsAlwaysSingle`.

## `pack_files_for_tar`'s gzip wrapper carries no timestamp or filename
**Implemented** (`internal/util/tar.go`). CPython builds the wrapper as
`gzip.GzipFile(fileobj=NamedTemporaryFile(suffix='.tar.gz'))`, so its header carries
`time.time()` in MTIME and the random temp-file basename in FNAME (measured: `tmpsuqwxwrx.tar`,
different on every call). Two Python calls on identical input therefore differ in those bytes,
and there is no Python byte string to match. The Go writer emits neither field
(`gzip.Header{OS: 255}`, zero `ModTime`), which makes the whole payload reproducible. The tar
stream underneath — the layer both Docker and Kubernetes decompress before extracting, and the
only layer any consumer sees — is byte-identical, and member order is the caller's
(`ORDERING.tsv` row `utils.py:452`). Reversing this would mean re-introducing a clock read into
a pure function for no observable gain.

## `utils.is_platform` is deleted, and the deletion is not on PACKAGE_GRAPH's list
**Implemented** (`internal/util/doc.go`). `PACKAGE_GRAPH.md` §2.1 enumerates the sanctioned
`utils.py` deletions — `class_for_name`, `chunk_list`/`list_chunks`, `check_python_version`,
`pywintypes_*` — and `is_platform` is not among them, but it has no Go counterpart either. Its
three Python call sites (`DockerLink.py:333,368`, `LstartCommand.py:193`) all ask the same
question, "is this Linux", which Go answers with `runtime.GOOS` or a build tag, both of which
the compiler checks and a `is_platform("linux")` call would not. Behaviour-neutral today: all
three call sites are in packages that are not yet ported, so nothing has silently changed shape
around it. **Action for the contract owner:** either errata the `PACKAGE_GRAPH.md` §2.1 row to
list it, or say the helper must come back.

## `realpath` on Windows is a symlink walk, not `GetFinalPathNameByHandle`
**Implemented** (`internal/util/realpath_windows.go`). `ntpath.realpath` resolves by opening the
path and asking the kernel for its canonical name, which also canonicalises 8.3 short names and
on-disk case (`C:\PROGRA~1` → `C:\Program Files`, `c:\users` → `C:\Users`). The port walks with
`filepath.EvalSymlinks` plus ntpath's drop-the-last-component fallback, which resolves reparse
points but does neither canonicalisation, and which swallows every open error where
`_getfinalpathname_nonstrict` filters on a specific `allowed_winerror` list. Because the
resolved lab path feeds `GenerateURLSafeHash` (`Lab.py:76,90`), a Windows user who spells the
same directory two ways gets two lab identities where Python gives one. Windows-only, and only
for a spelling that differs in case or short-name form. Closing it means calling
`GetFinalPathNameByHandle` directly for the resolve step; proposed for post-1.0, when a Windows
host is available to test on.

## `PyInt` is bounded where CPython's `int()` is not
**Implemented** (`internal/util/pyint.go`). CPython's ints are arbitrary precision; Go's are 64
bits. `PyInt` returns `ErrPyIntRange` — deliberately *not* `ErrPyIntSyntax` — for any literal
whose magnitude exceeds `math.MaxInt`. The reachable input is a lab.conf interface number:
`pc1[99999999999999999999]=A` parses in CPython and is then rejected downstream by
`NonSequentialMachineInterfaceError`, whose message names interface 0 as missing on device pc1
(measured).
**Decision for `labfile`:** `RULINGS.md` OQ-14a says dispatch on the int-parse result only, so
only `ErrPyIntSyntax` may route a `key[arg]` line to the meta path. An out-of-range literal is
still a number and must stay on the interface path, where the sequential-interface check
produces the same error Python produces. Pinned by `TestPyIntRangeIsBounded`.
**Applied** (`labfile/labconf.go`, `interfaceNumber`): `ErrPyIntRange` claims slot
`math.MaxInt`. For a single out-of-range number the value is unobservable — the parse cannot
then succeed, and any non-zero slot fails `check_integrity` with the same ``Interface `0`
missing`` message. For two on one device it IS observable, and the earlier claim that it never
is was wrong: distinct numbers collapse onto one slot, so `pc1[99999999999999999999]=A` +
`pc1[88888888888888888888]=B` is `MachineCollisionDomainError` here and
`NonSequentialMachineInterfaceError` in 3.8.3 — a different frozen code at the JSON boundary —
and a repeated literal prints the saturated value where Python prints its twenty digits (both
measured). `math.MinInt` is spelled for the negative twin but is unreachable from a file: the
arg class is `\w+`, which has no `-`. `DIVERGENCES.md` 45; pinned by
`TestInterfaceNumberDispatch`, `TestInterfaceNumberSaturationIsObservable` and vector
`labconf/interface_number_overflow`.
**Action for the contract owner:** the fix is an arbitrary-precision interface key in `model`
(`Machine.interfaces` is keyed by `int`, and `AddInterfaceOptions.Number` with it), reached
through `labfile` carrying the out-of-range digit string instead of a saturated `int` — the
shape `DIVERGENCES.md` 37 already uses for ulimit rendering via `math/big`. It is a `model` API
change for a doubly-pathological input, so it is recorded rather than done; `ERROR_CODES.md`
concedes nothing here, which is the reason it needs a decision rather than a comment.

## `shutil.which`'s unset-PATH fallback is a constant, not `confstr`
**Implemented** (`internal/util/pypath_unix.go`). With PATH removed from the environment
entirely, `shutil.which` searches `os.confstr("CS_PATH")`, falling back to `posixpath.defpath`.
Both are `/bin:/usr/bin` on Linux (measured against the oracle) and the port uses that constant;
macOS's `confstr` additionally lists `/usr/sbin:/sbin`, which Go cannot read without cgo. The
delta needs a Kathará installed in `/usr/sbin` or `/sbin` *and* a process with no PATH at all.
Everything else about the function is a faithful port and is pinned against the oracle by
`TestPyWhich`.

## `term/tmuxdrv` is a sub-package where the frozen graph says `term/tmux.go` (spike OI-1)
**In the tree, needs a human to confirm or reverse — it touches a frozen document, so the Phase 3
fixer pass left the code where it is rather than deciding.** `PACKAGE_GRAPH.md:160` maps
`trdparty/libtmux/tmux.py` → `term/tmux.go`, and the frozen `term/` inventory (§3 row 2) lists
`tmux.go`, not a sub-package; the tmux spike landed the driver in `term/tmuxdrv/` instead, and
`term/pty.go`'s package doc already refers to it that way. Both §10 reviews raised it (one as a
BLOCKER) and `RULINGS.md` carries no ruling.

Why it landed there: the package imports nothing from the module (standard library only), so it
cannot create an import cycle and can be tested without Docker, a lab, a TTY, `kathara/` or
`settings/`; folding it into package `term` would mix it with the ConPTY/creack-pty layer and put
generic identifiers (`Driver`, `Window`, `WindowInfo`, `CommandError`) into that namespace.

Two closes, both cheap: **(a)** amend `PACKAGE_GRAPH.md` to list `term/tmuxdrv` (imports: none)
and keep the code as-is, or **(b)** fold the six files into package `term` as `tmux.go` and
rename the colliding identifiers. Either way it is a documentation-or-move decision, not a
behaviour one; nothing else in this review depends on which is chosen.

## `model.PackData` is registered but not implemented, and nothing tracks it
**In the tree as a gap, needs a human to schedule or re-assign.** `PACKAGE_GRAPH.md` §1.1 row 5
lists `PackData` among `model`'s contents and §2.2 maps `Machine.pack_data`
(`model/Machine.py:381`) onto `model/pack.go`; there is no `model/pack.go`. `model/doc.go`
explains why — the function needs a bytes-level `convert_win_2_linux` (`internal/util` exposes it
over a host *path*, because Python's `WriteTarFS` buffers on a real filesystem before writing the
archive) and a tar writer that can emit directory members (`util.WriteTar` emits regular files
only, which is all `pack_files_for_tar` ever needed) — but an explanation in a package doc is not
a tracked deliverable, and neither `PROGRESS.md` nor this file carried it.

Nothing is currently broken by the gap: `pack_data`'s only consumers are the two backends'
`copy_files`, and neither backend is ported. **Action for the contract owner:** either schedule
`model/pack.go` together with the `internal/util` widening it needs (a `[]byte` overload of
`convert_win_2_linux` and directory members in `WriteTar`), or move the symbol to whichever
package ends up owning the tar layer and errata the `PACKAGE_GRAPH.md` rows. It must not ship as
a silent omission: a backend written against the register will expect the symbol to exist.
