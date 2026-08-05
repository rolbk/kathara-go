# JSON CLI Contract — Version 1

**Status:** FROZEN (Phase 0 gate artifact). Later phases implement against this document
without relitigating. Changes require a human owner decision and a version bump per §9.

**Scope:** the machine-readable output modes of the Go `kathara` binary (spec §5), which are
the wire protocol of the Python client package (spec §7). Human-mode output is governed by
byte-golden parity with Python v3.8.3 (spec §9 Layer A) and is touched here only where the
two modes interact.

**Normative references:** `PORT_SPEC.md` §5, §0.3, §4.3; `docs/port/ERROR_CODES.md`
(stable code table); `analysis/CLI_SURFACE.md` (human-mode surface);
`analysis/SYNTHESIS.md` OQ-7 rulings (accepted; incorporated below).

Ground truth for every Python-behavior claim: `kathara-python/src` at v3.8.3. File/line
citations below are into that tree.

---

## 1. The `--format` flag

### 1.1 Values and registration

Every non-interactive command accepts `--format {human,json,jsonl}`, default `human`:

| Command | `human` | `json` | `jsonl` |
|---|---|---|---|
| `lstart`, `lclean`, `lrestart`, `wipe`, `list`, `check`, `vstart`, `vclean`, `lconfig`, `vconfig`, `config` | yes | yes | no |
| `exec` | yes | yes | yes |
| `connect`, `settings` | yes (only) | no | no |
| `linfo` | FeatureNotAvailable stub in 1.0 (§5.6): errors in every mode | — | — |

- `--format` is a per-command flag (the top-level dispatcher parses only the command word,
  matching Python `kathara.py`, which parses only `sys.argv[1:2]`).
- Passing `--format json`/`jsonl` to a command that does not support that value is a usage
  error: usage text on stderr, exit **2**, nothing on stdout. Same for `--format jsonl` on a
  non-streaming command, and for any `--format` on `connect`/`settings`.
- `-w/--watch` (`list`; and `linfo` when it returns post-1.0) is incompatible with
  `json`/`jsonl`: usage error, exit **2**. Streaming inventory is reserved for the post-1.0
  stats envelope (spec §5.2).
- Top-level dispatch failures happen **before** any per-command flag parsing and are
  therefore always human-formatted regardless of a `--format` token in argv: no command /
  non-lowercase command → help + exit 1; unknown command → ``Unrecognized command `X`.`` +
  help + exit 1; `kathara -v` → `Current version: <ver>` on stdout + exit 0
  (`kathara.py:60-67,82-84`). Scripted clients must invoke known lowercase commands.

### 1.2 Mode semantics

- **`human`** — Python v3.8.3 output, unchanged, byte-golden. This includes Python's stream
  assignment: logging, panels, tables, progress bars and prompts all go to **stdout**; the
  only stderr writers are `exec`'s remote-stderr passthrough and argparse-class usage errors
  (`CLI_SURFACE.md` §0.4). **Pinned resolution of a spec-internal conflict:** spec §5.1 says
  both "human: current output, unchanged" and "logs … go to stderr in all modes"; these
  cannot both hold. Parity wins — human mode keeps Python's stdout assignment, because the
  Layer A byte-goldens are captured from Python stdout and are a merge gate. The
  "stderr in all modes" sentence applies to `json`/`jsonl` only.
- **`json`** — exactly one JSON object on stdout (§1.4), then process exit. Nothing else is
  ever written to stdout: no ANSI, no BOM, no logs, no prompts, no progress.
- **`jsonl`** — newline-delimited JSON event objects on stdout (§4). Same exclusivity rule.

### 1.3 Stream discipline in `json`/`jsonl`

- **stdout** carries protocol only: the single result/error/interrupt object (`json`) or
  the event stream (`jsonl`).
- **stderr** carries everything else: log lines (plain text, no rich markup, no ANSI color),
  warnings, and diagnostic notices. Progress bars, spinners, live screens and panels are
  **not rendered at all** in these modes (they are UI, not logs).
- **stdin** is never read for interaction in `json`/`jsonl` (§1.5). It is read only as a
  data channel when `--from-archive -` is given (§7).
- Encoding: stdout is UTF-8. JSON is emitted compact (no insignificant whitespace), with
  HTML escaping disabled (`<`, `>`, `&` appear literally), each object followed by exactly
  one `\n`. Object key emission order is pinned per envelope in §3–§5 (byte-goldens depend
  on it); additive future fields append at the end (§9).

### 1.4 One-object rule for `json`

A `json`-mode invocation writes exactly one of the following to stdout, then exits:

1. the command's **result envelope** (§3) — exit 0, except `check` (§3.7) and `exec` (§3.6);
2. the **error envelope** `{"error":{...}}` (§5) — exit 1;
3. the **interrupt envelope** `{"interrupted":true}` (§6.2) — exit 0.

`lrestart` emits a single combined object at the end of both phases (§3.3); no intermediate
object is emitted when the clean phase completes.

### 1.5 Interactive prompts in `json`/`jsonl` (pinned)

Python has three prompt sites plus one interactive wait. Human-mode behavior is unchanged;
in `json`/`jsonl` the CLI must never block on a prompt. Pinned per site:

| Site (Python source) | Human mode | `json`/`jsonl` mode |
|---|---|---|
| `wipe` confirmation `Are you sure to wipe Kathara?` without `-f` (`WipeCommand.py:59-60`; decline → `sys.exit()` → exit 0) | unchanged | **never prompts.** Without `-f/--force`: error envelope `{"error":{"code":"ConfirmationRequired","message":"Confirmation required: re-run with `--force` to wipe Kathara."}}`, exit 1, nothing wiped. With `-f`: proceeds. |
| Image-update prompt when `image_update_policy == "Prompt"` (`UpdateDockerImage.py`) | unchanged | **auto-answers "no"**: the local image is used, no pull. A notice is logged to stderr. (`Always`/`Never` policies behave as configured, both modes.) |
| Volume-mount prompt when `volume_mount_policy == "Prompt"` (`MountDevicesVolumes.py`) | unchanged | **auto-answers "yes"**: volumes are mounted as the scenario declares. A notice is logged to stderr. (`Always`/`Never` unchanged.) |
| `exec --wait` "Press [ENTER] to override..." (`HandleMachineTerminal.print_wait_msg`, `DockerMachine._wait_startup_execution`) | unchanged | no keyboard override; the CLI simply waits for `/tmp/EOS`. Interrupt via signal only. |

Rationale (pinned): auto-answers follow the declared intent of the scenario/settings and
keep deploys unattended; destruction (`wipe`) is the one action that must be explicit, and
`--force` already exists as the explicit form, so JSON mode requires it instead of guessing.
`ConfirmationRequired` is a JSON-mode-only stable code contributed to `ERROR_CODES.md` by
this contract; it can never be raised in human mode.

---

## 2. Envelope catalog

The complete, closed list of envelope types in contract v1:

**`json` mode (one object per invocation):**

| # | Envelope | Emitted by | Exit |
|---|---|---|---|
| E1 | `lstart` result | `lstart` (incl. `--from-archive`, dry mode) | 0 |
| E2 | `lclean` result | `lclean` | 0 |
| E3 | `lrestart` result (composite of E2+E1) | `lrestart` | 0 |
| E4 | `wipe` result | `wipe` | 0 |
| E5 | `list` result | `list` | 0 |
| E6 | `exec` aggregate result | `exec` | remote exit code |
| E7 | `check` report | `check` | 0 ok / 1 failed test |
| E8 | `vstart` result | `vstart` (incl. dry mode) | 0 |
| E9 | `vclean` result | `vclean` | 0 |
| E10 | `lconfig` result | `lconfig` | 0 |
| E11 | `vconfig` result | `vconfig` | 0 |
| E12 | `config` results (`get`/`set`/`list`/`reset` shapes) | `config` | 0 |
| E13 | error envelope `{"error":{...}}` | any command | 1 |
| E14 | interrupt envelope `{"interrupted":true}` | any command on SIGINT | 0 |

**`jsonl` mode (event objects, `exec` only in 1.0):**

| # | Event | Shape |
|---|---|---|
| S1 | stdout chunk | `{"type":"stdout","data":"..."}` |
| S2 | stderr chunk | `{"type":"stderr","data":"..."}` |
| S3 | exit | `{"type":"exit","code":N}` |
| S4 | error | `{"type":"error","error":{...}}` (same inner object as E13) |
| S5 | interrupted | `{"type":"interrupted"}` |

**Shared sub-objects:** the `lab` object (§3.0.1), the machine inventory object (§3.0.2),
the `error` object (§5).

---

## 3. Per-command `json` schemas

### 3.0 Shared sub-objects

#### 3.0.1 The `lab` object

Key order pinned: `name`, `hash`, `path`.

```json
{"name":null,"hash":"FwFaxbiuhvSWb2KpN5zw","path":"/home/user/labs/default_scenario"}
```

- `name` (string|null): the scenario name. Null for path-parsed labs without `LAB_NAME=` in
  `lab.conf` (Python `Lab(None, path=...)`); `"kathara_vlab"` for the v-command family;
  the `--name` value for archive deploys; the `LAB_NAME` value when `lab.conf` sets it
  (`LabParser.py:82-87` assigns it via the name setter).
- `hash` (string): `generate_urlsafe_hash(name)` when `name` is non-null, else
  `generate_urlsafe_hash(path)` — identical to Python `Lab.__init__`/name-setter semantics
  (`model/Lab.py:76,88-90`; `utils.py:53-57`). Golden constants:
  `"Default scenario"` → `9pe3y6IDMwx4PfOPu5mbNg`, `"default_scenario"` →
  `FwFaxbiuhvSWb2KpN5zw`.
- `path` (string|null): the absolute (realpath-resolved) scenario directory, after Python's
  quote-stripping of `-d` values. Null for `kathara_vlab` and for archive deploys (the
  extraction directory is private and ephemeral).

Where the parsed lab carries metadata, four additive nullable keys follow `path` in this
order: `description`, `version`, `author`, `email`, `web` (from `LAB_DESCRIPTION`,
`LAB_VERSION`, `LAB_AUTHOR`, `LAB_EMAIL`, `LAB_WEB`; `model/Lab.py:18`). They are emitted
only by `lstart`/`lrestart` (the commands that parse `lab.conf` fully) and only when at
least one is non-null.

#### 3.0.2 The machine inventory object

The spec §0.3 partial-exception fields, exactly six canonical keys. Key order pinned to the
Python `to_dict()` source order (`DockerMachineStats.py:121-127`, restricted to inventory
fields): `network_scenario_id`, `name`, `container_name`, `user`, `status`, `image`.

```json
{"network_scenario_id":"9pe3y6IDMwx4PfOPu5mbNg","name":"pc1","container_name":"kathara_user_pc1_9pe3y6IDMwx4PfOPu5mbNg","user":"user-abcdefgh","status":"running","image":"kathara/base"}
```

- Docker backend: values from container labels/attributes exactly as Python reads them
  (`lab_hash` label, `name` label, container name, `user` label, container status,
  `image.tags[0]`). `user` and `status` are nullable (they are `Optional` in Python).
- Kubernetes backend (pinned mapping — Python's `KubernetesMachineStats.to_dict()` has
  `pod_name` and `assigned_node` and **no** `user`): `container_name` carries the pod name,
  `user` is null, and one additive backend key `assigned_node` (string|null) follows
  `image`. The six canonical keys are always present on both backends.
- Resource-sampling fields (`pids`, `cpu_usage`, `mem_usage`, `mem_percent`, `net_usage`,
  `interfaces`) are **absent in 1.0** (deferred, spec §0.3). When they land post-1.0 they
  append after the canonical keys (additive).

### 3.1 `lstart`

Key order: `lab`, `dry_run`, then either `checks` (dry mode) or `machines`, `links`, and
optionally `machine_stats`.

Normal deploy (exit 0):

```json
{"lab":{"name":null,"hash":"FwFaxbiuhvSWb2KpN5zw","path":"/labs/default_scenario"},"dry_run":false,"machines":["r1","pc1","pc2"],"links":["A","B"]}
```

- `machines` (array of string): the devices actually deployed (after positional selection
  and `--exclude` filtering), in **deploy schedule order** — `lab.conf` insertion order as
  reordered by `lab.dep` dependencies. This order is semantic in Python and deterministic
  in Go; it is emitted as scheduled, not as completed.
- `links` (array of string): collision domains deployed, **canonically sorted**
  (lexicographic byte order). Python's link deploy order is pool-completion-dependent;
  per the accepted nondeterministic-order ruling, Go emits canonical sorted order.
  `kathara_host_bridge` is included when it was deployed (bridged devices exist).
- With `-l/--list`: one additive key `machine_stats` (array of inventory objects §3.0.2,
  sorted by `name`) appended after `links`.

Dry mode (`--print`/`--dry-mode`, exit 0):

```json
{"lab":{"name":null,"hash":"FwFaxbiuhvSWb2KpN5zw","path":"/labs/default_scenario"},"dry_run":true,"checks":[{"file":"lab.conf","ok":true},{"file":"lab.dep","ok":true}]}
```

- `checks`: one entry per file Python prints a `✓` line for (`LstartCommand.py` dry path):
  always `lab.conf`; `lab.dep` if dependencies were parsed; `lab.ext` never appears in 1.0
  (its presence errors first, see below). Order: `lab.conf`, `lab.dep`. `ok` is always
  `true` in v1 — a failed check raises and produces the error envelope instead.
- Order of operations is Python's (`LstartCommand.run`): a `lab.ext` file in the scenario
  errors (in 1.0: `FeatureNotAvailable`, feature `lab.ext`) **before** the dry-mode exit,
  and privileged/root checks likewise precede deployment. Errors → E13, exit 1.

### 3.2 `lclean`

Key order: `lab`, `machines`, `links`.

```json
{"lab":{"name":null,"hash":"FwFaxbiuhvSWb2KpN5zw","path":"/labs/default_scenario"},"machines":["pc1","r1"],"links":["A","B"]}
```

- `machines`/`links`: names actually undeployed, **canonically sorted** (undeploy order in
  Python is API-listing/pool order — nondeterministic → sorted ruling applies). Empty
  arrays when nothing was running.
- The `lab` object reflects Python's parse-or-fallback: if `lab.conf` fails to parse, the
  lab is `Lab(None, path=...)` (hash from path, `name` null) — never an error
  (`LcleanCommand.py`, catches `(Exception, IOError)`).
- Scenario addressing: `--lab-hash`/`--lab-name` (§8) may replace `-d`; then `path` is null
  and `name`/`hash` reflect the given identifier.

### 3.3 `lrestart`

One object, key order: `clean`, `start`. Values are exactly the E2 and E1 envelopes:

```json
{"clean":{"lab":{...},"machines":[...],"links":[...]},"start":{"lab":{...},"dry_run":false,"machines":[...],"links":[...]}}
```

- Emitted only after both phases complete. If the start phase fails, the error envelope
  (E13) is emitted instead and the clean phase's work is **not** reported (faithful to
  Python, where lclean's output has already scrolled by but no summary exists).
- The Python lrestart flag drift (`--xterm` accepted by lrestart's parser but rejected by
  lstart's re-parse — `LrestartCommand.py:156`, recorded in `DIVERGENCES.md`) is ported
  as-is: in `json` mode that invocation performs the clean, then exits **2** with usage
  text on stderr and **nothing on stdout**. Scripted clients must not pass `--xterm`.

### 3.4 `wipe`

Key order: `settings_wiped`, `all_users`, `machines`, `links`.

```json
{"settings_wiped":false,"all_users":false,"machines":["pc1"],"links":["A"]}
```

- `-s/--settings`: `{"settings_wiped":true,"all_users":false,"machines":[],"links":[]}`.
- `machines`/`links`: canonically sorted names of what was removed (empty when none).
- `-a/--all` without root → E13 `Privilege`
  (`You must be root in order to wipe all Kathara devices of all users.`), exit 1.
- `json` mode requires `-f/--force` (§1.5), else `ConfirmationRequired`, exit 1.

### 3.5 `list`

Key order: `machines`.

```json
{"machines":[{"network_scenario_id":"...","name":"pc1","container_name":"...","user":"...","status":"running","image":"kathara/base"}]}
```

- Inventory objects (§3.0.2), sorted by (`network_scenario_id`, `name`) — canonical-order
  ruling; Python's row order is manager dict insertion order, which is API-listing order.
- `-n/--name` filters as in Python (single-device or empty array). `-a/--all` without root
  → E13 `Privilege`, exit 1. `-w/--watch` → usage error, exit 2 (§1.1).

### 3.6 `exec` (aggregate `json` mode)

Key order: `stdout`, `stderr`, `exit_code`.

```json
{"stdout":"PING 8.8.8.8 (8.8.8.8): 56 data bytes\n...","stderr":"","exit_code":0}
```

- `stdout`/`stderr` (string): the complete, concatenated remote output per stream, decoded
  as UTF-8 with invalid sequences replaced by U+FFFD. (Pinned divergence from Python's
  per-chunk `chardet` detection, which crashes with `TypeError` on undecidable binary
  output — `ExecCommand.py:103-110`, `cli.md` gotcha 5. Recorded in `DIVERGENCES.md`;
  goldens use ASCII output.)
- `--no-stdout` / `--no-stderr` suppress collection: the suppressed field is `""`.
- `exit_code` (int): the remote command's exit code, from the exec-inspect after stream
  exhaustion (`DockerExecStream.exit_code()` → `exec_inspect['ExitCode']`).
- **Process exit code = `exit_code`** — verified: `ExecCommand.run` returns
  `exec_output.exit_code()` and the entrypoint exits with the command's return value.
  `exec` is the only command whose success exit is not necessarily 0 (both modes).
- Failures before execution (device not found / not running, bad scenario) → E13, exit 1.

### 3.7 `check`

Key order: `manager`, `manager_version`, `runtime_version`, `kathara_version`,
`os_version`, `container_test`; inside `container_test`: `image`, `ok`, `error`.

```json
{"manager":"Docker (Kathara)","manager_version":"27.3.1","runtime_version":"go1.24.1","kathara_version":"<go port version>","os_version":"Linux-6.12.88+deb13-cloud-amd64-x86_64","container_test":{"image":"kathara/base","ok":true,"error":null}}
```

- `manager`: `get_formatted_manager_name()` output (`"Docker (Kathara)"` /
  `"Kubernetes (Megalos)"`); `manager_version`: `get_release_version()`.
- `runtime_version`: the Go runtime version. Replaces Python's "Python version is" line;
  recorded divergence (human mode prints the analogous Go line).
- `os_version`: Linux `uname` triple `sysname-release-machine`; `platform.platform()`
  equivalent elsewhere (parity with `CheckCommand.py:78`).
- `container_test`: the deploy+undeploy self-test of lab `kathara_test` / device
  `hello_world`. On failure: `ok:false`, `error` = the exception string (the text Python
  prints after `× Running container failed: `, glyph U+00D7, `CheckCommand.py:76`).
- **Exit-code exception (pinned):** a failed container test emits this **report envelope**
  (not E13) with `ok:false` and exits **1** — mirroring Python, which prints the report
  and `return 1` without raising. Errors before the report (e.g. daemon connection at
  startup) are E13, exit 1, as usual.

### 3.8 `vstart`

Key order: `lab`, `machine`, `dry_run`, then `links` (non-dry only).

```json
{"lab":{"name":"kathara_vlab","hash":"<generate_urlsafe_hash(\"kathara_vlab\")>","path":null},"machine":"pc1","dry_run":false,"links":["A","B"]}
```

- `links`: collision domains attached via `--eth`, canonically sorted (plus nothing for
  `--bridged`, which uses the reserved `kathara_host_bridge` — included when deployed).
- Dry mode (`--print`/`--dry-run`): `{"lab":{...},"machine":"pc1","dry_run":true}` — exit
  0 immediately after flag validation, matching Python's early return (dry-run proves only
  that argparse accepted the flags; `VstartCommand.run` step 3).
- Meta/option validation failures (`MachineOptionError` etc.) → E13, exit 1. Malformed
  `--eth` value → usage error exit 2 (argparse type), but a non-numeric interface number
  → E13 code `Syntax`, exit 1 (Python raises `SyntaxError` past argparse;
  `VstartCommand.py:243`).

### 3.9 `vclean`

Key order: `lab`, `machine`, `machines`.

```json
{"lab":{"name":"kathara_vlab","hash":"...","path":null},"machine":"pc1","machines":["pc1"]}
```

- `machines`: what was actually undeployed. **Empty array for a non-existent name** — the
  Python silent no-op success (`VcleanCommand.py`: undeploy of empty selection, exit 0).

### 3.10 `lconfig`

Key order: `lab`, `machine`, `added`, `removed`.

```json
{"lab":{"name":null,"hash":"...","path":"/labs/x"},"machine":"pc1","added":[{"link":"A","mac":null},{"link":"B","mac":"00:11:22:33:44:55"}],"removed":[]}
```

- `added`: objects `{"link":string,"mac":string|null}` in **CLI argument order** (order is
  semantic: it determines interface numbering; `cli.md` §2). `mac` null when the `CD/MAC`
  value had no MAC part.
- `removed`: array of collision-domain names, CLI order. Exactly one of `added`/`removed`
  is non-empty (the `--add`/`--rm` MEG is required and exclusive).
- Parse errors of the scenario are **not** swallowed here (no fallback lab — faithful to
  `LconfigCommand.py`): E13, exit 1.
- The `cd_mac` trap is preserved (pinned): a malformed `--add` value (`utils.py:468`
  `SyntaxError`, which escapes argparse) → E13 code `Syntax`, exit 1; a malformed `--rm`
  value (`ArgumentTypeError`) → usage error, exit 2.

### 3.11 `vconfig`

Identical shape to §3.10 with the vlab `lab` object:

```json
{"lab":{"name":"kathara_vlab","hash":"...","path":null},"machine":"pc1","added":[{"link":"A","mac":null}],"removed":[]}
```

### 3.12 `config` (the §3.2 settings rebuild's non-interactive command)

The `config` subcommand surface (`get`/`set`/`list`/`reset`) is defined by the settings
rebuild design; this contract pins only the JSON shapes. Values use the frozen
`kathara.conf` JSON schema types (spec §0.4; `SYNTHESIS.md` §1.5).

| Subcommand | Envelope (key order pinned) |
|---|---|
| `config get <key>` | `{"key":"image","value":"kathara/base"}` |
| `config set <key> <value>` | `{"key":"image","value":"kathara/frr","saved":true}` |
| `config list` | `{"settings":{...}}` — the 12 base keys in `_to_dict` order, then the active addon's keys (same order as the on-disk file) |
| `config reset` | `{"settings":{...defaults...},"saved":true}` |

Unknown key on `get`/`set` → E13 code `Settings`, exit 1. Validation failures on `set`
(prefix regex, debug level, etc.) → E13 code `Settings` with the exact Python
`SettingsError` message, exit 1.

---

## 4. `jsonl` streaming envelope (`exec`)

```console
$ kathara exec --format jsonl --lab-hash FwFaxbiuhvSWb2KpN5zw pc1 -- ping -c 2 8.8.8.8
{"type":"stdout","data":"PING 8.8.8.8 (8.8.8.8): 56 data bytes\n"}
{"type":"stdout","data":"64 bytes from 8.8.8.8 ...\n"}
{"type":"exit","code":0}
```

### 4.1 Event types

| Event | Keys (order pinned) | Semantics |
|---|---|---|
| `stdout` | `type`, `data` | one non-empty chunk of remote stdout |
| `stderr` | `type`, `data` | one non-empty chunk of remote stderr |
| `exit` | `type`, `code` (int) | terminal event on normal completion; `code` = remote exit code; process then exits with `code` |
| `error` | `type`, `error` (object, §5.1) | terminal event on failure; process exits 1 |
| `interrupted` | `type` | terminal event on SIGINT; process exits 0 (§6.2) |

Exactly one terminal event (`exit`, `error`, or `interrupted`) ends every `jsonl` stream;
nothing follows it on stdout. Clients must ignore unknown event types (§9).

### 4.2 Demux null-vs-empty ruling (pinned)

Python's stream yields per-frame tuples `(stdout, stderr)` where each side is
`bytes | None` — the docker SDK demux emits `None`, not `b""`, for the stream a frame does
not belong to (`DockerMachine._exec_run` with `stream=True, demux=True`; `SYNTHESIS.md`
§1.4). The Python CLI writes `""` for a falsy stdout side and skips falsy stderr sides
(`ExecCommand.py:103-110`) — i.e. `None` and `b""` are already observably identical.

**Pinned:** an event is emitted **only for a non-empty chunk side**. `None` sides and
empty (`b""`) sides emit nothing. A frame with both sides empty emits no event. There are
no `{"data":""}` events in v1 (the spec §5.2 example's `{"type":"stderr","data":""}` is
illustrative, not normative). Consequences: event count is not frame count; chunk
boundaries are transport artifacts and not contract — clients must concatenate `data`
per stream. `--no-stdout`/`--no-stderr` suppress that stream's events entirely.

`data` is UTF-8 text with invalid sequences replaced by U+FFFD (same decode rule and
recorded chardet divergence as §3.6). Chunk boundaries may split multi-byte sequences;
the encoder carries partial sequences over to the next event rather than emitting a
replacement at a chunk boundary that a later chunk completes.

### 4.3 Future streams

Post-1.0 resource stats reuse this envelope with new event types (one event per sample),
per spec §5.2. New event types are additive (§9).

---

## 5. Error envelope

### 5.1 Shape

```json
{"error":{"code":"MachineNotFound","message":"Device `pc1` not found.","machine":"pc1"}}
```

Key order: `code`, `message`, then structured fields in the per-code order of §5.4.

- In `json` mode: the whole object `{"error":{...}}` is the single stdout object, exit 1.
- In `jsonl` mode: the same inner object appears as `{"type":"error","error":{...}}`, exit 1.
- Human mode is untouched: `CRITICAL ({PythonClassName}) {message}` rendering per Python
  (`kathara.py:102-108`; `ERROR_CODES.md` §0.1). Note the deliberate asymmetry, pinned:
  **human mode prints the Python exception class name** (e.g. `MachineNotFoundError`) for
  byte-parity; **`code` carries the stable identifier** (e.g. `MachineNotFound`).
- Partial parallel failures: `error` is the **primary** error (canonical order); when more
  than one error was collected from a failed batch, a sibling key `errors` (array of error
  objects, same shape, canonical order, primary included as element 0) follows `error`.
  Absent for single-error failures. Frozen semantics in `ERROR_CODES.md` §6; clients that
  only read `error` observe exactly the Python behavior.

### 5.2 `code`

`code` values are the stable identifiers from `docs/port/ERROR_CODES.md` (the frozen
registry, which corrects and supersedes the draft `ERROR_CODES.tsv`) and are part of
the public contract. Accepted rulings incorporated:

- The 33 Kathara exception classes map 1:1 to codes (spec §4.3).
- **User-reachable builtin exceptions get stable codes:** `Syntax`, `Value`, `OS`
  (covers Python `IOError` too — `IOError` is an alias of `OSError` in Python 3, so
  there is no separate `IO` code), `FileNotFound`, `FileExists` (kept for message
  parity; semantically "does not exist"), `NotADirectory`, `Permission`, `Connection`
  (per `ERROR_CODES.md` §1.2).
- **Everything else buckets to `InternalError`** (programmer errors, third-party leaks
  with no translation, impossible-in-Go paths). `InternalError` carries only `code` and
  `message`.
- Contract-contributed codes (JSON-CLI-layer, defined here and registered in
  `ERROR_CODES.md` §1.4): `ConfirmationRequired` (§1.5), `FeatureNotAvailable` (§5.6),
  `InternalError`.

### 5.3 `message`

`message` is the **exact Python format-string output** for the mapped exception — the
per-class templates catalogued in `ERROR_CODES.md` §2 (typos, backticks and missing periods
included; e.g. `` Collision domain A is already the network scenario. `` sic).

**Set-interpolation ruling (pinned, accepted OQ-7e):** where Python interpolates a `set`
(nondeterministic order), Go emits the elements **sorted lexicographically inside the same
Python-set repr format** — `{'...', '...'}` with single quotes and `", "` separators.
Verified instance: `DockerManager.py:151/155` →
`The following devices are not in the network scenario: {'pc1', 'pc3'}.`
The same rule applies to any joined-collection message where Python's order is
nondeterministic (e.g. `', '.join(kwargs.keys())` sites keep Python's deterministic kwarg
order — only genuinely unordered collections are sorted).

### 5.4 Structured fields per code

Additive, typed fields mirroring the attributes/interpolants the Python classes carry.
Emission order after `message` is the order listed here. Absent when unknown at the
raise site.

| Codes | Fields |
|---|---|
| `MachineNotFound` (single-device variants), `MachineNotRunning`, `MachineNotReady`, `MachineAlreadyExists`, `MountDenied` (device variant) | `machine` (string) |
| `MachineNotFound` (plural variant, §5.3) | `machines` (array of string, sorted) |
| `MachineOption` | `machine` (string), `option` (string — the option name being parsed) |
| `LabNotFound` | `machine` (string) or `link` (string) — the referenced object, when known |
| `MachineBinary` | `binary` (string), `machine` (string) — the lab-checker contract fields |
| `InvalidImageArchitecture` | `image` (string), `arch` (string) |
| `NonSequentialMachineInterface` | `iface` (int), `machine` (string) |
| `InterfaceMacAddress` | `mac` (string), `iface` (int), `machine` (string) |
| `LinkNotFound`, `LinkAlreadyExists` | `link` (string) |
| `MachineCollisionDomain` | `machine` (string), `link` (string) — when both appear in the message; variant 1 (`ERROR_CODES.md` §2): `machine` (string), `iface` (int) |
| `DockerImageNotFound`, `Connection` (image-pull variant) | `image` (string) |
| `SettingsNotFound`, `FileExists`, `NotADirectory` | `path` (string) |
| `HostArchitecture` | `arch` (string) |
| `Syntax`, `Value` (file-parse variants) | `file` (string), `line` (int) — when the message carries them (`In {conf_name} - Line {n}` variants) |
| `FeatureNotAvailable` | `feature` (string, §5.6) |
| `ConfirmationRequired` | (none) |
| all others | (none in v1; additive later) |

### 5.5 Exit code

Every error envelope exits **1**. No error envelope is ever paired with exit 0 or 2.
Usage errors (the argparse/cobra exit-2 class) produce **no JSON on stdout** in any mode:
usage text on stderr, exit 2 — faithful to Python, where argparse bypasses the exception
machinery entirely (`CLI_SURFACE.md` §0.6, §15).

### 5.6 `FeatureNotAvailable`

Deferred features (accepted ruling) are present as stubs that emit:

```json
{"error":{"code":"FeatureNotAvailable","message":"lab.ext external links are not supported in this release. Use Kathará 3.8.x.","feature":"lab.ext"}}
```

Pinned `feature` tokens (closed set for 1.0): `"lab.ext"` (also covers external collision
domains and nsenter paths), `"linfo"` (the whole command; it stays in the command table
and help for parity but always errors), `"stats-sampling"` (resource fields / watch
streams), `"webhooks"` (Docker Hub / GitHub version checks — these are silently skipped,
so this token is reserved but normally unobservable). Exact per-feature messages are
pinned in `ERROR_CODES.md`. Exit 1, both `json` and human modes (human prints
`CRITICAL (FeatureNotAvailableError) {message}`).

---

## 6. Exit codes

### 6.1 Table

| Code | Meaning |
|---|---|
| 0 | Success. Also: `-v/--version`; dry runs; **Ctrl-C/SIGINT (all commands, all modes)** (§6.2); `wipe` declined at the human-mode prompt (`sys.exit()` no-arg, `WipeCommand.py:60`) |
| 1 | Any error envelope (§5.5); top-level dispatch failures (no/invalid/unknown command, human help output); `check` failed container test (with report envelope, §3.7) |
| 2 | Usage errors: unknown flag, missing required, bad flag value of the `ArgumentTypeError` class, `--format` misuse (§1.1). Usage on stderr, empty stdout |
| N | `exec` only: the remote command's exit code (§3.6/§4.1) — verified `ExecCommand.run` returns `exec_output.exit_code()` |

Per-command exceptions, exhaustively: `exec` (exit = remote code), `check` (exit 1 with a
non-error report envelope on failure). `connect` exits 0 on clean detach and does **not**
propagate the remote shell's exit status (human-only command; listed for completeness).

### 6.2 SIGINT / Ctrl-C (pinned, accepted OQ-7d ruling)

Parity with Python (`kathara.py:96-101`): **exit 0, always**, in every mode.

- Warning parity: the text `You interrupted Kathara during a command. The system may be in
  an inconsistent state! If you encounter any problem please run `kathara wipe`.` is
  emitted unless the command is in the exempt list. Exempt list (pinned):
  `exec`, `linfo`, `list`, `settings` (the Python four), **plus `config`** (new command,
  non-deploying — pinned here since Python has no entry for it). In human mode the warning
  goes where Python puts it (stdout via logging); in `json`/`jsonl` it goes to stderr.
- `json` mode stdout: the interrupt envelope `{"interrupted":true}` (single key), exit 0.
  A client seeing exit 0 must check for this key before treating stdout as a result
  envelope.
- `jsonl` mode: terminal event `{"type":"interrupted"}` (no `exit` event follows), exit 0.
- Go's context-cancellation (spec §0.2 #11) governs *cleanup* on interrupt; it does not
  change this observable exit contract.

---

## 7. `--from-archive` tar-on-stdin deploy (spec §5.4)

### 7.1 Invocation

```
kathara lstart --from-archive - --name "BGP Announcement" --format json
```

- `--from-archive ARG`: v1 accepts only `-` (read the archive from stdin). Any other value
  is a usage error, exit 2. (File-path support would be additive.)
- `--name NAME`: **required** whenever `--from-archive` is given (usage error otherwise),
  and only valid together with `--from-archive` (usage error otherwise). NAME must be
  non-empty.
- `--from-archive` is mutually exclusive with `-d/--directory` (usage error, exit 2).
- Available on `lstart` only. Works in all three formats (`--format human` included);
  `json` is the expected client mode.
- `vstart` does not take archives; it gains `--format json` for the incremental pattern
  (§3.8).

### 7.2 Name → hash rule

The lab identity is `generate_urlsafe_hash(NAME)` (`utils.py:53-57`): strip non-ASCII
runs → md5 → urlsafe-base64 → drop the two `==` padding chars → delete every `-` and `_`.
Golden constant: `--name "Default scenario"` → hash `9pe3y6IDMwx4PfOPu5mbNg`.

**Precedence (pinned):** `--name` is applied **after** parsing, via the same semantics as
Python's `Lab.name` setter (which recomputes `hash`; `model/Lab.py:88-90`). It therefore
overrides a `LAB_NAME=` line in the archived `lab.conf` (which `LabParser` assigns through
that same setter, `LabParser.py:86-87`) for both `name` and `hash`. All backend naming
(container names, labels, network names) uses the resulting hash exactly as a
directory-based deploy would.

### 7.3 Tar layout

The archive is **a normal Kathará scenario directory rooted at the archive root** — no
wrapping top-level directory (a single leading component is *not* stripped; `./` prefixes
are normalized away):

```
lab.conf                (required unless -F/--force-lab)
lab.dep                 (optional)
<device>.startup        (optional, per device)
<device>.shutdown       (optional)
shared.startup          (optional)
shared.shutdown         (optional)
<device>/               (optional per-device directory trees)
kathara.conf            (optional; honored exactly like a directory deploy's per-lab
                         settings override — Command._load_custom_configuration parity)
lab.ext                 (presence → FeatureNotAvailable "lab.ext", exit 1)
```

### 7.4 Format and constraints (pinned)

- Accepted encodings: POSIX ustar/PAX tar, plain or gzip-compressed (detected by magic
  bytes). Anything else → E13 code `OS`, exit 1.
- Rejected entries (E13 code `Invocation`, exit 1, nothing deployed): absolute paths,
  paths containing `..` traversal, symlink and hardlink entries, device/FIFO entries.
  Regular files and directories only. File modes are preserved; owners are ignored.
- No CLI-imposed size limit in v1; backend limits still apply (e.g. the Kubernetes 3 MiB
  per-device ConfigMap limit → `KubernetesConfigMap` error).
- Semantics after extraction (pinned): the archive is materialized to a private temporary
  directory and `lstart` proceeds **exactly** as if `-d <tmpdir>` had been given — same
  parse order, same `-F` fallback (code `OS`, `No lab.conf in given directory.`, without
  `-F`), same `--exclude`/positional device selection, same dry mode — except (a) the
  name/hash override of §7.2 and (b) `lab.path` is reported as null in envelopes (§3.0.1).
  The temp directory is the lab's host path for mounts/`hostlab` during the run.
- stdin is consumed to EOF before deployment begins. In `json` mode stdin is used for
  nothing else (§1.3, §1.5).

### 7.5 Undeploying archive labs

An archive-deployed lab has no stable path; cleanup addresses it by identity:
`kathara lclean --lab-name "BGP Announcement"` or `--lab-hash <hash>` (§8). This is the
Python client's `undeploy_lab` mapping.

---

## 8. Scenario addressing flags (client plumbing)

The spec's own streaming example uses `--lab-hash` (`PORT_SPEC.md` §5.2). Pinned: the
commands that *resolve a running scenario* gain two flags, mutually exclusive with each
other and with `-d/--directory` (and with `-v/--vmachine` where that exists):

| Flag | Meaning |
|---|---|
| `--lab-hash HASH` | address the scenario by hash directly |
| `--lab-name NAME` | address by name; hash = `generate_urlsafe_hash(NAME)` (identical to Python manager methods' `lab_name` parameter, e.g. `DockerManager.exec:472`) |

Commands: `exec`, `lclean`, `lconfig`. (`lstart`/`lrestart` deploy from files and do not
take them; the v-family is fixed to `kathara_vlab`; `list` is inventory-wide.) The spelling
is `--lab-name`, not `--name`, to avoid colliding with the existing `-n/--name`
device-name flags (`lconfig`) — `lstart --name` remains archive-only (§7.1). These flags
work in all formats. Defaulting is unchanged: with none of `-d`/`-v`/`--lab-hash`/
`--lab-name` given, the current directory is used, exactly as Python.

---

## 9. Contract stability

### 9.1 Frozen in v1 (breaking to change)

- The envelope catalog of §2: top-level key names and structure of E1–E14, event `type`
  values S1–S5, and the one-object rule (§1.4).
- **Key emission order** as pinned per envelope (byte-goldens depend on it).
- `error.code` values: the stable identifiers of `ERROR_CODES.md` including
  `FeatureNotAvailable`, `ConfirmationRequired`, `InternalError`; the `feature` token set
  (§5.6); the structured-field names of §5.4.
- `message` texts: the exact Python format strings (they are shared contract with human
  mode and the Python client's re-raised exception messages).
- The exit-code table (§6.1), the SIGINT contract (§6.2), and the prompt rulings (§1.5).
- The demux ruling (§4.2): no empty-`data` events; concatenation semantics.
- The inventory object's six canonical keys and their order (§3.0.2).
- The `--from-archive` layout, constraints and name→hash rule (§7).
- JSON encoding rules (§1.3): compact, UTF-8, no HTML escaping, one trailing `\n` per
  object.

### 9.2 Extensible (additive, no version bump)

- **New fields appended at the end of any object.** Clients MUST ignore unknown keys.
- **New event types** in `jsonl`. Clients MUST skip unknown `type` values.
- **New error codes** for new failure modes. Clients MUST map unknown codes to a generic
  error (the Python client raises its base `KatharaError` with the message).
- New commands, new subcommands, new flags (including widening §8 to more commands), and
  file-path support for `--from-archive`.
- New structured fields on existing error codes (appended after the pinned ones).
- Nullable-field population (a field documented `|null` starting to carry values).

### 9.3 Versioning

Envelopes carry **no version field** in v1. The contract version is this document's; the
binary's `kathara -v` output identifies the build. Any breaking change (removing/renaming
a frozen key, changing key order, changing an exit code, changing a `code` value) requires
a new contract version and an explicit new opt-in surface (e.g. a new `--format` value);
`--format json`/`jsonl` remain v1-semantics forever.

---

## Appendix A: pinned resolutions of ambiguities (with source verification)

| # | Ambiguity | Pin | Verified against |
|---|---|---|---|
| A1 | Spec §5.1 "human unchanged" vs "logs to stderr in all modes" | human keeps Python's stdout assignment; stderr rule applies to json/jsonl only | `CLI_SURFACE.md` §0.4; Layer A goldens are stdout captures |
| A2 | Prompts in json mode | never prompt; wipe → `ConfirmationRequired` unless `--force`; image-update Prompt → auto-no; volume Prompt → auto-yes; `--wait` → no ENTER override | `WipeCommand.py:59-60`, `UpdateDockerImage.py`, `MountDevicesVolumes.py`, `DockerMachine._wait_startup_execution` |
| A3 | Demux `None` vs `b""` (OQ-7c) | both = absent; events only for non-empty sides; no `data:""` events | `ExecCommand.py:103-110` (falsy stdout → `""` no-op write; falsy stderr skipped); docker SDK demux yields `(bytes\|None, bytes\|None)` |
| A4 | `exec` exit semantics | process exit = remote exit code, json and jsonl and human | `ExecCommand.run` returns `exec_output.exit_code()`; `DockerExecStream.exit_code()` → `exec_inspect['ExitCode']`; `kathara.py:95` |
| A5 | Ctrl-C in json (OQ-7d) | exit 0 all modes; `{"interrupted":true}` / `{"type":"interrupted"}`; warning exempt list = Python's four + `config` | `kathara.py:96-101` |
| A6 | Set repr in messages (OQ-7e) | sorted elements inside Python set-repr format | `DockerManager.py:151,155` |
| A7 | Inventory key order; K8s field mismatch | Python `to_dict` order (`network_scenario_id,name,container_name,user,status,image`); K8s: `container_name`←pod name, `user`=null, additive `assigned_node` | `DockerMachineStats.py:121-127`; `KubernetesMachineStats.py:89-97` |
| A8 | `--name` vs `LAB_NAME` in archived lab.conf | `--name` applied post-parse via name-setter semantics, always wins | `model/Lab.py:88-90`; `LabParser.py:86-87` |
| A9 | `cd_mac` SyntaxError escaping argparse | `--add` malformed → envelope `Syntax` exit 1; `--rm` invalid → usage exit 2 | `utils.py:468` (`SyntaxError`), `cli/ui/utils.py` (`alphanumeric` → `ArgumentTypeError`) |
| A10 | `check` failure shape | report envelope with `container_test.ok:false`, exit 1 (not an error envelope) | `CheckCommand.py:76-79` (prints `×` U+00D7 line, `return 1`, no raise) |
| A11 | Machine-filter shapes (OQ-7a) | internal only; CLI keeps Python call shapes (lstart empty sets, lclean None); not surfaced in envelopes | `LstartCommand.py` (`set(...)` always), `LcleanCommand.py` (empty→`None`) |
| A12 | `exec` command payload (OQ-7b) | internal only; single token → string, multi → list, as Python; envelope unaffected | `ExecCommand.py:96` (`.pop()` on single-element list) |
| A13 | Watch modes under json | exit 2 usage error; streaming inventory reserved post-1.0 | spec §5.2 ("exec is the only streaming consumer in 1.0") |
| A14 | Scenario addressing for archive labs | `--lab-hash`/`--lab-name` on `exec`/`lclean`/`lconfig`; spelling avoids `-n/--name` collision | spec §5.2 example (`--lab-hash`); `DockerManager.exec:468-472` (`lab_name` → `generate_urlsafe_hash`) |
| A15 | Exec output decoding | UTF-8 with U+FFFD replacement, chunk-boundary-safe; chardet divergence recorded | `ExecCommand.py:103-110` crash path (`chardet` → `decode(None)` `TypeError`), `cli.md` gotcha 5 |
