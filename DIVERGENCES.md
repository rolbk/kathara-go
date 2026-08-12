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

## From `kathara/` (port divergences, not Python bugs)

The public API package holds no behaviour of its own — it is interfaces, value
types, a registry and a facade that delegates — so everything here is a *shape*
decision that pins what the backends will be able to do. Each one is recorded
because it is visible from the §7 client API.

49. **The stats generators yield an ordered slice, not a map, and the order is
    canonical.** Python's `get_machines_stats`/`get_links_stats` yield a
    `Dict[str, IMachineStats]` filled by a `multiprocessing.dummy.Pool`, so the
    dict's insertion order — and therefore the row order of `kathara list` and
    of `lstart -l` — is thread-completion order and differs run to run.
    ORDERING.tsv rows 44, 59, 79 and 88 rule that the port sorts by the dict key
    instead; `kathara.MachinesStatsStream.Next` returns
    `[]MachineStatsEntry` sorted by `ID` (the container name on Docker, the pod
    name on Kubernetes) and the singular `MachineStatsStream.Next` picks the
    *first* entry by that sort where Python's `dict.popitem()` picked the
    last-inserted. A Go map could not have carried the rule at all: PORT_SPEC
    §10 rejects ranging over one whose order can reach a container. Row order
    becomes stable, which is strictly a narrowing of Python's observable
    envelope; the `popitem` change is only reachable when more than one
    container matches a device name, i.e. under `all_users` or across
    scenarios. The register asks for both, and both are pinned by the interface
    contract in `kathara/stats.go` rather than by a test here, because the
    backends that must honour it do not exist yet.
50. **`ExecStream.ExitCode` returns `(int, error)` where PACKAGE_GRAPH.md §2.7
    sketches `ExitCode() int`.** The value is a live API round-trip —
    `exec_inspect(...)["ExitCode"]` on Docker — which can fail for reasons that
    have nothing to do with the command's status, and Python lets that raise. A
    bare `int` would have to swallow the failure or panic, and PORT_SPEC §10
    forbids panicking on a Python-reachable path. The extra result is additive:
    it cannot change the code a successful call reports, which is what
    JSON_CLI_CONTRACT.md §3.6 makes the process exit code. **Action for the
    contract owner:** errata the §2.7 cell, or say the error must be dropped.
51. **`TTYSession` has no `fileno()`.** `ITerminalSession.fileno()` returns
    `Optional[int]` and existed for one reason: `TerminalRunner` used it to
    choose between an fd-readiness loop and a thread pumping blocking reads,
    with `None` meaning "no fd, use the thread" (NILABILITY.tsv:178). In Go a
    goroutine blocked on `Read` *is* the threaded pump and costs nothing, so
    there is no choice to make and no fd to expose. Two Python properties go
    with it: the trap that `fileno()` is tested with `is None` because fd 0 is
    valid, and the EOF asymmetry between the two pumps — the fd path treats an
    empty read as EOF and closes, the threaded path treats it as "poll again"
    (analysis/manager-foundation.md §7 gotchas 14-15). The port has one rule
    instead: `io.EOF` ends the session, a zero-byte read with a nil error does
    not.
52. **`ExecStream`, `TTYSession` and the four stats streams have a `Close`
    Python has no counterpart for.** CPython's refcounting closed the hijacked
    socket when the generator or the session object went out of scope. Go has no
    such moment, so an abandoned stream would hold a connection for the life of
    the process. Every `Close` is idempotent and safe before exhaustion, which
    is what `ITerminalSession`'s `_closed` flag effectively provided on the one
    object that had it.
53. **`LinkStats` emits a `user` key Kubernetes' Python class does not have.**
    One Go struct stands in for `DockerLinkStats` and `KubernetesLinkStats`, and
    the two `to_dict()`s are not the same shape: Docker's is
    `{network_scenario_id, name, network_name, user, enable_ipv6, external,
    containers}` and Kubernetes' is `{network_scenario_id, name, network_name,
    vxlan_id}` with no `user` at all (`KubernetesLinkStats.py:40-45`). The
    port's `user` has no `omitempty`, so a Kubernetes record serialises
    `"user":null` before `vxlan_id`, where Python's dict has no such key. This
    is the same treatment `MachineStats` gets, where JSON_CLI_CONTRACT.md
    §3.0.2 *requires* the nulled `user` on Kubernetes — so the two records stay
    consistent with each other, at the price of one key on the one record no
    contract pins. Nothing in 1.0 renders `LinkStats`; the divergence is latent
    until a shape is pinned for it, and pinning one is when to decide between
    keeping this and splitting the type. Pinned by `TestLinkStatsJSONShape` in
    `kathara/stats_test.go`.
54. **`kathara`'s *test* binary imports `internal/util`, which PACKAGE_GRAPH.md
    §1.2's "complete" edge list does not give it.** `TestLabRefMessagesMatchUtil`
    (`kathara/params_test.go`) puts `LabRef.RequireSingle`/`AtMostOne` side by
    side with `util.CheckRequiredSingleNotNoneVar`/`CheckSingleNotNoneVar` over
    all eight shapes of the lab-identifier triple, because the frozen §1.2 row
    denies this package the edge and so the counting and the two message texts
    are written twice — and a drift between the copies would be a wrong sentence
    in a user's terminal. The production edge list is unchanged: `kathara.a`
    links `kerrors`, `model`, `settings` and `event` and nothing else, and no
    cycle is possible in either direction since `internal/util` imports only
    `kerrors`. **Action for the contract owner:** say whether §1.2 governs test
    files; if it does, the comparison moves to a `cmd`-side test that owns both
    edges.

## From `backend/docker/` (port divergences and reproduced Python bugs)

The Docker backend is where the frozen naming and label schema lives
(PORT_SPEC §0.4), so almost everything in it is reproduced rather than
decided. What follows is the residue: the places where the Go SDK, the deferral
boundary, or a Python nondeterminism the register rules on made a choice
unavoidable. Reproduced Python bugs that needed no decision — the dead
`lab_hash` conditional in `get_container_name`, the `(':' or '@')` tag test, the
`['shared_mount']` literal list, the unanchored `eth{n}` sysctl match and its
`IFNAME0` corruption, the lexicographic `kathara.iface` sort — are carried in
code comments and pinned by tests, not listed here.

55. **The endpoint-sysctls DriverOpt is emitted in canonical sorted order.**
    `_create_driver_opt` joins a Python `set` (`DockerMachine.py:470`), so the
    value of `com.docker.network.endpoint.sysctls` differs between two runs of
    the same command and is visible in `docker inspect`; ORDERING.tsv row 40
    flags it as a known nondeterministic site and the accepted ruling on OQ-8
    picks a canonical order. The port sorts, and keeps the set's other property:
    a device sysctl that renders to exactly a baseline entry collapses into it
    rather than appearing twice. Pinned by
    `TestCreateDriverOptSortsCanonically` and `TestCreateDriverOptDedupes`.
    `_get_iface_sysctls` returns a sorted slice for the same reason
    (ORDERING.tsv row 39).
56. **The image check/pull order is the scenario's, not a hash order.**
    `deploy_machines` builds `set(map(get_image, machines))` and
    `check_from_list` iterates it (`DockerMachine.py:150`, `DockerImage.py:123`),
    so the order of the pull progress bars and the update prompts is
    hash-randomised per run. ORDERING.tsv rows 29 and 46 both say "!! collect
    into slice, dedupe preserving first occurrence"; the port does exactly that
    and `CheckFromList` takes an ordered slice.
57. **Stats streams re-query on every step and never accumulate.** Python's two
    stats generators are infinite, yield the SAME dict object each round, and on
    an empty result `yield dict()` and then FALL THROUGH — no `continue` — so
    the following step returns stale accumulated entries without re-querying
    (docker-backend.md gotcha 19). All three behaviours are artefacts of the
    accumulate-and-resample machinery PORT_SPEC §0.3 defers: with inventory-only
    stats there is nothing to accumulate. Each `Next` here re-queries and
    returns what is running now. The two observable properties that survive are
    preserved: an empty result is an empty batch and NOT the end of the stream
    (NILABILITY.tsv:64), and the stream never ends on its own.
58. **`DockerLinkStats`'s shared-mode KeyError is not reproduced.**
    `DockerLinkStats.__init__` indexes `attrs['Labels']['lab_hash']` and
    `['user']` directly (`stats/DockerLinkStats.py:27,30`), and `NetworkLabels`
    deliberately omits both in the `LABS` and `USERS` sharing modes — so
    constructing one for a shared collision domain raises `KeyError`
    (SYNTHESIS §1.2). The port reads the labels defensively and answers "".
    Reproducing the crash would fail an API call for a configuration this same
    backend produced two functions earlier, on a path 1.0 does not render: no
    CLI command reads `get_links_stats`, and JSON_CLI_CONTRACT.md pins no shape
    for it. Pinned by `TestLinkStatsSharedModeLabelsAreBenign`.
59. **`image.tags[0]` is made total.** `DockerMachineStats.__init__`
    (`stats/DockerMachineStats.py:42`) and `_delete_machine`'s shutdown warning
    (`DockerMachine.py:1112`) both index the first tag of an image that may have
    none, which is an `IndexError` (docker-backend.md gotcha 21). The port falls
    back to the image reference from the inspect. A device listing and a log
    line must not be able to fail on one untagged image, and PORT_SPEC §10
    forbids the panic the direct index would be. Pinned by
    `TestMachineStatsUntaggedImage`.
60. **`chardet` decoding is dropped at both of its sites.** `_exec_run` decodes
    an exec's stdout with `chardet.detect` before scanning it for the OCI
    runtime pattern (`DockerMachine.py:884-885`), and `connect` decodes the
    startup log the same way before writing it out (`:713-714`). The port
    matches the regexp against the bytes and writes the log bytes through
    unchanged. The pattern is pure ASCII and every encoding chardet can detect
    agrees with UTF-8 over the ASCII range for these bytes, so only a wide
    encoding (UTF-16) — which no OCI runtime emits — could differ; the log dump
    is a debug artefact no contract pins. It also keeps a charset-detection
    dependency out of the module.
61. **An unmapped HTTP status is indistinguishable from a 500.** Python sniffs
    `e.response.status_code == 500` at four sites. The Go SDK encodes the status
    as an `errdefs` sentinel and its own error wrapper makes the unmapped-status
    payload unreachable to `errors.As`, so `errhttp.ToHTTP` answers 500 for a
    502 or a 510 — and for an error that never came from the daemon. Every sniff
    pairs the status with a substring test on the daemon's message, which is
    what keeps the reachable cases right; the one Python behaviour this could
    have changed, `test_connect_interface_plugin_api_error`'s 510, is preserved
    because that error's explanation carries no plugin phrase either. Measured
    and pinned by `TestStatusCodeRoundTrips` and
    `TestPluginSniffCannotSeeAnUnmappedStatus`.
62. **`plugin.upgrade()` is ported as nothing, because it does nothing.**
    `check_and_download_plugin` calls it on every launch (`DockerPlugin.py:47`)
    and OQ-16 asks whether the sequence can be trimmed. It cannot be trimmed
    because there is nothing there: docker-py's `Plugin.upgrade` is a GENERATOR
    FUNCTION, so calling it and discarding the result constructs a generator and
    runs nothing — no privileges query, no pull, no `reload`, and not even the
    `DockerError('Plugin must be disabled before upgrading.')` its first line
    would raise for the enabled plugin that is the normal case.
    Oracle-verified against docker-py 7.2.0:
    `inspect.isgeneratorfunction(Plugin.upgrade)` is True. Issuing a real
    `PluginUpgrade` would therefore be the behaviour change, not omitting one.
63. **`pack_data`'s archive is deterministic and its member order is declared.**
    Python stages the tree on a real filesystem and tars it on close, so every
    member carries `time.time()` and the gzip header carries the random
    temp-file basename — two of its own runs already differ, and OQ-15(c) leaves
    the walk order deliberately unspecified. The port emits a fixed epoch, no
    gzip name or mtime, and the order `hostlab/`, `hostlab/{name}/`, the
    device's files sorted, then the four scenario files in their fixed order.
    Extraction is what the goldens compare (SYNTHESIS §1.4) and no two members
    can collide, so the order is unobservable past the untar. It is the same
    choice `util.PackFilesForTar` already makes.
    One byte-level detail is NOT the same choice: `backend/docker/pack.go` and
    `backend/kubernetes/pack.go` end the stream at `tar.Writer.Close`'s two zero
    blocks, so their archives are **unpadded**, while `internal/util/tar.go`
    pads out to CPython `tarfile`'s `RECORDSIZE` (20 × 512) the way
    `TarFile.close()` does. Nothing observes the difference: both packers hand
    the bytes straight to a `put_archive`, and every untar — the daemon's, the
    kubelet's, GNU tar's, Python's own — stops at the zero blocks and never
    reads the padding. `util.PackFilesForTar` pads because its archive LENGTH is
    pinned to CPython's by `TestWriteTarRecordPadding` (10240 bytes for an empty
    input, not 1024); these two are only ever compared after extraction.
    Unifying them on the padded form is a post-1.0 cleanup, tracked in
    PROPOSED-DIVERGENCES.md.
64. **`retrieve_files` extracts without sanitising members, deliberately.**
    `tarfile.extractall(path=dst)` with no `filter=` is a fully-trusted
    extraction, so an archive holding `../` components writes outside `dst`.
    docker-backend.md gotcha 28 rules that the port must not silently add
    safety that changes behaviour, so it does not: `filepath.Join` cleans the
    member name but does not clamp it, and `Join(dst, "../x")` lands beside
    `dst` exactly as `os.path.join` does. The archive comes from the Docker
    daemon relaying a path the caller chose, so the exposure needs a container
    that is already hostile. PROPOSED-DIVERGENCES.md carries the hardening
    request. The one member shape that does NOT match is an absolute name; see
    69.
65. **`_mount_volumes` is restored rather than deleted.** `deploy_machines`
    writes the option before the fan-out and `del`s it after
    (`DockerMachine.py:154,188`); `model.Lab` has no removal method and this
    stage may not add one. The port writes back the value it computed —
    `policy in ("Prompt", "Always")` — which is exactly what
    `Machine.get_volumes` derives when the option is ABSENT, so every reader
    sees the same answer. What it preserves that leaving the interactive
    prompt's reply in place would not is that a declined prompt does not persist
    into the next deploy of the same `Lab`. The leak on the error path is
    Python's too: the `del` is not in a `finally`.
66. **One `TTYSession` implementation, not the `tty_unix.go`/`tty_windows.go`
    pair PACKAGE_GRAPH.md §4 lists.** The Python split existed because the
    sessions reached into docker-py privates for a Unix fd (`handler._response`,
    `os.read`) or a Windows named pipe (`handler._handle.handle`,
    `win32file.ReadFile`). The Go SDK returns a `types.HijackedResponse` holding
    a `net.Conn` on both platforms — it uses go-winio for the npipe itself — so
    there is one implementation and no platform code to split.
    **Action for the contract owner:** errata the §4 row, or say the file must
    be split anyway.
67. **`Machine.pack_data` is implemented inside `backend/docker`.**
    PACKAGE_GRAPH.md §1.1 row 5 and §2.2 put it in `model/pack.go`, which does
    not exist — a gap PROPOSED-DIVERGENCES.md already tracks, and one this stage
    may not close, since `model` needs two widenings of `internal/util` (a
    bytes-level `convert_win_2_linux` and a `WriteTar` that emits directory
    members) that are outside its edit scope. `backend/docker/pack.go` carries a
    copy so the backend is not a stub. When the symbol lands, that file becomes
    a call. The same constraint put a twenty-line posix `shutil.which` in
    `iptables_linux.go`, because `internal/util`'s `pyWhich` is unexported.
68. **`mem_limit` above 2^63 saturates instead of being posted whole.**
    docker-py's `parse_bytes` is `int(float(digits_part) * units[suffix])`
    (`docker/utils/utils.py:433-441`), so the digits go through a binary64
    before they are scaled and everything above 2^53 is ROUNDED —
    `mem=9007199254740993b` posts …992, not …993. `parseMemory` reproduces that
    rounding exactly (oracle-verified vectors in
    `TestParseMemoryRoundsThroughFloat64`), and diverges only past int64:
    Python's result is an arbitrary-precision int, so it posts a `Memory` the
    daemon cannot decode into its `int64` field and answers 400 to, while the
    port saturates to `math.MaxInt64`, which the daemon ACCEPTS — Python errors
    where the port deploys. `mem=9223372036854775807b` is the smallest input
    that differs, because `float()` rounds it up to 2^63. Above ~1.8e308
    `float()` is `inf` and Python's `int(inf)` is an OverflowError; the port
    saturates there too rather than growing an error return on a signature no
    reachable scenario needs. `GetMem` preserves arbitrary digits
    (`model/machine.go`), so the inputs exist; no network scenario writes one.
69. **An ABSOLUTE tar member is rooted under `dst` rather than honoured.**
    Python's `extractall` builds each target with `os.path.join(path,
    tarinfo.name)`, and `os.path.join("/dst", "/abs/x")` DISCARDS `dst` and
    writes to `/abs/x`; `filepath.Join` roots it at `/dst/abs/x` instead. Every
    other fully-trusted behaviour is reproduced, including the `../` escape (64)
    and the mode/mtime/ownership restoration, so this is the single member shape
    that differs. It is unreachable from `retrieve_files`: the archive is built
    by the Docker daemon from a container path and `get_archive` emits only
    relative, basename-rooted names. Reproducing it would widen a hole
    PROPOSED-DIVERGENCES.md already asks to close, which is why the clamp
    stands and is recorded here instead.

## From `backend/kubernetes/` (port divergences and reproduced Python bugs)

70. **Empty lists, zero-valued fields and canonicalised quantities differ in the
    submitted object.** The Python Kubernetes client serializes an object by
    dropping only the attributes that are `None`, so it posts
    `"volumeMounts": []`, `"volumes": []`, `"imagePullSecrets": []` and
    `"readOnly": false`; client-go's structs carry `omitempty` and drop all four,
    and its `metav1` types add `"creationTimestamp": null` and `"status": {}`
    that Python has no field for. `resource.Quantity` additionally
    re-serializes canonically, so the `"2000m"` CPU limit Python posts is `"2"`
    here — which is what the API server stores whichever client posted it, since
    it canonicalises on the way in. None of the five differences is readable at
    the API server, and the Layer C goldens are the Python request bodies
    verbatim: `assertGolden` decodes each one INTO THE SAME GO TYPE and
    re-encodes, so both sides go through one serializer, and `assertRoundTrip`
    fails the test if any key of the raw oracle output does not survive that
    decode. **Action for the contract owner:** none needed unless a golden is
    ever meant to be a byte-for-byte HTTP body rather than an object.
71. **`get_env_var_value_from_pod` does not mutate the pod.** Python's
    implementation is `container_definition = containers.pop()`
    (`KubernetesMachine.py:777`), which REMOVES the container from the pod object
    it was handed, so a second call on the same pod answers `None` and the device
    silently falls back to `Setting.device_shell` (k8s-backend.md G7). It is also
    why `get_lab_from_api` has to read `pod.spec.containers[0]` BEFORE calling it
    (`KubernetesManager.py:706-707`). Reproducing it would mean handing out API
    objects that decay as they are read, and the observable consequence is
    reachable from no 1.0 path: the two callers (`connect`, `_delete_machine`)
    each fetch their own pod and read it once. `EnvVarValueFromPod` reads the
    LAST container, as `pop()` does, and leaves the pod alone.
    `TestEnvVarValueFromPod` pins both reads.
72. **The startup watcher is always joined; the 180 s watchdog cancels the
    operation instead of the process.** Python starts `_wait_machines_startup` on
    a non-daemon thread and `join()`s it after the deploy fan-out — but the join
    is SKIPPED when the fan-out raises, so the thread leaks and is kept alive
    until its `threading.Timer(180)` fires and `os.kill(os.getpid(), SIGINT)`s the
    whole process (k8s-backend.md G4/G5). Two halves, two answers. The leak is not
    reproduced: a leaked goroutine holds a watch connection open for the life of
    the process and has no observable behaviour to preserve, so `DeployMachines`
    cancels and joins the watcher on every path. The SIGINT is the OQ-10 ruling
    (PACKAGE_GRAPH.md §2.8): the `logging.error` text is preserved verbatim,
    `kubectl -n {hash} get pods` included, and the call then answers
    `context.DeadlineExceeded` — which the CLI renders exactly as it renders a
    Ctrl-C, exit 0 with the interrupt warning (JSON_CLI_CONTRACT.md §6.2), which
    is what the SIGINT produced. The watchdog is scoped to the one operation, so
    a second `lstart` in the same process is unaffected where Python's signal
    would have hit whatever was running.
73. **A pod without a `name` label is skipped by both watchers instead of killing
    them.** `_wait_machines_startup` and `_wait_machines_shutdown` index
    `event['object'].metadata.labels['name']` on an UNFILTERED watch over the
    namespace (`KubernetesMachine.py:248,627`), so a foreign pod raises `KeyError`,
    kills the watcher thread, and costs the deploy its `machines_deploy_ended`
    event — silently, because `threading.excepthook` writes to stderr and `join()`
    returns normally. A goroutine may not crash (PORT_SPEC §10), and the namespace
    belongs to one scenario, so nothing this backend creates is affected.
    The skip is the WATCHERS' only. The same index on the caller's own goroutine
    is reproduced as a crash, because there PORT_SPEC §10 does not apply and
    swallowing it would invent behaviour: `undeploy`'s wait-set comprehension
    (`KubernetesMachine.py:590`) returns the `KeyError`, since a `""` in the wait
    set is a name no event can satisfy and would turn Python's crash into a stall
    until the watchdog of 87 (`TestUndeployUnlabelledPodIsAKeyError`).
    Three READ-ONLY accessors do keep answering `""` and are covered by this
    entry rather than by a crash, because each is reached from a listing that
    PORT_SPEC §10 forbids failing on one foreign object: `NetworkLinkName`
    (`network['metadata']['labels']['name']`, `KubernetesManager.py:330` — an
    unlabelled NAD is silently left out of `selected_links` instead of raising),
    `linkStatsFor`'s use of the same accessor, and `reconstructDevice`'s
    `pod.metadata.labels["name"]` (`KubernetesManager.py:704`), which names the
    reconstructed device `""`. All three need a foreign object labelled
    `app=kathara`; entry 76 is the same reasoning for a NAD whose config will not
    parse.
74. **The pod watch is opened before the fan-out rather than inside the watcher
    goroutine, and a watch that will not open fails the operation.** Python starts
    the thread first and the thread opens the watch, which is a race it loses
    whenever a pod reaches Ready before `w.stream` is established. ORDERING.tsv
    row O17 states the requirement the code only approximates ("watch must be
    established before deletions to not miss DELETED events"), so the `Watch` call
    is made on the caller's goroutine and only the event loop runs concurrently.
    `machines_deploy_started` is still dispatched from the watcher goroutine, as
    CONCURRENCY.tsv row `KubernetesMachine.py:184` records.
    Moving the call moves its ERRORS too. In Python the `ApiException` from
    `w.stream` is raised inside the wait thread, where `threading.excepthook`
    prints it and `join()` returns normally (CONCURRENCY.tsv rows
    `KubernetesMachine.py:184,599`: "errors-swallowed … LOST"), so `lstart` and
    `lclean` carry on with no watcher and report success; here the same failure is
    returned before anything is deployed or deleted, carrying the `KubernetesAPI`
    code of entry 88. Fail-fast rather than silent-continue is the deliberate
    half of this divergence: a watch that cannot be opened means the operation
    cannot report what it is contracted to report.
75. **The VNI check-and-reserve is one critical section.**
    `_get_unique_network_id` is a membership loop followed by an assignment on a
    `multiprocessing.Manager` proxy dict (`KubernetesLink.py:319`); each proxy
    operation is atomic and the PAIR is not, so two workers probing to the same
    free VNI can both take it and put two collision domains on one VXLAN wire.
    CONCURRENCY.tsv row `KubernetesLink.py:319` rules the port fixes it — "make
    check+reserve one critical section under a sync.Mutex (fixes latent bug; safe
    deviation, note in port docs)" — which is this note. `TestVNIAllocatorIsRaceFree`
    pins it. The `multiprocessing.Manager` child process becomes the mutex
    (row `KubernetesLink.py:75`).
    The ids are also RESERVED BEFORE the fan-out, one per collision domain in
    scenario order, and each worker is handed the id it must create with.
    ORDERING.tsv row `KubernetesLink.py:77` requires it: Python calls
    `_get_unique_network_id` from the pool thread (`KubernetesLink.py:99`), so when
    two names probe to the same id — a sha256 collision modulo
    `MAX_K8S_LINK_NUMBER`, or a collision with a VNI the cluster already carries —
    WHICH collision domain keeps the base id and which takes the offset depends on
    thread arrival, and the register marks that non-deterministic and rules
    "reserve IDs sequentially in link order BEFORE parallel create; then create in
    parallel". Only the creates race now, and the assignment is a pure function of
    the scenario and the cluster's existing VNIs.
    `TestDeployLinksReservesIDsInScenarioOrder` pins it on the measured colliding
    pair `cd57`/`cd6099` (both hash to 1392701 under the `user123` seed), in both
    orders.
76. **A NetworkAttachmentDefinition whose `spec.config` will not parse is skipped
    rather than fatal.** `_get_existing_network_ids` does
    `json.loads(network['spec']['config'])` unguarded over every Kathará NAD in
    the CLUSTER (`KubernetesLink.py:348-350`), so one foreign object in a namespace
    someone else labelled `app=kathara` raises out of the listing and takes the
    whole `lstart` with it. The only consequence of skipping it is that its VNI is
    not reserved, and PORT_SPEC §10 forbids a listing that can fail on one object.
    The same reasoning covers `KubernetesLinkStats`, whose VNI is left nil.
77. **`copy_files` waits for the upload to finish.** Python opens a STREAMING exec,
    writes the archive to stdin and takes a single `next()`; `_exec_stream` breaks
    out of its loop BEFORE yielding once the buffer empties, so the generator falls
    through to `response.close()` and the websocket is closed without waiting for
    `tar` (k8s-backend.md G19). Python gets away with it because its write is
    synchronous; the Go transport owns the stdin reader and closing early would
    truncate an upload. The exec runs to completion here, and its output and exit
    status are still ignored — as Python ignores them.
78. **`KubernetesExecStream.exit_code` translates an OCI runtime failure into
    `MachineBinaryError`.** Python's streaming handle has NO `try/except ValueError`
    (`exec_stream/KubernetesExecStream.py:22-28`), so a non-integer exec status
    propagates as a bare `ValueError` and is never translated — while the
    non-streaming `_exec_all` DOES translate it (`KubernetesMachine.py:913-918`).
    The asymmetry decides whether `kathara exec --format jsonl` reports
    `MachineBinary` or an untyped crash for the same missing binary, and
    JSON_CLI_CONTRACT.md §4 pins the former for `exec`. Both paths go through
    `execExitCode` here.
79. **`docker_config_json` is base64-decoded before it is put in the Secret, and an
    undecodable value produces no Secret and no error.** Python's `data` dict holds
    strings the client serializes verbatim, and the setting is already the base64 of
    a `config.json` (`KubernetesSecret.py:34`); client-go's `Secret.Data` is
    `map[string][]byte` and its codec base64s on the way out, so handing it the
    setting string would double-encode. The value is therefore decoded here and
    re-encoded there, which is the identity for every input the API server accepts
    (`TestSecretDataRoundTrip`, `TestSecretGolden`). An input that is not valid
    base64 cannot round-trip and does not have to: Python sends it, the API server
    answers 400, and `_create_secret`'s `except ApiException` swallows it — so the
    decode failure takes the same exit, no Secret and no error, which is the
    observable behaviour (`TestSecretUndecodableConfigIsSilent`).
80. **`KubernetesConfigMap.delete_for_machine` cannot report a transport failure.**
    Python catches `ApiException` and lets anything else — a dial failure, a TLS
    error — propagate out of `_delete_machine` and fail one worker of the undeploy
    fan-out (`KubernetesConfigMap.py:48-53`). The Go signature has nowhere to put
    it: the method is called between the shutdown exec and the Deployment delete,
    and threading an error out of it would change which of the two failures the
    user sees. Both failures are swallowed here; the Deployment delete that follows
    fails on the same transport anyway, so the operation still reports one.
81. **`Setting.open_terminals = False` is not written.** `deploy_machines` mutates
    the process-wide settings singleton (`KubernetesMachine.py:174`, k8s-backend.md
    G20) so that the `machine_deployed` subscriber does not try to open a terminal
    on a device whose payload is a NAME rather than a `Machine` — which would
    `AttributeError` inside `HandleMachineTerminal.run`. PORT_SPEC §0.2 #10 makes
    the settings an injected value rather than a singleton, so there is nothing
    process-wide to mutate, and the payload variance is expressed in the types
    instead: `event.MachineDeployed` carries both `Machine` and `Name` and a
    subscriber tells the two apart. Megalos still never opens a terminal, because
    `ConnectTTY` is the only terminal path and the CLI drives it explicitly.
82. **`Machine.pack_data` is implemented inside `backend/kubernetes` as well.**
    The same gap DIVERGENCES.md 67 records for the Docker backend:
    PACKAGE_GRAPH.md §2.2 assigns the symbol to a `model/pack.go` that does not
    exist, and PACKAGE_GRAPH.md §1.2 gives the two backends no edge to each other
    — that separation is what makes the `nok8s` build possible at all — so the
    copy is duplicated rather than shared. `backend/kubernetes/pack_test.go` pins
    the same cases `backend/docker/pack_test.go` pins. When `model.PackData`
    lands, both files become a call. PROPOSED-DIVERGENCES.md already carries the
    request; the second copy makes it more urgent, not less.
83. **`shlex.split` is copied a second time, and `shlex.join` is new.** Same
    constraint as 82: `backend/docker/shlex.go` cannot be imported from here.
    `ShlexJoin` has no Docker counterpart — the Docker backend reads the missing
    binary out of the OCI regexp's capture groups, while `OCI_RUNTIME_RE` on this
    backend is the bare literal `OCI runtime exec failed` and
    `MachineBinaryError.binary` is `shlex.join(command)`, the whole command quoted
    (`KubernetesMachine.py:917`). Both halves are pinned against CPython vectors in
    `testdata/shlex.json`.
84. **The exec transport prefers WebSocket with a SPDY fallback.** Python's
    `stream()` upgrades to a WebSocket; client-go's default is SPDY. The port keeps
    Python's preference by making the WebSocket executor primary and falling back on
    an upgrade failure, which is what `kubectl` does and what an API server too old
    to negotiate the v5 protocol needs. Not observable in the exec's answers.
85. **`grace_period_seconds` is sent once, in the body.** `_undeploy_link` passes it
    BOTH as a `V1DeleteOptions` body and as a query parameter
    (`KubernetesLink.py:190-191`, k8s-backend.md G21); client-go carries it in the
    body only. The API server reads the body, so the request is the same modulo a
    redundant parameter.
86. **The container-port name is fifteen random hex characters, and can still be
    rejected.** `str(uuid.uuid4()).replace('-', '')[0:15]`
    (`KubernetesMachine.py:419`) produces fifteen characters from `[0-9a-f]`, and a
    Kubernetes container-port name must be an IANA_SVC_NAME — at most fifteen
    characters, lower-case alphanumeric and `-`, and at least one NON-DIGIT. Roughly
    one name in 1200 comes out all digits and the API server rejects the pod.
    `randomPortName` reproduces the alphabet and therefore the odds; fixing it would
    change the name of every port on every device, which PORT_SPEC §0.4 freezes.
    The generator is a field so a golden can pin it.
87. **The shutdown wait has a 180 s watchdog, where Python's `join()` can hang
    forever.** `undeploy` starts `_wait_machines_shutdown` on a thread and
    `wait_thread.join()`s it after the delete fan-out
    (`KubernetesMachine.py:599-609`), so `lclean` blocks until every watched
    device has produced a DELETED event — which is also what makes
    `machine_undeployed` and `machines_undeploy_ended` observable. The join is
    reproduced. What is added is the timer: unlike `_wait_machines_startup` this
    path has NO `threading.Timer`, so a DELETED event that never arrives — a
    device selected for undeploy that was not running, a watch the API server
    dropped — hangs the caller forever. CONCURRENCY.tsv row
    `KubernetesMachine.py:599` rules the port "add a sane timeout (deviation from
    Python's infinite hang, document it)"; it is `MAX_TIME_ERROR`, the same 180 s
    idle interval the startup watchdog uses, reset on every pod event. When it
    fires the wait simply ends: there is no Python message to preserve and no
    Python error to report, so `undeploy` answers whatever the deletions answered
    and only `machines_undeploy_ended` is missing — the progress bar stays open,
    exactly as it does when Python's own termination test never fires.
    On a fan-out failure Python skips the join entirely and leaks the thread; the
    watcher is stopped and joined instead, for the reason entry 72 gives about the
    deploy path. `TestUndeployDispatchesEventsAndWaits`, `TestUndeployWatchdog`
    and `TestUndeployLeavesNoWatcherBehind` pin the three halves.
88. **Every escaping Kubernetes API error carries the `KubernetesAPI` code, not
    only the three `raise e` sites.** ERROR_CODES.md §1.3 assigns code
    `KubernetesAPI` and human label `ApiException` to "a Kubernetes API error not
    matched by any translation rule", naming `KubernetesMachine.py:370,837` and
    `KubernetesManager.py:145` in parentheses. Those three are where Python
    *re-raises* one; they are not where a user *sees* one. `kathara.py:104` prints
    `({type(e).__name__}) {e}` for every uncaught exception, so an `ApiException`
    from a call nobody wrapped in a `try` — `list_namespaced_pod` behind `linfo`
    or `exec`, `delete_namespaced_deployment` inside the undeploy fan-out,
    `create_namespaced_custom_object` behind `kathara.deploy_link`,
    `list_namespace`, `delete_namespace` in `wipe`, `VersionApi().get_code()`, the
    two `w.stream` opens — prints the identical `(ApiException) (404) Reason: Not
    Found…` line. Go cannot recover that at the CLI: `isolation_test.go` forbids
    any `k8s.io/…` import outside this package, so an untranslated client-go error
    reaching the boundary would fall into the `InternalError` fallback of §1.4 and
    lose the label. `translateAPI` therefore applies the passthrough at every call
    whose error Python lets escape uncaught. It is a no-op on anything that is not
    an `ApiException` (a dial failure, a cancelled context — which Python's
    `except ApiException` would not catch either), it keeps the cause reachable so
    `isConflict`/`isForbidden` and the outer `except` blocks of `create` and
    `deploy_lab` still fire, and it is idempotent. The calls Python SWALLOWS
    (`KubernetesConfigMap.py:52`, `KubernetesLink.py:193`,
    `KubernetesMachine.py:687`, `KubernetesNamespace.py:37,52`,
    `KubernetesSecret.py:65`) are untouched. **Action for the contract owner:**
    ERROR_CODES.md §1.3's parenthesised site list reads as exhaustive and is not;
    `backend/docker` does not yet apply the sibling `DockerAPI` code at all, and
    the two backends should be brought into line before the CLI freezes error
    rendering.
89. **A `mem` unit Kubernetes has no suffix for is refused one API call earlier.**
    `Machine.get_mem` accepts the units b/k/m/g (`model/Machine.py:499`), so
    `mem=100k` and `mem=5b` are legal in `lab.conf` and arrive at
    `_build_definition` as `100k`/`5b`. Python uppercases (`memory.upper()`,
    `KubernetesMachine.py:437`) and SUBMITS `100K`/`5B`; the quantity grammar has
    a lower-case `k` and no `B` at all, so the API server rejects the Deployment
    and `create`'s `except ApiException` re-raises it as the `(ApiException)` line
    of entry 88. client-go parses the quantity locally, so `resource.ParseQuantity`
    fails while the object is still being built. The user-visible code is the same
    — the parse failure is wrapped in `kerrors.NewKubernetesAPI` — and the message
    text is client-go's ("unable to parse quantity's suffix") rather than the API
    server's. What differs in a request trace is one call: the ConfigMap is created
    on both paths, and Python additionally issues the `create_namespaced_deployment`
    that fails. `TestBuildDefinitionRejectsUnsupportedMemoryUnit` pins both the
    refusal and the neighbouring `m`/`g` that still deploy. Entry 70 covers only
    the canonicalisation of quantities that DO parse. The `cpus` limit cannot reach
    this: it is always `"%dm"`.

## From `cmd/kathara` and `internal/cliout` (port divergences and reproduced Python bugs)

90. **`rich` is reimplemented rather than replaced by `lipgloss`.**
    `PACKAGE_GRAPH.md` §3's dependency table names
    `github.com/charmbracelet/lipgloss` v1.1.0 for `internal/cliout`'s
    "tables/panels/progress". It is not used. The `human` renderer is under a
    byte-parity obligation (`PORT_SPEC` §9 Layer A captures Python's stdout and
    diffs it), and lipgloss wraps text with `muesli/reflow`, whose fold points
    differ from `rich/_wrap.py:divide_line`'s on the very first golden that
    exercises them: `07-static-routing`'s `LAB_AUTHOR` folds after
    `F. Ricci, ` and `Text.rstrip_end` crops exactly the one space that
    overflowed the fold width, leaving a row that ends in a comma. reflow
    strips the whole trailing run. `internal/cliout/text.go` is therefore a
    line-cited port of `_wrap.py` + `Text.wrap` + `Lines.justify`, pinned by
    `TestLabMetadataPanelMatchesGoldens` and five sibling tests against the
    recorded goldens. No third-party rendering dependency is added.
    **Action for the contract owner:** drop the lipgloss row from
    `PACKAGE_GRAPH.md` §3, or say which goldens may be re-recorded.

91. **A log record is emitted unfolded, with a nine-space gutter on the
    continuation rows.** `RichHandler` renders a record as an eight-column
    level name, a space, and the message word-folded into the remaining width,
    with the continuations indented nine columns and every row padded to the
    console width. `NORMALIZATION.md` §6.3 undoes exactly that fold before a
    golden is stored, because the fold column depends on host paths inside the
    message. `Console.Log` therefore emits one physical row per *logical* line
    and never folds — the normalized form is identical, and it cannot drift
    with the console width. What is NOT dropped is the gutter: the harness
    recognises a continuation row by the exact `^ {9}\S` prefix, so a message
    that carries an embedded newline (`LabParser`'s syntax error interpolates
    the raw line, terminator included — `test/goldens/err-malformed-labconf`)
    is emitted as `CRITICAL ` + first row, then nine spaces + the rest.
    Verified against the recording end to end.

92. **Progress bars are not rendered to a non-terminal, and always carry a bar
    glyph when they are.** `rich.live.Live` suppresses every intermediate
    refresh on a file or a dumb terminal and prints the final state once at
    stop, which is what put `[Deploying devices] ━━━ 3/3` into the pre-rule
    recordings; `NORMALIZATION.md` §6.6 now drops any line containing
    `━ ╸ ╹ ╺ ╻` or a braille spinner frame. `cliout.ProgressBar` reproduces the
    suppression and guarantees at least one `━` in every line it does emit, so
    a bar can never survive normalization and diff against a golden that has
    none. The intermediate frames on a real terminal are redrawn with `\r`
    rather than through a `Live` region; the observable difference is that a
    resize mid-deploy does not reflow the bar.

93. **`kathara list --watch` redraws in place instead of taking over the
    screen.** Python opens a `rich.live.Live(screen=True)` alternate-screen
    session and loops (`ListCommand.py:78-85`). The port clears and reprints.
    No golden covers watch mode (it never terminates), an alternate screen
    destroys the scrollback the user was reading, and `screen=True` on a pipe
    emits control sequences into a file. The refresh period (1 s), the break
    condition (the stats stream ending) and the exit code (0, Ctrl-C exempt
    from the warning) are unchanged.

94. **`kathara check` prints `Go version is:` where Python prints
    `Python version is:`.** `JSON_CLI_CONTRACT.md` §3.7 names the JSON key
    `runtime_version` and asks human mode for "the analogous Go line"; the
    label is `Go version is:` followed by three tabs, which lands the value in
    the same column 32 as the other four rows.

95. **`osVersion` outside Linux is `GOOS-GOARCH`, not `platform.platform()`.**
    `CheckCommand.linux_platform_info` is `uname` and is reproduced exactly;
    the macOS and Windows arms call `platform.platform()`, which interpolates
    an OS release string (`macOS-14.5-arm64-arm-64bit`,
    `Windows-10-10.0.19045-SP0`) that Go reads only through a syscall neither
    `runtime` nor `x/sys` exposes portably. The value is a diagnostic line, is
    not compared by any golden, and is the `os_version` key of E7.

96. **argparse's prefix matching is not reproduced.** Python accepts `--dir`
    for `--directory` (oracle-probed), and `CLI_SURFACE.md` §0.6 explicitly
    leaves the choice to the port and records that no golden exercises it. The
    port requires full names: with abbreviation, every flag added in a later
    release can break a script that was unambiguous before it. Short flags,
    clusters, `--flag=value` and `-dvalue` all behave as argparse does.

97. **`kathara settings` is a bubbletea form, not a curses menu.**
    `PORT_SPEC` §0.2 #1 deletes the vendored `consolemenu` and §3.2 item 3 asks
    for a bubbletea form over the same keys; `cmd/kathara/settings_tui.go` is
    it. Every key the three Python handlers built an item for has a row
    (`TestSettingsFormCoversEveryMenuKey`), and every row writes through
    `settings.Settings.SetString`, so §3.2 item 4 ("validation lives in
    `settings/` and runs on both paths") holds by construction. The places
    where the form's *observable* behaviour differs from `cli/ui/setting/*.py`
    are enumerated one by one under "What the bubbletea settings form does
    differently from the consolemenu screen" in `PROPOSED-DIVERGENCES.md`. It
    also degrades honestly: with no terminal it answers `InvocationError`
    naming `kathara config`, where the curses menu would have failed inside
    ncurses. Python's `-h`-is-ignored quirk (`CLI_SURFACE.md` M-5) is
    reproduced by `commandSpec.NoParser`, which skips the whole
    parse-and-validate block for this one command: argv is never read, so `-h`
    prints nothing, `--bogus` is not an unknown flag, and §12's "no argparse
    exit-2 path exists" holds. `TestSettingsNeverParsesArgv` pins it.

98. **Python's `EOFError` at a confirmation prompt keeps its class name.**
    `rich.prompt` calls CPython's `input()`, which raises `EOFError` on a
    closed stdin; nothing catches it, so `kathara wipe` under the Layer A
    harness prints `CRITICAL (EOFError) EOF when reading a line` and exits 1.
    `ERROR_CODES.md` §1.2 buckets `EOFError` into `InternalError`, which would
    have made the human label read `InternalError`. `cliout` therefore consults
    an optional `HumanLabel() string` on the error before falling back to the
    registry, which is also what keeps `model.PyRuntimeError`'s
    `TypeError`/`AttributeError`/`KeyError`/`CreateFailed` labels — the last of
    which `test/goldens/err-nonexistent-dir` asserts. The JSON `code` is
    unaffected and stays `InternalError`.

99. **`exec` decodes with one U+FFFD per *maximal subpart*, which is what
    CPython does — the earlier "one per byte" reading of this entry was
    wrong.** `JSON_CLI_CONTRACT.md` §3.6 replaces Python's crashing per-chunk
    `chardet.detect` with "UTF-8 with invalid sequences replaced by U+FFFD",
    and the parity target for *how many* is `bytes.decode("utf-8", "replace")`.
    Oracle-measured on CPython 3.13: `b'\xe2\x82'` → one replacement,
    `b'\xe2\x82A'` → `'\ufffdA'`, `b'\xf0\x9f\x98'` → one, while
    `b'\xff\xff'` → two and `b'\xed\xa0\x80'` → three. That is the Unicode
    maximal-subpart rule, not one-per-byte and not the one-per-run
    `strings.ToValidUTF8` implements, so `cmd/kathara/exec.go` spells the
    decoder out (`utf8Step`) and `TestUTF8DecoderReplacesMaximalSubparts` pins
    it against sixteen oracle rows, each also fed one byte at a time. The
    decoder still holds back a trailing sequence that is a well-formed
    *prefix*, so a rune split across two chunks is one character and an
    unfinished one at end of stream is one replacement.

100. **`connect` forwards SIGWINCH on Unix; Python never resized at all.**
     `TerminalRunner` sizes the session once at attach. A shell that is never
     told the window grew wraps its prompt at the old width, which is one of
     the failures `PORT_SPEC` §3.3 is about. `resize_unix.go` watches SIGWINCH
     and calls `TTYSession.Resize`; `resize_windows.go` is a no-op, because the
     console API reports a resize through `ReadConsoleInput`, which cannot be
     read while the same handle is being drained as a byte stream — that needs
     the ConPTY input layer of the Phase 6 multiplexer.

101. **The terminal `flush` uses the escape sequence on Windows too.**
     `HandleMachineTerminal.flush` writes `\033[2J\033[0;0H` on Unix and shells
     out to `cls` through `os.system` on Windows. The escape sequence works on
     Windows 10 and later through virtual-terminal processing; spawning a
     command interpreter to clear a screen is not reproduced. The port also
     skips the clear entirely when stdout is not a terminal, where Python would
     have written the raw bytes into the redirected file.

102. **argparse's prefix matching aside, the sub-command help and every exit-2
     usage block are argparse's byte for byte — but they are rendered by a
     ported `HelpFormatter`, not by `pflag.FlagUsages`.** `CLI_SURFACE.md`'s
     conventions call "usage + error to stderr, exit 2" part of the observable
     contract, and pflag's own renderer disagrees with argparse on every axis:
     it sorts alphabetically, prints Go type names (`-d, --directory string`),
     appends `(default [])`, splits a two-spelling action into two rows, has no
     `positional arguments:` section, and — for the `nargs='*'` options — leaks
     `bindList`'s `NoOptDefVal` sentinel, two NUL bytes included, into the help
     text. `cmd/kathara/usage.go` therefore ports `HelpFormatter._format_usage`,
     `_get_actions_usage_parts`, `_format_action`, `_format_action_invocation`,
     `_format_args` and `_fill_text`, plus `textwrap`'s greedy wrap.
     `TestArgparseHelpFormatterMatchesOracle` diffs the result against
     `testdata/argparse_help/*.txt`, which are `parser.format_help()` run on the
     real Python command objects at COLUMNS=80, for all thirteen parsers — they
     match exactly. Two residual differences remain and are deliberate:
     (a) the port's own flags (`--format`, `--lab-hash`, `--lab-name`,
     `--from-archive`, `--name`) appear in the real commands' help, which is why
     the fidelity test drives replica parsers instead; (b) the *error message*
     for an unknown or malformed flag is pflag's (`unknown flag: --bogus`,
     `flag needs an argument: --directory`) where argparse says
     `unrecognized arguments: --bogus`, because that message comes out of the
     parser and not the formatter. Every message the port raises itself —
     `the following arguments are required: …`, `one of the arguments --add
     --rm is required`, `argument -a/--all: not allowed with argument
     -s/--settings`, `unrecognized arguments: a b` — is argparse's, including
     the order the four checks fire in.

103. **The layout the help is rendered at is fixed at 80 columns.** argparse
     asks `shutil.get_terminal_size()` and folds to `columns - 2`, so a wide
     terminal gets a wide help block. `parser.usage()` renders at 80, which is
     what `shutil` reports for a pipe and therefore what every non-terminal
     invocation — the goldens included — already saw. `parser.usageAt` takes
     the width, so wiring the console's is a one-line change if a human decides
     the reflow is wanted.

104. **SIGTERM is no longer folded into the Ctrl-C contract.** An earlier build
     passed `syscall.SIGTERM` to `signal.NotifyContext` alongside
     `os.Interrupt`, which gave a terminated process the warning, the
     `{"interrupted":true}` envelope and exit 0. `src/kathara.py` installs no
     SIGTERM handler at all — the default disposition kills the process, exit
     143, no output — and `JSON_CLI_CONTRACT.md` §6.2 pins the contract to
     SIGINT. `cmd/kathara/main.go` now watches `os.Interrupt` only. The
     interrupt is also read off `ctx.Err()` rather than off a flag set by a
     goroutine racing `finish`.

105. **`linfo` declares `--format`.** `JSON_CLI_CONTRACT.md` §1.1's `linfo` row
     reads "FeatureNotAvailable stub in 1.0 (§5.6): errors in every mode" with
     `—` in the json and jsonl columns, which can be read either as "the flag
     is a usage error, like `connect`/`settings`" or as "the flag is accepted
     and every value errors". The port takes the second reading, because §5.6
     registers `linfo` as a `feature` token of the JSON **error envelope**, and
     that envelope only exists in the machine formats: under the first reading
     the registration could never be reached by any invocation. So
     `kathara linfo --format json` answers
     `{"error":{"code":"FeatureNotAvailable","message":…,"feature":"linfo"}}`
     and exits 1, while `--format jsonl` stays a usage error (linfo does not
     stream). `TestLinfoErrorsInEveryMode` pins all three.
     **Action for the contract owner:** confirm the reading, or say the flag
     should be rejected and the §5.6 token is documentation-only.

## From the golden harness (SDK-forced divergence, not a Python bug)

106. **`CapAdd`/`CapDrop` reach the daemon in a different *spelling* and order
     than docker-py sends, and the difference is not reachable from port code.**
     The Go Docker SDK rewrites both lists client-side, unconditionally, inside
     `ContainerCreate`:

     ```go
     // github.com/docker/docker@v28.5.2/client/container_create.go:72
     hostConfig.CapAdd = normalizeCapabilities(hostConfig.CapAdd)
     hostConfig.CapDrop = normalizeCapabilities(hostConfig.CapDrop)
     ```

     `normalizeCapabilities` (`:139`) de-duplicates and `sort.Strings`-es;
     `normalizeCap` (`:159`) upper-cases and prepends `CAP_` unless the value
     already carries the prefix or is the magic constant
     `allCapabilities = "ALL"` (`:132`). There is no API-version gate and no
     opt-out short of forking the client or hand-rolling the `/containers/create`
     POST. docker-py does no such rewrite: it puts `MACHINE_CAPABILITIES` on the
     wire exactly as `DockerMachine.py` spells it — bare names, literal source
     order. The daemon stores whichever form it received, so `docker inspect`
     reports:

     | | `HostConfig.CapAdd` |
     |---|---|
     | Python (docker-py) | `["NET_ADMIN","NET_RAW","NET_BROADCAST","NET_BIND_SERVICE","SYS_ADMIN"]` |
     | Go (docker SDK) | `["CAP_NET_ADMIN","CAP_NET_BIND_SERVICE","CAP_NET_BROADCAST","CAP_NET_RAW","CAP_SYS_ADMIN"]` |

     **Semantics are identical.** The daemon resolves `NET_ADMIN` and
     `CAP_NET_ADMIN` to the same kernel capability and the bounding set is a
     set, not a sequence, so the resulting container's `CapBnd`/`CapEff` masks
     are bit-for-bit equal under either spelling. Nothing observable inside the
     container, and nothing about what a device may do, changes. What differs is
     only the daemon's echo of the request it was handed.

     **Ruling: canonicalize in the golden harness.** `NormalizeCapabilities`
     (`tools/goldenharness/normalize.go`, mirroring the SDK function including
     the `ALL` special case) is applied to both lists on both sides of the
     comparison, so the golden asserts the capability **set** in canonical form.
     The 47 stored goldens were migrated mechanically to that form — a pure
     transform of the recorded arrays, not a re-recording — and re-verified
     against the Python oracle at 47/47. Membership and cardinality stay
     asserted: a missing `NET_ADMIN`, a stray `SYS_PTRACE`, or a non-empty
     `cap_add` on the `privileged` path (Kathara passes `cap_add=None` there)
     still fails. Only letter-case, the `CAP_` prefix and list order are
     conceded. Rationale: a byte-exact assertion here would be an assertion
     about the client library, not about the port, and could only be satisfied
     by bypassing the SDK — real cost, zero semantic gain. Recorded in
     `tools/goldenharness/NORMALIZATION.md` section 10; the ORDERING.tsv row
     that previously listed `cap_add` as order-asserted (section 5.1) is
     withdrawn there for the same reason.
## From Layer D gate runs (2026-08-12)

11. **`connect_tty` via the subprocess client prints CLI chrome** ("Waiting startup commands execution. Press [ENTER] to override...") that 3.8.3's pure API path never printed — inherent to the §7 architecture (client shells out to `kathara connect`). Cosmetic.
12. **Accidental root-logger installation not replicated.** 3.8.3's `decorators.py:13` calls root `logging.debug(...)` during `Kathara.get_instance()`, implicitly firing `logging.basicConfig()`; user scripts' later `logger.info` narration thereby reaches stderr. The Go-backed client has no such side effect, so tutorial narration is invisible unless the user configures logging. Recorded per §10 (accidental side effect, not replicated by patch); tutorials still pass gate 2.

## From the terminal rebuild, `term/` (sanctioned §0.2 #2 divergences, not Python bugs)

Phase 6 §3.3 items 1 and 3. Item 2 (tmux) is entry 18 above. These are the
divergences the rebuild *is*: PORT_SPEC §0.2 #2 sanctions them, so they belong
here and not in `PROPOSED-DIVERGENCES.md`. Rows 113–119 are the table
`docs/port/SPIKES/windows-terminal.md` §9 held for this moment (work item
W6-10); they are the OQ-18/19/20 Windows quirks the rebuild fixes rather than
reproduces.

107. **A `MULTIPLEXER` value is reserved in the `terminal` setting.** Python
     reserves exactly one non-program value in that key, `"TMUX"`
     (`setting/Setting.py:288`), which `check_terminal` short-circuits on. The
     rebuild reserves a second, `"MULTIPLEXER"`, selecting the built-in
     multiplexer, and short-circuits the check on it for the same reason — there
     is no file on disk to stat. The **empty** value, which is Python's own
     Windows default, selects it too. Schema, key order, file path and every
     other value's meaning are unchanged (`term/mode.go`, `settings/validate.go`;
     `settings.TerminalMultiplexer` and `term.TerminalMultiplexer` pinned equal
     by `TestTerminalTokensMatchTerm`). **User-visible on Windows:** the stock
     configuration now opens the multiplexer where 3.8.3 spawned a PowerShell
     console per device. On Unix the stock default is still `/usr/bin/xterm` /
     `Terminal` and nothing changes until the user opts in — see
     PROPOSED-DIVERGENCES.md for the ruling that would flip that too.

108. **`num_terms > 1` opens one tab, not N windows.** `HandleMachineTerminal.run`
     calls `open_machine_terminal` `get_num_terms()` times and Python opens that
     many emulator windows onto one device. Under the multiplexer the device is
     enqueued once (`app.enqueueMuxDevice` dedupes by name) and gets one tab.
     This is the collapse the tmux backend already had in 3.8.3, where the
     second `add_window` finds the window the first one made
     (`docs/port/SPIKES/tmux.md` §6). The external adapters keep Python's
     behaviour exactly: N invocations, N windows. Pinned by
     `TestMuxDevicesAreDeduplicated`.

109. **The multiplexer window opens after the command, not during the deploy.**
     Python opens each device's window from the `machine_deployed` event, so
     windows appear one at a time as the scenario comes up. One window for the
     whole scenario cannot: the multiplexer opens once, from `runCommand`, after
     the command body has emitted its result. Ordering against the deploy's own
     output is therefore different under `MULTIPLEXER` — the panel, the progress
     bars and the `--list` table are all complete before the window appears.
     tmux and the external adapters keep Python's per-device timing.

110. **`kathara connect` renders through the multiplexer when that mode is
     selected.** §3.3 item 4 marks `connect` "unchanged behaviour" and item 1
     asks for "attach via kathara connect"; both hold, because the transport is
     the same `ConnectTTY` call either way and only the renderer differs. The
     raw byte pump is still what runs for `TMUX`, for every external emulator,
     and whenever stdin or stdout is not a terminal — which is every scripted
     and golden-harness invocation. **User-visible:** under `MULTIPLEXER`,
     `connect` gains scrollback, copy and a detach key, and the remote shell's
     exit status is still not propagated (exit 0, CLI_SURFACE.md §9).

     Everything else about the command is held identical to the raw path, and
     deliberately so: the attach runs **before** the multiplexer starts, so a
     device that is not running still exits 1 with the error on the console and
     no window ever opens; `startup_waited == 2` still exits 0 silently; and the
     window closes when the shell does, because the multiplexer quits once its
     only session has ended cleanly (`term.Run`). The one residue is a session
     that ends with a *transport* error mid-attach: the raw path exits 1 with
     it, the multiplexer renders it in the pane, keeps the window up so it can
     be read, and exits 0 when the user detaches. Erroring out from under a
     full-screen program the user is still looking at is the worse of the two,
     and `connect`'s exit code is unobservable to the scripted callers, which
     take the raw path in any case.

111. **Pane rendering is a bounded terminal emulation, not a passthrough.**
     Python delegated rendering to xterm / Terminal.app / conhost. `term/screen.go`
     is what replaces them, and its limits are stated in its own file header:
     no terminal replies (DSR, DA), combining marks dropped, no reflow on
     resize, no character-set designation. A device program that blocks on a
     cursor-position report will not get one. This is a rendering-fidelity limit
     of the rebuild, not a behaviour change against Python, which had no
     renderer of its own.

112. **The startup log reaches a pane as CRLF.** `connect_tty`'s `-l` block is
     written with bare newlines to a cooked stdout in Python. A pane is a raw
     screen, where LF indexes without returning, so the CLI converts the block
     before replaying it as the pane's first bytes (`term.PrefixSession`,
     `cmd/kathara`'s `crlf`). Same text, no staircase.

113. **`WriteConsoleW`'s trailing NUL is gone (OQ-19).** 3.8.3's Windows console
     adapter wrote `len(buffer)` including the terminating NUL, putting one
     U+0000 in the stream per output chunk. The rebuild writes raw bytes.

114. **An initial resize is emitted on every platform (OQ-18/19).** The Unix
     adapter emitted the terminal size before any I/O; the Windows one never
     did, so a remote TTY kept the wrong geometry until the user resized the
     window. The multiplexer resizes every pane — foreground and background —
     from the first `WindowSizeMsg`, and the connect runner emits the size
     before the pumps start. Pinned by `TestMuxResizePropagatesToEveryPane` and
     `TestMuxResizeUsesColumnsFirst`.

115. **No U+FFFD at chunk boundaries (OQ-20).** Python decoded each 4096-byte
     read as UTF-8 with `errors="replace"`, so a multi-byte rune split across
     two reads became a replacement character. `Screen.Write` carries the
     partial rune to the next write. Pinned by the "UTF-8 split across two
     writes" row of `TestScreenPrintingAndControls`.

116. **Function keys go out as modern xterm sequences.** Python translated
     virtual keys through a hand-rolled `KEYCODES` table that sent F1–F4 as the
     legacy `ESC[11~`…`ESC[14~`. The rebuild sends SS3 (`ESC OP`…`ESC OS`),
     which is what `TERM=xterm` terminfo — the devices' own — describes. A
     raw-mode reader inside a device could observe the difference; readline and
     vim accept both. Pinned by `TestEncodeKey`.

117. **A console that cannot be put in raw mode is an error, not a hang.**
     `enter_raw`'s `GetConsoleMode` failure returned silently in Python, leaving
     a session that read nothing forever. The multiplexer refuses a non-terminal
     outright on both of its entry points — `connect` falls back to the raw pump
     path (`connect.go`), and the deploy path skips the window with a debug line
     rather than writing alternate-screen frames into a pipe
     (`app.runPendingTerminals`, pinned by
     `TestRunPendingTerminalsSkipsANonTerminal`). `kathara lstart | tee log`
     under `MULTIPLEXER` therefore deploys and prints exactly as it always did,
     which is what Python's separate OS windows gave it for free.

118. **End of stream is `io.EOF`, uniformly.** Python's npipe path treated an
     empty read as "no data yet" and signalled EOF by exception, the opposite of
     its own Unix fd path. Both legs now end with `io.EOF`
     (`term/pty_unix.go` normalizes Linux's `EIO`; the ConPTY leg ends on
     `ERROR_BROKEN_PIPE`).

119. **Raw-mode restore no longer depends on session close returning.** Python's
     cleanup was single-threaded with the restore last, so a hung session close
     left the user's shell raw. bubbletea restores the terminal on its own exit
     path, `term.Run` closes every session in a deferred `shutdown`, and the
     connect path's restore is a top-frame `defer`. PORT_SPEC §12 risk 6.

     **Where the §12 risk-6 test stands.** The Unix leg is executed:
     `TestConnectRestoresTheTerminalOnEveryExit` runs `attachTTY` on a real
     pseudo-terminal and compares the termios the kernel holds before and after,
     over all four ways out — session EOF, a stream error, a cancelled context
     and a panic unwinding through the frame — with
     `TestConnectRestoreIsNotVacuous` guarding the comparison itself. macOS runs
     the same test in CI (the file builds for `linux || darwin`). **Windows has
     no equivalent and is a documented manual-verification gap**: its console
     modes are restored by bubbletea on the multiplexer leg and by
     `golang.org/x/term` on the connect leg, neither of which this repository
     exercises on a real console. See item 120.

120. **The multiplexer uses backend transports on every platform; the ConPTY
     layer has no production caller.** `docs/port/SPIKES/windows-terminal.md` §1
     (work item W6-8) sketched a Windows pane as a local `kathara connect` child
     under a ConPTY. The shipped design attaches every pane through the backend
     `TTYSession` instead, on all three platforms: one code path rather than two,
     the same transport `kathara connect` uses (item 110), and console-mode
     handling left to bubbletea, which sets the Windows VT modes itself.
     `term/conpty_windows.go` and `term.StartPtySession` stay as the tested seam
     for a future embedded local-child pane — `StartPtySession` is what the
     real-pty integration tests drive on Unix — but nothing in `cmd/kathara`
     reaches ConPTY today.

     Three Phase-6 work items fall out of that and are **not** done, recorded
     here rather than silently dropped:

     - **W6-9 (Windows CI mirror).** There is no `conpty_windows_test.go`, so
       the ConPTY implementation is design-from-documentation, exactly as the
       spike's §11 warned it would be until a Windows runner executed it. CI
       does run `go test ./...` on `windows-latest`, so the multiplexer's model
       tests, the key encoder and the screen are covered there; the
       pseudoconsole itself is not.
     - **W6-9's F-key check** against a real device shell (spike §9 row 4) is
       likewise unexecuted; `TestEncodeKey` pins the byte sequences, not a
       device's reaction to them.
     - **W6-14 (Windows 10 1809 / build 17763 floor).** No version check is
       enforced. With no production caller for `CreatePseudoConsole`, a check
       would guard nothing a user can reach; it becomes required the moment a
       pane hosts a local child on Windows.

     One Windows-only behaviour difference in the external adapter belongs with
     them: `subprocess.Popen(..., creationflags=CREATE_NEW_CONSOLE)` leaves the
     child's standard handles to its new console, while `os/exec` always passes
     handles and gives a child with nil `Stdin/Stdout/Stderr` the NUL device.
     The Unix adapter now inherits this process's descriptors, which is what
     Popen does there (`term/external_unix.go`); the Windows arm is left alone
     because both alternatives — NUL handles or this console's handles — are
     wrong in different ways and neither can be verified from this repository.
