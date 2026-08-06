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
