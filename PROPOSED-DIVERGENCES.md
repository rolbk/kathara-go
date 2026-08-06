# PROPOSED-DIVERGENCES

Redesign ideas outside the §0.2 sanctioned list, plus behaviour deltas that are already in the
tree and need a human to confirm or reverse. Each entry says which it is. A human decides,
possibly after 1.0.

## Deterministic MACs via kathara.machine driver opt
The network plugin derives deterministic MACs only when the `kathara.machine`+`kathara.iface` driver opts are sent; Kathara 3.8.3 sends `kathara.iface`+`kathara.link`, so default-path MACs are random per deploy. Adding `kathara.machine` would make MACs deterministic and match the (incorrect) claim in PORT_SPEC §9, but changes on-the-wire behaviour vs 3.8.3. Not implemented.

## FolderParser machine order (OQ-15a ruling applied)
Python's conf-less machine order is `glob` order = readdir order (ext4 hash order, effectively random per filesystem). Go sorts names. Any fixed order is within Python's observable envelope; vectors are marked order-insensitive. Recorded here because it is technically a behaviour change on any single given filesystem.

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
