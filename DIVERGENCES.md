# DIVERGENCES

Python bugs found during the port. Behaviour is ported as-is (spec §0.1); fixes are post-port commits.

The sections after the first two are the exception: they record places where the **port** deliberately or unavoidably diverges from Python, rather than Python bugs reproduced faithfully.

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

## From the `tmux` spike + Phase 3 review (port divergence, not a Python bug)

18. **Shared `"Kathara"` tmux session → one session per network scenario.** In
    3.8.3 every scenario opened from a path shares a single tmux session named
    `Kathara` (`trdparty/libtmux/tmux.py:29`, `session_name = "Kathara" if not
    session_name`, reached whenever `Lab.name is None` — i.e. for every
    path-parsed scenario; only vlab/API-named scenarios got their own session).
    Devices from unrelated scenarios end up as sibling windows in it, and
    killing that session kills all of them. Kathara-Go creates
    `kathara_<name-or-hash>` per scenario (`term/tmuxdrv/naming.go`;
    `Lab.hash` is the URL-safe md5 already used for container labels, Docker
    network names and the K8s namespace, so two invocations from the same
    directory land on the same session). Approved under spec §0.2 #2 / §3.3
    ("one session per network scenario named from the lab"); OQ-9, recorded in
    `docs/port/SPIKES/tmux.md` §3 and §6. **User-visible:** session names in
    `tmux ls` change, and devices of different path-parsed scenarios stop
    sharing one session. Pinned by `TestSessionName`,
    `TestSanitizedNameRoundTrip`, `TestEnsureSessionLifecycle`.

## From `setting/` + `validator/` (validated against 3.8.3, `settings/testdata/`)

Python bugs, ported as-is. Every one was probed against the live 3.8.3
`Setting` class; the fixtures under `settings/testdata/` are that oracle's
output.

19. **A per-scenario `kathara.conf` silently resets the whole active addon.**
    `Setting.load_from_disk` overlays the twelve base keys onto the loaded
    settings but calls `load_settings_addon()` first (`Setting.py:114`), which
    constructs a *brand-new* addon object, and only then applies the file's
    values to it. So `Command._load_custom_configuration` loading a scenario
    `kathara.conf` that sets nothing but `image` also resets `hosthome_mount`,
    `shared_mount`, `image_update_policy`, `shared_cds`, `remote_url`,
    `cert_path` and `network_plugin` to their defaults for the rest of the run
    — a user with `remote_url` configured silently deploys to the *local*
    daemon instead. Base keys do not behave this way, so the two halves of one
    file disagree. Pinned by `TestLoadFromDiskResetsAddonKeys` against 3.8.3's
    own output (`settings/testdata/conf_overlay.json`).
20. **`Setting.check()` writes to the default path, never to the one it was
    loaded from.** The weekly update bookkeeping ends in `self.save_to_disk()`
    with no argument (`Setting.py:206`), so a run that loaded a scenario's
    `kathara.conf` copies that scenario's settings over the user's global
    `~/.config/kathara.conf` the first time a week has passed. Pinned by
    `TestCheckWritesToDefaultPathNotTheLoadedOne`.
21. **The settings-screen URL validator rejects every lower-case domain.**
    `remote_url` and `api_server_url` are guarded by
    `RegexValidator(setting_utils.URL_REGEX)`, which is `re.match` with **no**
    `re.IGNORECASE` against a pattern built from `[A-Z0-9]` character classes
    (`cli/ui/setting/utils.py:13-18`). `https://docker.example.com:2376` is
    refused and `https://DOCKER.EXAMPLE.COM:2376` is accepted; only the
    `localhost` and dotted-quad alternatives work in lower case. It lives in
    the replaced consolemenu prompt layer, so the Go port does not carry it —
    `settings/validate.go` ports the three `validator/` classes and the
    menu-membership restrictions, and leaves those two keys unrestricted.
22. **A `kathara.conf` that is valid JSON but not an object crashes.**
    `load_from_disk` catches only `ValueError` from `json.load`
    (`Setting.py:110`), so `[1, 2, 3]` reaches `settings.items()` and escapes
    as an uncaught `AttributeError`. There is no portable traceback to
    reproduce and the port may not panic, so Go reports it as the invalid
    settings file it is (`Not a valid JSON.`). The bare document `null` is the
    same crash (`'NoneType' object has no attribute 'items'`) and gets the same
    answer; it needs its own guard in Go because unmarshalling `null` into a
    map is a no-op that returns no error. A second `AttributeError` escape is
    absorbed the same way: a file with an `"addons"` key holding an **object**
    clobbers `Setting.addons` itself (it is in `__slots__`), after which
    `hasattr` starts answering through `dict.get` and the next addon key in the
    file dies with `'dict' object has no attribute 'hosthome_mount'` — where
    the Go schema simply drops the unknown key and loads. (`"addons"` holding a
    *string* loads in Python too, because `hasattr` then raises through
    `str.get`.) Pinned by `TestLoadErrors/JSON_that_is_not_an_object`,
    `TestLoadErrors/the_document_null` and `TestUnknownKeysAreDropped`.
23. **An unknown `manager_type` dies in the addon factory, not in the manager
    check.** `manager_type: "podman"` makes `load_settings_addon()` raise
    `ClassNotFoundError` (`Setting.py:294` → `Factory.get_class`), which
    `kathara.py` does not catch — the user gets a traceback, never the
    `Manager Type not allowed.` message `_check_manager` exists to produce.
    `ClassNotFoundError` has no Go representation (`ERROR_CODES.md` §1.1: the
    registry deleted every raiser), so `settings` reports the message Python
    would have produced one call later. `str.capitalize()` in the same lookup
    is reproduced exactly, so `manager_type: "DOCKER"` still loads, saves
    verbatim, and is rejected only by `Check`. Pinned by
    `TestLoadErrors/unknown_manager_type` and
    `TestManagerTypeCaseIsToleratedOnLoad`.

### Port divergences in `settings/` (not Python bugs)

24. **Values of the wrong JSON type are refused instead of stored.** Python
    `setattr`s whatever `json.load` produced, so `"open_terminals": "yes"`
    loads, is truthy everywhere it is read, and is written back as the string
    `"yes"`. A typed struct (§3.2's rebuilt surface) cannot hold that, and
    coercing silently would be worse, so the port answers `Settings file is not
    valid: Setting `open_terminals` must be a boolean. …`. One consequence is
    narrower and worth naming: a hand-written **integer** `last_checked` is
    read as a float and written back with a `.0`, where Python round-trips the
    int. Files written by 3.8.3 itself always carry a float. Pinned by
    `TestLoadErrors/wrong_value_type`.
25. **`NaN` / `Infinity` in `kathara.conf` no longer load.** CPython's
    `json.load` accepts all three JavaScript literals by default and
    `json.dumps` writes them; Go's scanner rejects them, so such a file is
    reported as invalid JSON. The port still *writes* Python's spellings if a
    caller puts a non-finite value in `LastChecked`, so it can never emit a
    document it cannot describe. Unreachable from any file 3.8.3 wrote — and
    unreachable from the port's own CLI, because `kathara config set` refuses
    the `inf`/`infinity`/`nan` words `strconv.ParseFloat` would otherwise
    accept (`Setting `last_checked` must be a number.`). Python has no
    equivalent entry point to be faithful to: §3.2's scriptable path is
    port-new, and a validated `set` may not write a file the next `load`
    rejects. Pinned by `TestSetStringRejections`.
26. **Three `NILABILITY.tsv` rows are implemented against the register.**
    Row 46 asks for `last_checked` as an **int64 of unix seconds**. `time.time()`
    is a float and `json.dumps` writes every digit of it, so the
    `"last_checked": 1785923124.0260758` that every existing install carries
    could not round-trip through an integer, and §0.4 freezes that file; the
    field is a `float64` (`settings/testdata/kathara.conf.3.8.3` is the
    fixture that would fail). Rows 49 and 51 are the same kind of conflict:
    **`network_plugin` and `image_pull_policy` are `*string`, not `string`.**
    `NILABILITY.tsv` rows 49 and 51 propose a plain string with the default
    substituted for `null`. Substituting would rewrite a user's `"network_plugin":
    null` as `"kathara/katharanp_vde"` on the next save, which §0.4 freezes
    against, and Python does not substitute at consumption either — it
    interpolates `None` into the driver name (`DockerPlugin.py:30`,
    `DockerLink.py:139`). The pointer keeps the file byte-identical; consumers
    apply Python's own semantics. Flagged for the register owner.

## From the §7 Python client package (port divergences, not Python bugs)

These are places where `python/Kathara`'s observable behaviour differs from the
v3.8.3 manager API it replaces. The §7.2 losses (methods that raise
`NotSupportedError`, inventory dicts instead of docker-py objects) are spec'd and
listed in the facade's own docstring; what follows is everything *else* that a
consumer could notice, none of it closable inside the frozen JSON CLI contract.

27. **`get_machines_stats(all_users=True)` without root reports the CLI's
    message, not the API's.** v3.8.3 raises
    `PrivilegeError("You must be root to get devices statistics of all users.")`
    (`DockerMachine.py:1038`); the client goes through `kathara list -a`, whose
    frozen message is `You must be root in order to show all Kathara devices of
    all users.` (`ListCommand.py:58`). Same class, same failure, different
    `str(e)` — and `ERROR_CODES.md` §4 makes `str(e)` contract. Not fixable
    client-side: the message is produced by the binary. The same substitution
    applies to `get_machine(s)_api_objects(all_users=True)`.
28. **A disconnected (tombstoned) interface makes `deploy_lab` refuse, where
    v3.8.3 deploys.** `Machine.remove_interface` keeps the key and nulls the
    value, and `check_integrity` tolerates the hole (`model/Machine.py:136-141`),
    so v3.8.3 deploys a device with a `None` slot — and then crashes with
    `AttributeError` if the hole is at slot 0 (`DockerMachine.py:259-268`), or
    silently numbers the remaining interfaces differently if it is not. lab.conf
    cannot express a hole at all, so `_archive` raises `NotSupportedError` naming
    the interface. More honest, and strictly a refusal: nothing is deployed
    wrongly. Reachable after `disconnect_machine_from_link` (item 22).
29. **`exec` costs one extra process spawn.** v3.8.3 looks the container up
    before it creates the stream (`DockerMachine.py:779-781`); the client
    reproduces that with `kathara list -n <device>`, because raising
    `MachineNotRunningError` from the *call* rather than from the first iteration
    is what kathara-lab-checker's `try/except` around `exec(...)` depends on
    (`DNSAuthorityCheck.py:22-30` and 7 more sites). Residual race, identical in
    kind to v3.8.3's own: a device that dies between the probe and the exec
    surfaces as a mid-stream `error` event instead.
30. **`connect_machine_to_link` updates the model after the command, not
    before.** v3.8.3 calls `machine.add_interface(...)` and *then* the backend
    (`DockerManager.py:213-222`), keeping the interface in the model when the
    backend call fails. Across a process boundary that would leave the model
    claiming an interface the binary never created, so the client mutates only on
    success. Success paths are identical; failure paths differ in that the
    client's model stays clean. `disconnect_machine_from_link` likewise
    tombstones only after `lconfig --rm` succeeds.
31. **`connect_machine_to_link` numbers a bridged device's new interface as any
    other device's.** v3.8.3 reads `bridged_iface` off the container's labels and
    starts after it (`DockerManager.py:212-219`); the client has no container
    object and takes the model's next free number. Only observable on a device
    deployed with `bridged`, and only in the local model — the binary numbers the
    interface it actually creates.
32. **A named scenario with no directory is addressed for `connect_tty` through a
    synthesised scenario directory.** `connect` takes `-d`/`-v` only (contract §8
    gives `--lab-hash`/`--lab-name` to `exec`, `lclean` and `lconfig`), so the
    client writes a throwaway directory containing one `LAB_NAME=<name>` line and
    passes `-d`. `LabParser` assigns that through the name setter, which
    recomputes the hash from it (contract §3.0.1, A8), so the binary lands on the
    hash `deploy_lab` deployed under. Observable as a temporary directory that
    exists for the duration of the call. A scenario known **only** by hash still
    raises `NotSupportedError`: a hash cannot be turned back into its name.
33. **Device names outside the lab.conf grammar cannot be deployed.** The API
    accepts any string for `new_machine`, and v3.8.3's `deploy_lab` never
    round-trips through a file, so `Lab.new_machine("PC1")` deploys; the client
    ships a lab.conf, whose device-name class is `[a-z0-9_]{1,30}`
    (`LabParser.py:41`), so the far side answers `Syntax` for the offending line.
    Bounded serialization loss of the archive wire format, in the same family as
    the meta-value limits (quotes, newlines, empty values) the archive rejects
    up front.

## From `setting/` again (second review pass; port divergences, not Python bugs)

Numbered after the §7 client items to keep every identifier above stable; they
belong with items 24-26.

34. **`check()` stamps `last_checked` and rewrites the file on every stale run,
    where v3.8.3 does so only when GitHub answered.** `Setting.check` sets
    `checked = False` on `HTTPConnectionError` and skips both the stamp and the
    `save_to_disk()` (`Setting.py:207-213`), so an offline install retries the
    release check on every single run and never rewrites its file. The webhook
    is deferred (§0.3; `PACKAGE_GRAPH.md` §2.1 keeps `last_checked` in the
    schema per §0.4), and with no call left to fail only the success branch
    survives the removal: `Settings.Check` stamps and saves exactly when a week
    has passed. That is what a reachable-GitHub 3.8.3 does, and it is the state
    every existing install's file is already in — but an *offline* 3.8.3 host
    that never rewrote `~/.config/kathara.conf` will now see it rewritten
    (with the item-20 side effect if the settings came from a scenario
    directory). This is an autonomous ruling on deferred-feature semantics
    recorded here per §0.1, **not** a row of the frozen `RULINGS.md`; earlier
    drafts of `settings/check.go` cited a non-existent "OQ-12(b)" for it.
    Pinned by `TestCheckStampsAndSavesWhenStale` and
    `TestCheckDoesNotSaveWhenFresh`. The sibling question — whether
    `docker_config_json` should take the *path* the settings screen asks for or
    the base64 the screen stores — stays unruled: the key holds what it is
    given (the schema value, i.e. the base64) and `ValidateDockerConfigJSON`
    reads and encodes a path for whichever caller wants the screen's flow.
35. **A `kathara.conf` that is not valid UTF-8 is refused; a lone surrogate
    escape is loaded with a replacement character.** `open(path, 'r')` decodes
    with the locale's encoding, so on the UTF-8 locale every supported install
    runs, a stray `\xff` raises `UnicodeDecodeError` — a `ValueError`, caught
    at `Setting.py:110` and reported as `Not a valid JSON.`. `encoding/json`
    instead substitutes U+FFFD inside strings, which would load the file and
    silently rewrite the user's bytes on the next save, so `LoadFromJSON`
    rejects a non-UTF-8 document up front with that same frozen message. Two
    residues: under a non-UTF-8 locale (`LANG=C`) CPython would decode the same
    bytes as latin-1 and load mojibake, where the port refuses; and a lone
    surrogate *escape* (`"\ud800"`), which CPython's decoder keeps and
    re-emits verbatim, is folded to U+FFFD by `encoding/json` and would be
    saved back as `�`. Neither is reachable from a file 3.8.3 itself
    wrote. Pinned by `TestLoadErrors/not_UTF-8`.

## From `model/` (port divergences, not Python bugs)

Numbered after the `setting/` second pass so every identifier above stays
stable. The Python bugs `model/` reproduces — the ulimit messages naming the
option, the fatal `bridged_iface`, the tombstone crashes, the trailing space in
the volume-mode message — are items 1-8 and 28 above and are NOT repeated here;
what follows is where the *port* behaves differently from 3.8.3.

36. **`Machine.interfaces` is kept sorted by number at all times, where Python
    sorts only in `check()`.** PORT_SPEC §0.2 #4 and ORDERING.tsv
    (`model/Machine.py:67`) sanction the ordered slice and say `check()`'s
    re-sort "becomes a no-op" under it, which is what this implements. The
    residue is a window Python has and the port does not: between
    `add_interface(link, number=2)` and `check()`, 3.8.3 iterates in *insertion*
    order, so `Machine.__str__` prints interface 2 before interface 1 and a
    manager that deployed without calling `check_integrity` would wire them in
    that order. Both backends call `check_integrity` at the top of `deploy_lab`
    and `LabParser.parse` calls it too, so no CLI path can observe it; only an
    API user who numbers interfaces out of order and prints the device before
    deploying can. Pinned by `TestCheck/sequential`.

    A second window the sorted slice closes is on the *crash* path of
    `Lab.remove_machine` (item 28): the loop walks `interfaces.values()` in
    insertion order and dies at the first tombstone, so which collision domains
    have already lost their back-reference depends on that order. With slot 5
    added before slot 0 and slot 5 then disconnected, 3.8.3 raises before it
    touches slot 0 and that link keeps `pc1` in `link.machines`; the port walks
    0 first and clears it. Same error, same "device stays registered" effect,
    different surviving `Link.machines` — and `Link.MachineNames()` is public
    and feeds topology output. Exact parity is impossible once insertion order
    is gone. Pinned by `TestLabRemoveMachineTombstoneCrashOrder`.
37. **Integers wider than an int64 saturate.** Python's ints are arbitrary
    precision and five model values are not: a port number, a ulimit's soft and
    hard limits, a numeric sysctl value, `get_num_terms`'s result and
    `get_cpu`'s. All five are stored as fixed-width integers because that is
    what a container runtime accepts, and a literal beyond the range clamps to
    `math.MaxInt64` / `math.MinInt64` instead of erroring — keeping Python's
    "this is not a failure" behaviour. `pc1[cpus]=1e300` is the widest reachable
    one: 3.8.3 hands Docker the 301-digit `int(1e300)` (which Docker then
    rejects), the port hands it `math.MaxInt64`. Everything that *renders* such
    a number stays exact: `get_mem` and the ulimit soft/hard message go through
    `math/big`, so `nofile=-1:99999999999999999999999999` reports all 26 digits,
    as `kerrors.NewOptionUlimitSoftHard` requires. Pinned by
    `TestSaturate`, `TestAddMetaUlimit/hard_limit_beyond_int64` and the
    `num_terms` and `cpu` arms of `TestAccessorsAgainstOracle`.
38. **`str.isnumeric()` is approximated by the Unicode N categories, so the CJK
    ideographic numerals answer false.** Python's predicate is
    `Numeric_Type != None`, which also covers `一`, `二`, `〇` and their kin;
    Go's tables carry no Numeric_Type, and `unicode.Nd|Nl|No` is the closest
    total function. The gate has exactly one consumer, the sysctl int coercion
    (`model/Machine.py:182`), so the only affected input is a sysctl value made
    entirely of those characters: 3.8.3 accepts it as numeric and then dies with
    an uncaught `ValueError` from `int()`, while the port stores it as a string.
    Every other numeric-but-not-decimal character (`²`, `½`, `Ⅷ`) is in N and
    crashes identically. Pinned by `TestPyIsNumeric`.
39. **A meta explicitly set to Python `None` is not representable.** `add_meta`
    stores whatever it is given, so `add_meta("shell", None)` puts a None in the
    dict and `get_shell()` then returns None rather than the settings default —
    reachable from `KubernetesManager.get_lab_from_api` when `_MEGALOS_SHELL` is
    unset. The typed `Meta` spells "absent" and "None" the same way (`Scalar`'s
    zero value), so the port falls back to the default there. The Go `AddMeta`
    takes a string and cannot express the input at all; the k8s backend, when it
    is ported, must decide what it wants explicitly rather than inheriting a
    None.
40. **`Lab.remove_machine`'s InvocationError is reachable only as a nil device
    object.** Python takes `name` and `machine` as two optional arguments and
    raises `You must specify a device name or object.` when both are None
    (`model/Lab.py:326`); the port splits them into `RemoveMachine(name, …)` and
    `RemoveMachineObj(machine, …)`, which NILABILITY.tsv:27 offers as one of the
    two accepted shapes. The frozen message stays reachable —
    `RemoveMachineObj(nil, …)` returns it — but `RemoveMachine("")` is a
    MachineNotFound rather than an Invocation, because no registered device can
    be named the empty string. Pinned by `TestLabMachines/remove_by_object`.
41. **`Lab(None, None)` has no spelling.** It is a `TypeError` inside `re.sub`
    in 3.8.3 — the model does not guard it — and NILABILITY.tsv:24 maps Python's
    None path onto `""`, so `NewLabFromPath("")` is `Lab(None, "")`: the empty
    string is hashed and the filesystem is in memory. Nothing can ask for the
    crash. Pinned by `TestLabConstruction/empty_path_is_the_memory_filesystem`.
42. **`add_meta` with one of the six *plural* container names cannot poison the
    device.** `add_meta` has no case for `exec_commands`, `sysctls`, `envs`,
    `ports`, `ulimits` or `volumes`, so those names fall into the generic branch
    (`model/Machine.py:289-291`) and **replace the container with the string**.
    3.8.3 then dies at the first use of it: `str(machine)` raises
    `AttributeError: 'str' object has no attribute 'items'`, a following
    `pc1[sysctl]=…` raises `TypeError: 'str' object does not support item
    assignment`, and `pc1[exec_commands]=foo` makes the managers iterate the
    characters of `foo` as boot commands (all oracle-verified). It is
    lab.conf-reachable: `LabParser`'s `arg` class is `\w+` and it filters no
    key. The typed `Meta` (§0.2 #5) has no way to hold a string in a container
    field, so the port keeps the container intact and stores the value in
    `Meta.Extras` under the same name. What IS reproduced is the return
    contract, which is the CLI-visible half: the container was always present,
    so the first such line reports a previous value and `LabParser` prints its
    duplicate-meta warning, and a second one reports the string the first
    stored. The residue is that 3.8.3 crashes where the port carries on. Pinned
    by `TestAddMetaContainerNames`.
43. **The scenario path is opened literally, where `open_fs("osfs://…")` first
    runs it through a URL parser and a shell-style expansion.** `Lab.__init__`
    builds a pyfilesystem URL out of the raw path (`model/Lab.py:79`), so
    `parse_fs_url` splits it at a `?` or a `!` and `OSFS.__init__` then applies
    `expandvars` + `expanduser` + `abspath` + `normpath` to what is left.
    Measured: `Lab(None, "/x/my?lab")` opens **`/x/my`** — the wrong directory,
    while `Lab.hash` is computed from the full path — `Lab(None, "/x/a!b")`
    opens `/x/a`, and `Lab(None, "~")` opens the home directory. The port stats
    and opens the string it was given, and `CreateFailed`'s message quotes that
    string rather than its absolutised form. PORT_SPEC §6 replaced pyfilesystem
    with `vfs` wholesale, so there is no URL layer to reproduce this in, and
    reproducing it by hand would mean re-implementing `parse_fs_url` in order to
    open the wrong directory. CLI-reachable through `lstart -d` on a path
    containing `?` or `!` (`LstartCommand.py:150` realpaths the argument, which
    strips a leading `~` or `$VAR` case but not those two); every other path
    behaves identically.

## From `event/` (Python bug, validated against 3.8.3)

Ported as-is. The bug's *reproduction* is `internal/cliout`'s, not `event`'s —
`event` only has to make it expressible, which is why `PullProgress` carries
pointers.

44. **A Docker pull that fails mid-stream crashes the CLI with
    `KeyError: 'status'` instead of reporting the failure.**
    `DockerImage.pull` iterates `client.api.pull(..., stream=True, decode=True)`
    and dispatches every decoded line as `docker_pull_progress`
    (`manager/docker/DockerImage.py:61-62`). docker-py `_raise_for_status`es
    only the *initial* HTTP response and then hands the stream through
    untouched (`APIClient.pull` → `_stream_helper`), so a failure that happens
    after the stream opened — a layer download that dies, an expired registry
    token, no disk left — arrives as a line
    `{"errorDetail": {"message": …}, "error": …}` with **no `status` key**.
    `HandleDockerImagePull.update` opens with `progress['status']`
    (`cli/ui/event/HandleDockerImagePull.py:42`), so it raises `KeyError`, and
    nothing on the path has a `try`: it unwinds through `EventDispatcher.dispatch`
    (`event/EventDispatcher.py:85-86`) and out of `pull`, aborting the loop.
    Oracle-verified: `update({'errorDetail': {'message': 'boom'}, 'error': 'boom'})`
    → `KeyError: 'status'`; the same through `dispatch` → `KeyError: KeyError('status')`.
    The user sees a traceback naming a dict key rather than the registry's error
    message. Reachable on any flaky pull, which is the common case this handler
    exists for.
    `event.PullProgress` therefore spells `Status`, `ID` and `Detail` as
    pointers: a plain `string` would collapse the absent key to `""`, land in
    the handler's `else: return` branch, and let the failed pull go on to report
    success through `docker_pull_ended` — a silent fix of a crash, which §0.1
    forbids. Pinned by `TestPullProgressCanCarryAnErrorLine`; the handler side
    is `internal/cliout`'s to reproduce when it lands.

## From `labfile/` (port divergences, not Python bugs)

The Python bugs `labfile` reproduces — the swallowed trailing comment, the
BOM that breaks line 1, the `LAB_*` value with a second `=`, the fatal
`bridged_iface`, the reserved-name asymmetry, the one bad folder that kills a
`FolderParser` run — are items 1-8 above and the SURPRISES list in
`labfile/testdata/vectors/README.md`. All 142 vectors pass unchanged. What
follows is where the *port* behaves differently from 3.8.3.

45. **An interface number wider than a Go int saturates instead of being kept
    exact.** `LabParser` dispatches on `int(arg)` and CPython's ints are
    arbitrary precision, so `pc1[99999999999999999999]=A` is an *interface*
    line carrying a twenty-digit number. `util.PyInt` answers `ErrPyIntRange`
    for it — deliberately not `ErrPyIntSyntax`, so RULINGS.md OQ-14a's "dispatch
    on the int-parse result only" keeps the line on the interface path — and
    `labfile.interfaceNumber` then claims slot `math.MaxInt`. For ONE such
    number both implementations end at the same place, `check_integrity`
    reporting ``Interface `0` missing on device `pc1`.``, because any number
    other than 0 fails the sequence check identically. For two, the saturation
    is observable, in two ways (both measured against 3.8.3):

    - *Distinct* numbers collapse onto one slot. `pc1[99999999999999999999]=A`
      followed by `pc1[88888888888888888888]=B` is a hole in the numbering in
      Python — `NonSequentialMachineInterfaceError`, ``Interface `0` missing on
      device `pc1`.`` — and a collision here: `MachineCollisionDomainError`,
      ``Interface 9223372036854775807 already set on device `pc1`.`` A different
      class, a different frozen code and a different message, at the JSON
      boundary as well as the human one.
    - A *repeated* number gets the right class and the wrong bytes: Python
      prints the twenty digits it read, the port prints the saturated value.

    Closing either needs an arbitrary-precision interface key in `model`
    (`Machine.interfaces` is keyed by `int`), which is out of proportion to a
    doubly-pathological input; the honest record is here rather than a silent
    claim of parity. The negative twin is unreachable from a file at all — the
    arg class is `\w+`, which has no `-`. Recorded in PROPOSED-DIVERGENCES.md
    ("`PyInt` is bounded"), pinned by `TestInterfaceNumberDispatch` and
    `TestInterfaceNumberSaturationIsObservable`. Vector
    `labconf/interface_number_overflow`.
46. **`depgen.flatten` refuses to re-enter a name already on its path, where
    Python recurses until RecursionError.** `_order` has no cycle guard: it
    walks the inverted graph depth-first and a cycle makes it recurse until
    CPython's 1000-frame limit raises. Go has no such limit — the goroutine
    stack grows to 1 GB and then the runtime *fatals*, which is not recoverable
    and which PORT_SPEC §10 forbids on any Python-reachable path. `Flatten`
    therefore skips a dependency it is already inside, which terminates and
    still names every device exactly once. `ParseDep` calls `HasLoop` first and
    answers `MachineDependencyError` (`Machines' dependency loop in lab.dep
    file.`), so no lab.dep can reach the guard; it exists because `Flatten` is
    exported. `HasLoop` needs no such treatment — it already returns as soon as
    a name repeats on the path. Pinned by `TestFlattenCycleGuard`; the
    cycle-free behaviour is pinned against the oracle over 590 random graphs by
    `TestDepGenAgainstOracle`.
47. **`LAB_DESCRIPTION=` and an omitted `LAB_DESCRIPTION` are one value.**
    NILABILITY.tsv:25 freezes `Lab.description/version/author/email/web` as
    plain Go strings with `"" = absent`, because every 3.8.3 reader tests them
    for truthiness; Python distinguishes `""` from `None` in `Lab.__dict__` and
    nothing in 1.0 looks. The Layer B vectors *do* look — `expected.json`
    records `"author": ""` next to `"email": null` — so `vector_test.go` folds
    `null` onto `""` for exactly those five fields and asserts everything else
    about them unchanged (`metadataFields`). `LAB_NAME` is NOT folded: the
    tri-state survives as `Lab.HasName`, because an empty name hashes the empty
    string while an absent one hashes the scenario path, and the hash is the
    identity every container is created under. Pinned by
    `TestParseLabMetadataEmptyName`.
48. **`\w` is the Go toolchain's Unicode tables, not CPython's, and the two
    editions differ by 622 codepoints.** `labfile.isWordRune` is
    `[\p{L}\p{N}_]` (RULINGS.md OQ-14a), which decides meta names,
    collision-domain names and lab.dep device names. A full 0–0x10FFFF sweep
    against the oracle finds exactly one disagreement: U+2EBF0–U+2EE5D, CJK
    Unified Ideographs Extension I, which is `Lo` in CPython 3.13's UCD 15.1.0
    and unassigned in `unicode.Version` 15.0.0, the table Go 1.26 ships. A
    collision domain or a lab.dep device named in those ideographs parses in
    3.8.3 and is a syntax error here. Every other codepoint agrees, `\s`
    (`pySpace`) and `str.strip`'s set included, so the class *logic* is exact
    and this is toolchain versioning: it closes itself when Go's tables catch
    up, and cannot be closed inside `labfile` without shipping a private copy of
    the UCD.
