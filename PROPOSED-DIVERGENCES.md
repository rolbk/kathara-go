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

## Two `PACKAGE_GRAPH.md` §2.7 signature sketches the `kathara` package could not take literally
**Implemented** (`kathara/execstream.go`, `kathara/registry.go`). The §2.7 mapping table sketches
two signatures inside prose cells, and both had to change shape — neither is a behaviour change,
both are additive, and both want an errata rather than a reversal.

`IExecStream.py`'s cell says `ExitCode() int`. The value is `exec_inspect(...)["ExitCode"]`, a
live API round-trip that fails for reasons unrelated to the command's status, and Python lets
that raise; a bare `int` would have to swallow it or panic, and PORT_SPEC §10 forbids the second
on a Python-reachable path. Shipped as `ExitCode(ctx) (int, error)` (DIVERGENCES.md 50).

`ManagerFactory.py`'s cell says `Register(name, factory)`. A two-argument form cannot answer
`Kathara.get_available_managers_name()` without constructing every backend, and constructing the
Docker one opens a daemon connection — which is exactly what Python's version avoids by
resolving the *class* and never instantiating (analysis/manager-foundation.md §7 gotcha 10).
Shipped as `Register(Backend{Name, FormattedName, New})`, so the display name is declared
alongside the constructor and `Registry.Available()` reads it without building anything. Pinned
by `TestRegistryListingDoesNotConstruct`.

**Action for the contract owner:** errata the two `PACKAGE_GRAPH.md` §2.7 cells, or say which
of the two properties (no swallowed inspect failure; no daemon connection to list backends)
should be given up instead.

## `model.Lab` has no way to remove a general option, which `deploy_machines` needs
**Worked around in the tree, needs a human to schedule the method or bless the workaround.**
`DockerMachine.deploy_machines` adds `lab.general_options['_mount_volumes']` before the deploy
fan-out and `del`s it afterwards (`DockerMachine.py:154,188`); `model.Lab` exposes `AddOption`
and `GeneralOption` but no removal, and `generalOptions` is unexported.

`backend/docker` writes the computed value back instead (DIVERGENCES.md 65). That is
behaviourally identical for every reader — `Machine.get_volumes` derives exactly
`policy in ("Prompt", "Always")` when the option is absent — and it does preserve the property
that matters, which is that a declined volume prompt does not persist into the next deploy of the
same `Lab`. What it does not preserve is the *key set*: `Lab.GeneralOptions()` lists one extra
entry after a deploy. Nothing in 1.0 renders general options, so the residue is latent.

**Action for the contract owner:** add `Lab.RemoveOption(name) bool` (the `model` package is out
of this stage's edit scope), or record that the write-back is the sanctioned shape and that
`GeneralOptions()` may carry `_mount_volumes` after a deploy. `backend/kubernetes` will meet the
same line (`KubernetesMachine.py` does the same juggling) and should not invent a second answer.

## `internal/util` needs a bytes-level `convert_win_2_linux` and an exported `which`
**Duplicated in the tree, needs a human to widen the utility package.** Two `internal/util`
functions exist only in the shape Python happened to need, and `backend/docker` needs the other
shape of each:

- `ConvertWin2Linux` takes a host *path*, because Python's `WriteTarFS` stages every file on a
  real filesystem before archiving it. A `vfs.FS` device folder can be in memory, so `pack_data`
  needs the transformation over `[]byte`. `backend/docker/pack.go` carries a 25-line copy
  (`convertWin2Linux`), validated against the oracle over eight vectors in `TestConvertWin2Linux`.
- `pyWhich` — the `shutil.which` port — is unexported, and the D-6 `get_iptables_version`
  carve-out needs it. `backend/docker/iptables_linux.go` carries a posix-only copy
  (`lookIptables`). `os/exec.LookPath` is not a substitute: it `Clean`s a relative PATH entry and
  answers differently for an unset PATH, which is the case a daemon-launched process hits.

Both copies are small and both are tested, but two implementations of a byte-for-byte parity
function is exactly the drift PORT_SPEC §10 warns about, and `backend/kubernetes` will want the
first one too (`KubernetesConfigMap` base64s the same archive).

**Action for the contract owner:** export `util.Which` and add a `util.ConvertWin2LinuxBytes(name
string, content []byte) []byte`, then delete the two copies; or record that per-package copies are
acceptable and pin them against each other with a shared vector file.

## `retrieve_files` extracts tar members without sanitisation
**Faithful in the tree, needs a human to decide whether to harden.** `DockerMachine.retrieve_files`
calls `tarfile.extractall(path=dst)` with no `filter=`, i.e. the fully-trusted extraction, so an
archive carrying `../` components writes outside `dst`. docker-backend.md gotcha
28 rules that the port reproduces it and records a proposal rather than silently hardening, which
is what `backend/docker/pack.go`'s `extractTar` does — for `../`, for the exact mode/mtime restore
and for the root-only chown. The one shape it does NOT reproduce is an ABSOLUTE member name, which
`os.path.join` honours and `filepath.Join` roots under `dst`; that is DIVERGENCES.md 69, taken
because Docker's `get_archive` cannot emit one and reproducing it would widen the very hole this
section asks to close.

The exposure needs a container that is already hostile, and the path is chosen by the user running
`kathara`. But the fix is three lines (reject a member whose cleaned path escapes `dst`), it
cannot break a legitimate `docker cp`-shaped archive, and "we reproduced the traversal" is a poor
sentence to have to write later.

**Action for the contract owner:** approve hardening `extractTar` (and record it as a sanctioned
divergence), or confirm the faithful behaviour ships as-is.

## Two `PACKAGE_GRAPH.md` rows the Docker backend could not take literally
**Implemented, both want an errata rather than a reversal.**

§4's platform table lists `backend/docker/tty_unix.go` / `tty_windows.go`. The split existed in
Python because the two session classes reached into docker-py privates for a Unix fd or a Windows
named pipe; the Go SDK hands back a `types.HijackedResponse` holding a `net.Conn` on both
platforms (it uses go-winio for the npipe itself), so there is one implementation, in `tty.go`,
and no platform code to split. DIVERGENCES.md 66.

§2.8 puts `Machine.pack_data` in `model/pack.go`, which does not exist; `backend/docker/pack.go`
carries it (DIVERGENCES.md 67, and the `model.PackData` gap already recorded above).

**Action for the contract owner:** errata the two rows, or say which of them must be honoured
literally.

## `backend/kubernetes` needed a second copy of `pack_data` and of `shlex.split`
**Implemented as copies, wants a decision.** The section above already asks for
`model.PackData` and for `util.ConvertWin2LinuxBytes`/`util.Which` on behalf of
`backend/docker`. The Kubernetes backend has now made the same copies, because
PACKAGE_GRAPH.md §1.2 gives the two backends no edge to each other — and that
separation is not incidental, it is what lets a `nok8s` build drop `client-go`
(PORT_SPEC §0.2 #8). So the choice is not "share between the backends"; it is
"move the symbol down into `model`/`internal/util`, or accept two copies pinned
against the same vectors".

Duplicated: `Machine.pack_data` plus its `convert_win_2_linux` and `extractTar`
tails (`backend/kubernetes/pack.go`, DIVERGENCES.md 82) and `shlex.split`
(`backend/kubernetes/shlex.go`, DIVERGENCES.md 83). `shlex.join` is NEW — only
the Kubernetes backend needs it, for `MachineBinaryError.binary` — and would
belong next to `shlex.split` wherever that lands.

**Action for the contract owner:** schedule `model.PackData` and a
`util.ShlexSplit`/`util.ShlexJoin` pair, then delete the four copies; or record
that per-package copies are acceptable and require the vector files to be shared
(`backend/kubernetes/testdata/shlex.json` is the CPython-derived one).

## The 180 s Kubernetes watchdog answers `context.DeadlineExceeded`
**Implemented under the OQ-10 ruling, wants confirmation of the exit code.**
PACKAGE_GRAPH.md §2.8 sanctions replacing `os.kill(os.getpid(), SIGINT)` with a
context deadline and requires the `kubectl -n {hash} get pods` message to be
preserved; both are done (DIVERGENCES.md 72). What the ruling does not say is
what the CLI should then PRINT and EXIT with. The port answers
`context.DeadlineExceeded` from `DeployMachines`, on the reading that Python's
SIGINT produced exit 0 plus the interrupt warning (JSON_CLI_CONTRACT.md §6.2)
and that a cancellation is the closest thing the port has to that. A reader who
expects a timeout to be an ERROR — exit 1, code `Connection` or a new one —
would be surprised, and `cmd/kathara` has not been written yet, so the decision
is still free.

**Action for the contract owner:** confirm that a startup timeout exits 0 with
the interrupt warning, or name the error code it should carry instead.

## `internal/cliout` needs an edge to `labfile`, which the frozen graph omits

`PACKAGE_GRAPH.md` §1.2 gives `internal/cliout` the edges `kerrors`, `model`,
`event`, `kathara`. It needs one more: `labfile`.

`JSON_CLI_CONTRACT.md` §5.4 pins two structured fields on the `Syntax` and
`Value` codes — `file` (string) and `line` (int) — "when the message carries
them (`In {conf_name} - Line {n}` variants)". The only type that carries those
two values is `labfile.ParseError`, whose `File` and `Line` are struct *fields*
and not methods (`ERROR_CODES.md` §0.3 freezes that shape), so `errors.As` over
the concrete type is the only way to read them. Without the edge, a malformed
`lab.conf` produces `{"code":"Syntax","message":"In lab.conf - Line 2: …"}`
with the two contract fields silently missing.

The edge introduces no cycle: `labfile` depends on `kerrors`, `internal/util`,
`model` and `vfs`, none of which reaches `internal/cliout`. It is one import
and one `errors.As` arm (`internal/cliout/errors.go`, the `parse` case of
`addErrorFields`), pinned by `TestErrorEnvelope`'s parse-failure row.

The alternatives were worse: a package-level extractor registry that
`cmd/kathara` fills would make the envelope's field set depend on init order,
and moving the fields onto an interface would change a shape `ERROR_CODES.md`
§0.3 froze.

**Action for the contract owner:** add `labfile` to `internal/cliout`'s row in
`PACKAGE_GRAPH.md` §1.2, or drop the `file`/`line` fields from
`JSON_CLI_CONTRACT.md` §5.4.

## `kathara linfo --format json` is a usage error, not a machine-readable stub

`JSON_CLI_CONTRACT.md` §1.1's table row for `linfo` reads
"FeatureNotAvailable stub in 1.0 (§5.6): errors in every mode" in the `human`
column, with an em dash in the `json` and `jsonl` columns. Taken literally —
which is how the port took it — `linfo` declares no `--format` at all, so
`kathara linfo --format json` is an unknown-flag usage error with exit **2**,
and the only way to see the deferral is the human line
`CRITICAL (FeatureNotAvailable) The linfo command is not supported…`.

That is defensible (the em dashes say the two modes do not exist for this
command) but it means a scripted client that probes `linfo` cannot parse the
refusal, and the phrase "errors in every mode" reads as though it could. The
alternative is one line: give `linfo` a `--format {human,json}` and let it emit
E13 with `"feature":"linfo"`. It is additive under §9.2 either way, so the
choice can be made after 1.0 without a contract version bump — but not
silently, because the exit code differs (2 today, 1 then).

**Action for the contract owner:** confirm the literal reading, or say that
`linfo` takes `--format` and errors through E13.

## `kathara config` is exempt from the startup settings check

`src/kathara.py:71` skips `Setting.check()` for any command whose name
*contains* `"settings"`. `config` is new in the port (`PORT_SPEC` §3.2 item 2)
and does not contain that substring, so a literal port would run the check
before it — meaning `kathara config set manager_type docker` would fail with
`SettingsError: Manager Type not allowed.` on exactly the file it is being
asked to repair, with no way out but deleting the file by hand.

`cmd/kathara/root.go` therefore skips the check for `config` as well as for the
substring test. `JSON_CLI_CONTRACT.md` §6.2 already pins `config` as a fifth
member of the Ctrl-C warning whitelist "since Python has no entry for it",
which is the same reasoning applied to the neighbouring list; this note asks
for the same treatment to be written down for the check.

**Action for the contract owner:** confirm that `config` skips the startup
settings check, or say that a broken `manager_type` must be repaired by editing
the file.

## `exec --wait` still polls stdin for the ENTER override in `json`/`jsonl`
`JSON_CLI_CONTRACT.md` §1.5 pins the machine formats as "no keyboard override; the CLI simply
waits for `/tmp/EOS`", and §1.3 as "stdin is never read for interaction". The wait loop that
implements the override lives in the backend (`backend/docker/machine.go`, the
`util.WaitUserInput()` call in the startup-wait loop) and is unconditional, so `kathara exec
--wait --format json` still breaks out of the wait the moment stdin is readable — and a closed
or redirected stdin, the normal case for a scripted client, is readable at EOF immediately
(`internal/util/input_unix.go` says so in its own doc). The wait is therefore skipped rather
than performed, silently.

Closing it needs a no-override wait shape plumbed from `cmd/kathara` through `kathara.WaitPolicy`
into the backend loop — three packages, none of them `cmd/kathara`, so the CLI fixer could not
make the change. **Action for the contract owner:** either add the field (a
`WaitPolicy.NoUserOverride` bool that the Docker and Kubernetes loops consult, set by `runExec`
when `Format.Machine()`), or errata §1.5 to say the override is unconditional. Not implemented.

## Terminal modes that this build cannot drive report `NotSupported`, not `FeatureNotAvailable`
`cmd/kathara/terminal.go` used to raise `FeatureNotAvailable` with the feature tokens
`terminal-multiplexer` and `terminal-external`. `ERROR_CODES.md` §5 closes that token set at
`lab.ext`, `linfo`, `stats-sampling` and `webhooks`, with frozen messages, and §9.1 freezes the
registry; `kerrors/errors.go` calls it "the closed set of 1.0" in its own doc. The two invented
tokens are gone: selecting a terminal the build cannot drive now raises `kerrors.ErrNotSupported`
("External terminal emulator `<name>` is not supported in this release. Set `terminal` to TMUX,
or use --noterminals."), which is a live code in the frozen taxonomy with no closed message list.
Reachable only in human mode today, since terminals are skipped under the machine formats.
**Action for the contract owner:** if the deferred terminal modes are meant to be `feature`
tokens, add them to §5 and the port will switch back.

## Linking bubbletea makes every command query the terminal at startup (mitigated)
`bubbletea@v1.3.10/tea_init.go` calls `lipgloss.HasDarkBackground()` from its package
`init()`. That writes `ESC]11;? ESC\` plus `ESC[6n` to **os.Stdout** and blocks on os.Stdin
until the terminal answers or `termenv.OSCTimeout` (5 s) expires. It is bubbletea's own
documented v1 workaround and its comment says it is removed in v2. Because it is an `init()`
it runs for every command in the binary, not only `kathara settings` — and `PORT_SPEC` §3.3
puts a second bubbletea program (the multiplexer) in the same binary, so this arrives with the
frozen design rather than with one command.

Measured on this tree before the mitigation (`kathara -v`, pty harness):

| stdout | cost |
|---|---|
| a pipe | 0.01 s, nothing written (termenv short-circuits on a non-tty) |
| a terminal that answers | 0.01 s, 10 bytes written |
| a pty with no emulator behind it (`script`, `expect`, pty CI, serial console) | **5.02 s**, 10 bytes written, keystrokes typed during the wait swallowed |
| `TERM=dumb`/`screen*`/`tmux*` | 0.01 s, nothing written |

The Layer A goldens are **not** affected — the harness captures through pipes, row 1 — which is
why this did not show up as a golden failure.

Mitigation in the tree: `internal/charmguard`, imported for effect by `cmd/kathara/main.go`,
pins `lipgloss.SetHasDarkBackground(true)` so the query never runs. `true` is the value the
query itself produces when it fails or times out (`termenv` converts the unanswered `NoColor{}`
to black), and nothing in the port renders an adaptive colour — the settings form uses bold and
faint only, which are background-independent. Re-measured after the change: 0.01–0.02 s and
zero bytes on all four rows.

Two things a human should sign off on:

1. **It leans on package initialization order.** Go guarantees only that a package's imports
   initialize before the package itself; the order between two packages that do not import each
   other is unspecified. gc walks the importer's imports in sorted path order, and
   `github.com/KatharaFramework/…/internal/charmguard` sorts before
   `github.com/charmbracelet/bubbletea`, so the guard wins. An `init()` in `main` cannot do the
   job — `main`'s own init runs *after* every import's. If a future toolchain reordered this the
   guard would stop working and the five-second wait would return: a degradation, not a
   breakage. The durable fix is bubbletea v2, and `PACKAGE_GRAPH.md` §5 pins v1 ("do not mix
   v1/v2 Charm libs").
2. **`internal/charmguard` is a package `PACKAGE_GRAPH.md` does not list.** It is nine lines and
   one `init()`. It is deliberately *not* folded into `internal/cliout`, which the public
   `kathara` package imports: a library consumer building their own bubbletea program must keep
   their own background detection.

**Action for the contract owner:** confirm the guard package and the ordering assumption, or
decide that `term`'s multiplexer work (§3.3) should own the problem and move it there.

## `config set docker_config_json` takes a path, not the stored value (closes DIVERGENCES item 34)
`DIVERGENCES.md` item 34 left this "unruled": the key holds a base64 string, and nothing said
whether a caller hands it the base64 or the path to encode. The settings screen never stored
what the user typed — `KubernetesOptionsHandler.py:170-172` validates the path with
`DockerConfigJsonValidator` and then runs

    base64.b64encode(json.dumps(json.load(f)).encode()).decode()

so the stored value is the base64 of CPython's *re-serialization* of the file, not of its bytes.
`kathara config set docker_config_json <path>` and the settings form now both do exactly that,
through `settings.EncodeDockerConfigJSON` — one conversion, both entry paths, per §3.2 item 4.
The empty string still clears the key to `null`, which is the screen's "Reset value to Empty
String" item.

The re-serialization is reproduced rather than approximated (`settings/dockerconfig.go`):
CPython's `", "`/`": "` separators, `ensure_ascii=True` escaping, `repr(float)`, int/float
classification by literal shape, and dict semantics for a duplicate key (last value wins, first
position kept). Ten cases captured from CPython 3 pin it.

**Consequence:** there is no way to write a pre-encoded base64 through `config set`. Nothing in
3.8.3 could either, and a caller holding the base64 already has the file it came from.
**Action for the contract owner:** confirm the path reading, or say that `config set` is a raw
schema-value writer and the encoding belongs only to the form.

## `kathara config reset` takes an optional key
`PORT_SPEC` §3.2 item 2 lists `reset`; `JSON_CLI_CONTRACT.md` §3.12 pins the envelope for
`config reset` only. The implementation also accepts `config reset <key>`, which puts one key
back to its default and leaves the rest of the file alone; it emits the same
`{"settings":{…},"saved":true}` envelope, so a scripted client parses one shape either way.
Without it, undoing one `config set` means knowing the default and typing it, and for
`shared_cds` or `network_plugin` that is not something a user carries in their head.

Two behaviours worth pinning down: the default is read off a fresh `Defaults()` whose
`manager_type` is forced to the *current* one (the key set depends on it — `api_token` is not a
key of a docker-typed schema; `manager_type` itself is exempt from the forcing, or it would be
its own default and `config reset manager_type` would be a no-op reporting `saved: true`), and
the write goes through `Settings.Set`, so a default this host cannot accept is refused exactly as
`config set` would refuse it (`config reset terminal` fails on a machine with no xterm, same
message as `config set terminal /usr/bin/xterm`).
**Action for the contract owner:** confirm the sub-command and its envelope, or drop it.

## What the bubbletea settings form does differently from the consolemenu screen
The rebuild is sanctioned (§0.2 #1, §3.2 item 3) and the goldens do not look at this screen —
`kathara settings` has no `--format` and is not recorded — so these are recorded for the record
rather than proposed. Each one is a place where the new screen's *observable* behaviour differs
from `cli/ui/setting/*.py`, enumerated so a reviewer can check the list rather than the diff.

1. **The Docker Hub image list is gone; `image` is a plain text field.** The menu prefixed the
   "Choose another image" prompt with every tag `DockerHubApi.get_tagged_images()` returned.
   The webhooks are deferred (§0.3) and that call is a network round trip on the way to a
   settings screen — the same one that hangs behind a university proxy. The prompt underneath it
   is what remains. Restoring the list is a one-line change once webhooks return.
2. **`image` is also unvalidated: `ImageValidator` is on neither entry path.** Python's "Choose
   another image" prompt ran the answer through `ImageValidator` → `Setting.check_image` →
   `Kathara.check_image`, which pulls the image's manifest from the registry and verifies its
   architecture, re-prompting on a connection failure, an unknown image or a wrong architecture
   (`CommonOptionsHandler.py:73-83`). The form and `kathara config set image` accept any string,
   and the failure surfaces at deploy time instead. The reason is item 1's reason: validating
   needs a backend connection, and `settings` sits below `kathara` in the package graph
   (`PACKAGE_GRAPH.md`) precisely so that reading or writing a setting never opens one — a
   `config set` that hangs behind a proxy defeats §3.2 item 2's "works over SSH". The check
   itself *is* ported and lives with the rest of the validation, as §3.2 item 4 requires:
   `settings.Settings.CheckImage` takes an injected `ImageChecker`, and `settings.IsImageRejection`
   classifies the three rejection errors. Both have no caller today.
   **Action for the contract owner:** confirm that `image` stays unvalidated, or say which entry
   path should pay for a registry round trip.
3. **Edits are batched and saved explicitly (`s`), not written after every change.** Python's
   `update_setting_value` set the attribute, ran `check()`, called `save_to_disk()` and printed
   "Saved successfully!" for each item, so an abandoned session left half its edits on disk.
   The form keeps a working copy, `s` writes it, and quitting with unsaved edits asks
   (`y` save and quit / `n` discard / `esc` keep editing). Ctrl-C never writes. The message on a
   successful save is still `SAVED_STRING`.
4. **The `terminal` row is on every platform, and it always offers a free-text answer.** Python
   built no terminal item at all on Windows, and none of the `remote_url`/`cert_path` rows
   either (`exec_by_platform(linux, lambda: None, osx)`), so a Windows user could not reach
   those keys from the screen — while `kathara config set` reaches them everywhere. A screen
   that hides a writable key is the bug. For the same reason the row's "Choose another value…"
   escape is unconditional: `terminal_emulator_menu_osx` offered only `Terminal`, `iTerm` and
   `TMUX` with no prompt, so a macOS user who had a fourth emulator had to leave the screen.
   The suggestions themselves are still per-platform, and the answer still goes through
   `Setting.check_terminal`, which is what refuses an emulator this host cannot launch.
5. **There is no `last_checked` row**, matching the Python menu, which had no item for it. It is
   update-check bookkeeping, not a setting. `kathara config set last_checked` still writes it;
   that asymmetry between the file and the screen is Python's.
6. **Two submenus that contained only prompts are flattened.** "Choose Kathara prefixes" became
   its two rows (`net_prefix`, `device_prefix`), and the remote-Docker submenu became its two
   (`remote_url`, `cert_path`). The one menu item that wrote two keys — "Reset remote Docker
   connection to default", which cleared both — survives as an item on the `remote_url` row.
7. **The rows are in the screen's order, not the file's.** The three handlers' `append_item`
   order is reproduced, which for the docker addon puts `network_plugin` first on screen and
   last on disk. `kathara config list` walks the file order, which is frozen (§0.4).
8. **A prompt opens on the current value.** consolemenu offered no default except on
   `docker_config_json`, so pressing Enter at a prompt failed the regex and re-prompted. Here it
   is a no-op re-set. `docker_config_json` keeps its `DEFAULT_DOCKER_CONFIG_JSON_PATH` default.
9. **The consolemenu `RegexValidator`s are still not ported**, as `settings/validate.go` already
   records: `remote_url`, `api_server_url`, `api_token`, `cert_path` and `device_shell` take any
   string from either entry path. The URL one is `re.match` with no `re.IGNORECASE` against
   `[A-Z0-9]` character classes and rejects every lower-case domain a user could type. The form
   inherits the omission because §3.2 item 4 requires it to: the two paths share one validator
   set, and this one is not in it.
10. **With no TTY the command refuses** with `InvocationError` and names
    `kathara config get|set|list|reset`, instead of driving `curses` at a pipe.

Every other menu label, submenu order and stored value is the Python one, including the places
where the label is not the value it writes: the `Yes`/`No` rows over `true`/`false`, "Reset value
to Empty String" over `null`, `shared_cds`'s three `SharedCollisionDomainsOption.to_string`
sentences over `0`/`1`/`2`, and `image_pull_policy`'s "If Not Present" over `IfNotPresent`.

## Making the built-in multiplexer the *stored* default collides with a recorded oracle
`PORT_SPEC` §3.3 item 1 calls the built-in multiplexer "the default". The key that
selects it is `terminal` (there is no `terminal_mode` key; §3.2 item 1 freezes the schema —
see `term/mode.go`), so "the default" means `settings.Defaults().Terminal` should be
`MULTIPLEXER` on all three platforms instead of Python's
`exec_by_platform('/usr/bin/xterm', '', 'Terminal')`.

It was implemented that way and then reverted, because that default is **oracle-pinned**:
`settings/testdata/conf_roundtrip.json` records what CPython 3.8.3 does with a partial
`kathara.conf`, defaults filled in, and `TestRoundTripAgainstOracle` compares the result byte
for byte. A file that omits `terminal` round-trips through 3.8.3 as `"/usr/bin/xterm"`;
flipping the Go default makes it `"MULTIPLEXER"` and the vector fails. Passing it would mean
re-recording a Layer B artifact, which an implementer may not do on its own authority.

Where that leaves 1.0 as it stands:

| platform | stock `terminal` | mode |
|---|---|---|
| Windows | `""` | **multiplexer** (`ModeFor("")`) — §3.3's target platform gets the new default for free |
| Linux | `/usr/bin/xterm` | external adapter (ported, works) |
| macOS | `Terminal` | external adapter (ported, works) |

Unix users opt in with `kathara config set terminal MULTIPLEXER`. Existing installs are
unaffected either way, since their file already carries a value.

**Action for the contract owner:** either confirm that the stored default stays Python's and
the multiplexer is opt-in on Unix for 1.0, or approve the one-line change in
`settings/settings.go` plus a re-record of the `only_last_checked` vector in
`settings/testdata/conf_roundtrip.json` (that vector is the only one affected; the file's other
cases all carry an explicit `terminal`). Recorded in DIVERGENCES.md item 107 as it stands.

## `shlex.split` exists twice
`term/external.go` carries a second implementation of CPython's `shlex.split`, needed for the
one `gnome-terminal --` branch of the ported Linux adapter. The first is
`backend/docker/shlex.go`, which `term` may not import (PACKAGE_GRAPH.md §2: `term` does not
depend on a backend). Both spell CPython's two ValueError messages and both carry
`kerrors.ErrValue`; the `term` copy is the smaller subset — it does not need the rune-level
whitespace handling the `exec` path does — and is table-tested against the same shapes.

The obvious home is `internal/util`, which both may import, but moving it means editing a
`backend/docker` file that this change does not own. **Action for the contract owner:** approve
hoisting `ShlexSplit` to `internal/util` as a mechanical post-merge move, or accept the two
copies and the drift risk.
