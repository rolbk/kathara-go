# PACKAGE_GRAPH.md — Phase 0 contract: Go package graph and Python→Go file mapping

**Status: FROZEN.** Later phases implement against this document without relitigating it.
Changes require a human ruling recorded here, not agent judgment.

Ground truth: `docs/port/PORT_SPEC.md` (spec), `/root/kathara/analysis/` (SYNTHESIS.md + nine
subsystem reports + ORDERING/NILABILITY/CONCURRENCY registers, ERROR_CODES.tsv), Python source
`kathara-python/src` at v3.8.3.

Accepted rulings honored throughout (they resolve SYNTHESIS OQ-1/3/5/6/8/10/13/15):

| Ruling | Effect here |
|---|---|
| MAC goldens pivot to DriverOpts wiring | no MAC-derivation code anywhere; explicit MACs pass through as `kathara.mac_addr` |
| Carve-out: `get_iptables_version` | ported into `backend/docker` (`iptables_linux.go`), not a ported `os/` package |
| Carve-out: entrypoint privilege drop | ported into `cmd/kathara` (`privileges_linux.go`); `@privileged` is a no-op in 1.0 |
| `Meta` gets an `Extras` map | `model/meta.go` carries `Extras map[string]any` for unknown options |
| Error codes | user-reachable builtin exceptions get stable codes; the rest bucket to `InternalError` (see `docs/port/ERROR_CODES.md`) |
| Nondeterministic Python orders | Go emits canonical **sorted** order (sysctl DriverOpts join, FolderParser glob, pack walk) |
| Ctrl-C | exits **0** with warning parity (suppressed for exec/linfo/list/settings, plus the port-new `config` — `JSON_CLI_CONTRACT.md` §6.2) |
| `linfo`, `lab.ext`, stats sampling, webhooks | `FeatureNotAvailable` stubs (webhooks: silently absent — they had no user-visible surface beyond the version banner) |

---

## 1. Package inventory, import edges, topological order

### 1.1 Packages and responsibilities

All paths relative to module root `github.com/KatharaFramework/kathara-go`.

| # | Package | Responsibility |
|---|---|---|
| 1 | `kerrors` | The complete error taxonomy (§0.2 #6): one sentinel/struct per Python exception class, stable string codes per `docs/port/ERROR_CODES.md`, `FeatureNotAvailable`, `InternalError` bucket, `MachineError{Machine,Op,Err}` wrapper, `errors.Join` conventions. Leaf package; **nothing in the module is below it** |
| 2 | `internal/util` | Port of `utils.py` (+ `version.py`, `strtobool`): `GenerateURLSafeHash`, `Slug`, current-user identity (SUDO-aware), platform helpers, dir-permission checks, tar packing + CRLF/BOM normalization + binary sniff, engine-version parse, tuple version compare, human-readable bytes |
| 3 | `vfs` | Filesystem abstraction replacing pyfilesystem2 (§0.2 #9): `FS` interface, `OSDir`, `Memory`, plus the `FilesystemMixin` convenience methods as free functions (`CreateFileFromString`, `UpdateFileFromString`, `WriteLineBefore`, `DeleteLine`, …) |
| 4 | `settings` | Config load/save/validate at `~/.config/kathara.conf`, frozen schema and one-space-indent serialization (§3.2); Docker/K8s addons; `SharedCollisionDomains` enum (from `types.py`); validators folded in from `validator/` |
| 5 | `model` | `Lab`, `Machine`, `Link`, `Interface`, typed `Meta` (+`Extras`); ordered interface slice (§0.2 #4); `PackData`; **no backend, settings, or event imports** — Setting-backed defaults are injected (`model.Defaults`, resolved by the caller; OQ-4 resolution) |
| 6 | `event` | Typed event dispatcher (§0.2 #13): typed payload structs (may reference `model` types), goroutine-safe dispatch, registration order preserved (progress bar before terminal spawn) |
| 7 | `labfile` | `lab.conf` / `lab.dep` / folder / option parsers, `depgen` topological sort, `lab.ext` `FeatureNotAvailable` stub. Produces `model` values; the Layer B conformance surface |
| 8 | `kathara` | Public API: `Client` (§0.2 #10), `Manager` interface with `context.Context` on every operation (§0.2 #11), backend registry (§0.2 #7, declared order docker → kubernetes per SYNTHESIS C-5), `ExecStream`, `TTYSession`, inventory `MachineStats`/`LinkStats`, options; **aliases re-exporting the `kerrors` taxonomy** so the spec-§4.3 surface (`kathara.ErrLabNotFound`, …) holds |
| 9 | `term` | Rebuilt terminal integration (§0.2 #2): built-in bubbletea multiplexer (default), tmux backend driving the `tmux` binary via CLI, ported external-emulator adapters (opt-in), `connect` single-device runner, raw-mode/console handling, PTY/ConPTY |
| 10 | `backend/docker` | Docker backend implementing `kathara.Manager` via `github.com/docker/docker/client`: machine/link/image/plugin/exec/stats(inventory)/tty-transport. Separately importable; excluding it from a build drops the Docker SDK |
| 11 | `backend/kubernetes` | Megalos backend implementing `kathara.Manager` via `client-go`. Separately importable; a Docker-only build excludes `client-go` (§0.2 #8) |
| 12 | `internal/cliout` | Human / json / jsonl renderers (§0.2 #12, §5), progress bars, image-pull/update event handlers, confirmation prompts, lab/topology tables. In json/jsonl modes stdout carries only protocol, logs go to stderr, progress/prompts are suppressed; human mode keeps Python's all-stdout stream assignment (`JSON_CLI_CONTRACT.md` §1.2–§1.3) |
| 13 | `cmd/kathara` | `main`, cobra root and all commands; vstart/vclean/vconfig as sugar (§0.2 #3); `config` + `settings` (bubbletea form lives here, validation in `settings`); event-handler wiring; startup privilege drop; `--from-archive` tar intake; Ctrl-C contract |
| 14 | `tools/goldenharness` | Layer A oracle: drives `kathara` binaries as subprocesses, snapshots `docker inspect`/in-container state, hosts the Layer B vector corpus. **Separate Go module with zero imports of production packages** (oracle independence) |
| — | `python/` | The §7 Python client package. Not a Go package; excluded from this import graph. See §6 |

### 1.2 Import edge list (complete; intra-module edges only)

Standard library and external modules excluded here (external deps are §5).

| Package | Imports (package → package) |
|---|---|
| `kerrors` | — (none) |
| `internal/util` | `kerrors` |
| `vfs` | — (none) |
| `settings` | `kerrors`, `internal/util` |
| `model` | `kerrors`, `internal/util`, `vfs` |
| `event` | `model` |
| `labfile` | `kerrors`, `internal/util`, `model` |
| `kathara` | `kerrors`, `model`, `settings`, `event` |
| `term` | `kerrors`, `internal/util`, `settings`, `kathara` |
| `backend/docker` | `kerrors`, `internal/util`, `vfs`, `model`, `settings`, `event`, `kathara` |
| `backend/kubernetes` | `kerrors`, `internal/util`, `vfs`, `model`, `settings`, `event`, `kathara` |
| `internal/cliout` | `kerrors`, `model`, `event`, `kathara` |
| `cmd/kathara` | `kerrors`, `internal/util`, `vfs`, `settings`, `model`, `event`, `labfile`, `kathara`, `term`, `backend/docker`, `backend/kubernetes`, `internal/cliout` |
| `tools/goldenharness` | — (none; separate module) |

Hard edges the spec mandates, restated as reviewer rules:

- `model` imports **no** backend, no `settings`, no `kathara` (spec §2: "`model` must not import `backend`"; the settings exclusion is the OQ-4 resolution — defaults are injected as `model.Defaults` by `cmd`/`kathara`, never read from a singleton).
- `backend/*` import `model` and `kathara`; `kathara` defines the `Manager` interface they implement.
- Backends register via explicit `Register` calls made from `cmd/kathara` (build-tag pair `backends_all.go` / `backends_dockeronly.go` under `//go:build nok8s` gives the client-go-free build of §0.2 #8). No `init()`-magic registration: registration order is the declared docker → kubernetes order.
- Nothing imports `cmd/kathara`; nothing imports `tools/goldenharness`.

### 1.3 Topological order (proof of acyclicity)

```
kerrors → vfs → internal/util → settings → model → event → labfile
  → kathara → term → backend/docker → backend/kubernetes
  → internal/cliout → cmd/kathara      (tools/goldenharness: disjoint)
```

Proof: for every package in §1.2, every listed import appears strictly earlier in this order —
`internal/util`(kerrors), `settings`(kerrors, util), `model`(kerrors, util, vfs), `event`(model),
`labfile`(kerrors, util, model), `kathara`(kerrors, model, settings, event), `term`(…, kathara),
`backend/*`(…, kathara), `internal/cliout`(kerrors, model, event, kathara), `cmd/kathara`(all).
A linear order consistent with every edge exists ⇒ the graph is a DAG. `tools/goldenharness` and
`python/` have no edges in either direction.

The one cycle the spec's own layout would have forced — `kathara` (holding the taxonomy per §2)
↔ `model` (raising taxonomy errors, while `kathara.Manager` uses `model` types) — is broken by
the `kerrors` leaf package; `kathara` re-exports aliases (`var ErrLabNotFound =
kerrors.ErrLabNotFound`, `type MachineError = kerrors.MachineError`) so the public API of spec
§4.3 is unchanged. This is deviation D-1 (§7).

---

## 2. Python module → Go package mapping (all 173 files)

Dispositions:

- **PORT** — faithful port, diff-reviewable against the Python (spec §0.1).
- **PORT\*** — faithful behaviour through a sanctioned §0.2 shape change; goldens are the arbiter.
- **REBUILT** — §0.2 #1/#2 subsystem rebuilds.
- **REPLACED** — mechanism replaced wholesale (§0.2 #7 registry, cobra, exec-tmux); no line-level Go counterpart.
- **COLLAPSED** — Python-only artifact or §0.2 #3 collapse; absorbed elsewhere or vanishes.
- **DEFERRED** — §0.3; where a ruling requires it, a `FeatureNotAvailable` stub location is named.

### 2.1 Root, auth, os, webhooks (12 files)

| Python (`src/…`) | Disposition | Go target / justification |
|---|---|---|
| `kathara.py` | PORT\* | `cmd/kathara/main.go` + `root.go`. Startup order preserved (settings load/first-run-save before logging config, event registration, `Setting.check` skipped iff `"settings"` substring). Ctrl-C exits 0 with warning parity (ruling; ERROR_CODES.tsv header item 5). Python-only steps (`freeze_support`, `check_python_version`, ImportError branch) vanish. Privilege drop: see `auth/` row |
| `Kathara/utils.py` | PORT | `internal/util/` (hash.go, slug.go, user_*.go, perms_*.go, input_*.go, arch_*.go, wsl_*.go, tar.go, binary.go, engine.go, bytes.go, check.go). Sanctioned deletions per spec §3.1 row 2: `class_for_name`, `chunk_list`/`list_chunks` (errgroup replaces chunked pools; CONCURRENCY.tsv), `check_python_version`, `pywintypes_*` |
| `Kathara/types.py` | PORT | `settings/types.go` (`SharedCollisionDomains` 1/2/3 + `ToString`). Deviation D-2: lands in `settings`, not `kathara` (§7) |
| `Kathara/exceptions.py` | PORT\* | `kerrors/errors.go` + `kerrors/codes.go` (§0.2 #6), aliased in `kathara/errors.go`. 33 classes 1:1 (incl. RESERVED-DEAD `Test`/`MachineSignatureNotFound` — codes reserved, never ported) + user-reachable builtins with stable codes + `InternalError` bucket (ruling; `docs/port/ERROR_CODES.md` is the frozen table). The `InvalidImageArchitecture` is-a `ValueError` quirk is preserved on the Python-client side (`ERROR_CODES.md` §1.1/§4) |
| `Kathara/decorators.py` | DEFERRED | `@privileged` is a **no-op in 1.0** (ruling): call sites call the method directly; no Go artifact. Returns post-1.0 with `PrivilegeHandler` as an explicit `runPrivileged(func() error) error` |
| `Kathara/strings.py` | PORT\* | `cmd/kathara/root.go`: command Short-descriptions verbatim, help row order = the `strings` dict insertion order (ORDERING.tsv); cobra help template pinned to it. `linfo` stays listed (stub, ruling) |
| `Kathara/version.py` | PORT | `internal/util/version.go` (`Parse` tuple semantics incl. prefix-is-smaller, `LessThan`; **not** a semver lib). `CURRENT_VERSION` → ldflags-injected in `cmd/kathara` |
| `Kathara/auth/PrivilegeHandler.py` | DEFERRED | §0.3 lab.ext bundle. **Carve-out (ruling):** the entrypoint's drop-effective-privileges-at-startup is ported → `cmd/kathara/privileges_linux.go` (+ `privileges_other.go` no-op), for setuid/setgid-docker installs. Ref-counted raise/drop machinery stays deferred |
| `Kathara/os/Networking.py` | DEFERRED | §0.3 lab.ext bundle, **except carve-out (ruling):** `get_iptables_version` → `backend/docker/iptables_linux.go` (binary lookup order `which` → `/sbin` → `/usr/sbin`, `FileNotFound` code, `--version` exec, TrimSpace) because `DockerPlugin._xtables_lock_mount` needs it on the core 1.0 path (SYNTHESIS C-3) |
| `Kathara/webhooks/DockerHubApi.py` | DEFERRED | §0.3. No Go artifact; settings TUI ships without image autocomplete (OQ-12a consequence, recorded in DIVERGENCES.md) |
| `Kathara/webhooks/GitHubApi.py` | DEFERRED | §0.3. No Go artifact; `Setting.check()` keeps writing `last_checked` (schema frozen §0.4) but performs no network call |
| `Kathara/strings.py`… `__init__` files | — | see §2.10 |

### 2.2 `model/` (5 files)

| Python | Disposition | Go target |
|---|---|---|
| `model/Lab.py` | PORT\* | `model/lab.go` (name-wins hash recompute, `general_options`, machine/link insertion-order maps backed by ordered slices per ORDERING.tsv; fs via `vfs.FS`) |
| `model/Machine.py` | PORT\* | `model/machine.go` (§0.2 #4 ordered `[]Interface` with tombstone slots — OQ-2 pinned in NILABILITY.tsv; `check()` re-sort + contiguity) + `model/meta.go` (§0.2 #5 typed `Meta` with pointer tri-states + **`Extras map[string]any`** ruling; accepts `rx` volume mode per SYNTHESIS C-6) + `model/pack.go` (`PackData`, sorted walk per canonical-order ruling, content-level golden comparison) |
| `model/Link.py` | PORT | `model/link.go` |
| `model/Interface.py` | PORT\* | `model/interface.go` (`Number`, `Link`, `MAC string` — explicit MAC pass-through only; no derivation, per MAC ruling) |
| `model/ExternalLink.py` | DEFERRED | §0.3 lab.ext. Post-1.0: `labfile/ext.go` + `netns/`. API references raise `FeatureNotAvailable` |

### 2.3 `foundation/model/` (2 files)

| Python | Disposition | Go target |
|---|---|---|
| `foundation/model/FilesystemMixin.py` | PORT\* | `vfs/` free functions (§0.2 #9): `CreateFileFromString/Path/Stream`, `UpdateFileFromString`, `WriteLineBefore/After`, `DeleteLine`, `CopyDirectory`, `Exists`, … operating on `vfs.FS`. "No filesystem set" → `Invocation` code parity |
| `foundation/model/LabFilesystemMixin.py` | PORT\* | `vfs/` free functions + thin `model.Lab` methods (`CreateStartupFileFromList`, `CreateShutdownFile…`) so the §7 client surface maps 1:1 |

### 2.4 `parser/` (5 files)

| Python | Disposition | Go target |
|---|---|---|
| `parser/netkit/LabParser.py` | PORT | `labfile/labconf.go` — hand-rolled quote/backreference logic reproducing comment-swallowing and quote-mismatch behavior exactly (OQ-14b); Unicode `\w`/`int()` decisions per Layer B freeze; only caller passing explicit `machine_iface_number` |
| `parser/netkit/DepParser.py` | PORT | `labfile/dep.go` (single-space grammar, duplicate-LHS overwrite, phantom `''` dep) |
| `parser/netkit/FolderParser.py` | PORT\* | `labfile/folder.go` — glob order **canonically sorted** (ruling on OQ-15a; recorded divergence: Python order was filesystem-dependent), reserved names silently skipped |
| `parser/netkit/OptionParser.py` | PORT | `labfile/options.go` — one-shot parse into `model.Meta` (+`Extras` for unknown keys, ruling) replacing the fifteen re-parsing accessors (§0.2 #5) |
| `parser/netkit/ExtParser.py` | DEFERRED | **Stub** `labfile/ext.go`: detecting `lab.ext` returns `FeatureNotAvailable` with the "not supported in this release, use 3.8.x" message (spec §0.3, ruling) |

### 2.5 `trdparty/` (28 files)

| Python | Disposition | Go target |
|---|---|---|
| `trdparty/depgen/depgen.py` | PORT | `labfile/depgen.go` (levels ascending, insertion order within level — deterministic-but-fragile order preserved, ORDERING.tsv; cycle → `MachineDependency` code) |
| `trdparty/libtmux/tmux.py` | REPLACED | §0.2 #2: `term/tmux.go` shells out to the `tmux` binary (documented CLI; never parses tmux internals). The vendored client library is not ported |
| `trdparty/nsenter/nsenter.py` | DEFERRED | §0.3 lab.ext bundle → post-1.0 `netns/` (Linux build tag, `containernetworking/plugins/pkg/ns` pattern) |
| `trdparty/strtobool/strtobool.py` | PORT | `internal/util/strtobool.go` (exact accepted-token set; `ValueError` → `InternalError`-bucketed parse error surfaced through `MachineOption` at the meta boundary) |
| `trdparty/consolemenu/**` (24 files: `console_menu.py`, `menu_component.py`, `menu_formatter.py`, `multiselect_menu.py`, `prompt_utils.py`, `screen.py`, `selection_menu.py`, `version.py`, `format/{menu_borders,menu_margins,menu_padding,menu_style}.py`, `items/{command_item,external_item,function_item,selection_item,submenu_item}.py`, `validators/{base,regex,url}.py`, 4 `__init__.py`) | REPLACED | §0.2 #1: vendored curses menu deleted; `kathara settings` becomes a bubbletea form in `cmd/kathara/settings_tui.go` over `settings/` validation. Spec §1 counts this as −1,295 SLOC not ported |

### 2.6 `setting/`, `foundation/setting/`, `validator/` (8 files)

| Python | Disposition | Go target |
|---|---|---|
| `setting/Setting.py` | REBUILT/PORT\* | `settings/settings.go` (§0.2 #1 rebuild, but path/schema/serialization frozen §0.4: one-space indent, no trailing newline, 12-key literal order + addon keys, chmod 600 + sudo-aware chown, unknown keys dropped on save, `shared_cds` as int, `last_checked` seeded `now − ONE_WEEK`); `settings/check.go` (`Setting.check` minus the deferred GitHub call, side-effect save preserved) |
| `setting/addon/DockerSettingsAddon.py` | PORT | `settings/addon_docker.go` |
| `setting/addon/KubernetesSettingsAddon.py` | PORT | `settings/addon_kubernetes.go` |
| `foundation/setting/SettingsAddon.py` | PORT\* | `settings/addon.go` (addon interface + key merge semantics) |
| `foundation/setting/SettingsAddonFactory.py` | REPLACED | §0.2 #7: explicit addon map `{"docker": …, "kubernetes": …}` in `settings/addon.go`; declared order preserved (C-5) |
| `validator/DockerConfigJsonValidator.py` | PORT | `settings/validate.go` (§3.2: validation runs on both `config set` and the TUI) |
| `validator/ImageValidator.py` | PORT | `settings/validate.go` |
| `validator/TerminalValidator.py` | PORT | `settings/validate.go` |

### 2.7 `event/`, `foundation/factory/`, `foundation/manager/`, `manager/Kathara.py`, `foundation/cli/` (18 files)

| Python | Disposition | Go target |
|---|---|---|
| `event/EventDispatcher.py` | PORT\* | `event/dispatcher.go` — typed handlers instead of `getattr` dispatch (§3.1 row 13); goroutine-safe (backends dispatch from worker goroutines, SYNTHESIS §1.8); stable event-name catalog and payload kwargs; per-backend payload variance kept as documented struct fields (Docker: machine object/API object; K8s: name string) |
| `foundation/factory/Factory.py` | REPLACED | §0.2 #7: reflection deleted. Manager registry → `kathara/registry.go`; settings addons → `settings/addon.go`; commands → cobra registration in `cmd/kathara` |
| `foundation/manager/IManager.py` | PORT\* | `kathara/manager.go` — the `Manager` interface, `context.Context` first parameter on every operation (§0.2 #11); lab-identifier triple semantics and filter truthiness/`is-not-None` fault line preserved per SYNTHESIS §1.7; stats methods reduced to inventory (§0.3 partial exception) |
| `foundation/manager/ManagerFactory.py` | REPLACED | §0.2 #7: `kathara/registry.go` (`Register(name, factory)`, declared order docker → kubernetes; `AvailableManagers()` keeps listing order, C-5) |
| `foundation/manager/stats/IMachineStats.py` | PORT\* | `kathara/stats.go`: `MachineStats` inventory struct (`name`, `container_name`, `status`, `image`, `user`, `network_scenario_id`); `update()` resource sampling DEFERRED → sampling APIs return `FeatureNotAvailable` (ruling) |
| `foundation/manager/stats/ILinkStats.py` | PORT\* | `kathara/stats.go`: `LinkStats` inventory struct; sampling deferred as above |
| `foundation/manager/exec_stream/IExecStream.py` | PORT\* | `kathara/execstream.go`: `ExecStream` interface — `Next() ([]byte, []byte, error)` returning `io.EOF` at end (maps StopIteration), `ExitCode() int` valid only after exhaustion; feeds §5.2 jsonl envelope |
| `foundation/manager/terminal/core/ITerminalSession.py` | PORT\* | `kathara/tty.go`: `TTYSession` interface (`Read`, `Write`, `Resize(cols, rows)`, `Close`) — the transport contract backends implement and `term` consumes |
| `foundation/manager/terminal/core/IConsoleAdapter.py` | REBUILT | §0.2 #2 → `term/console_unix.go` / `term/console_windows.go` (x/term raw mode + SIGWINCH; ConPTY/VT input on Windows) |
| `foundation/manager/terminal/core/TerminalRunner.py` | REBUILT | §0.2 #2 → `term/runner.go` (goroutines + done channel replacing asyncio; preserves: initial resize emission on Unix, EOF-on-empty-read, swallow-and-close pump errors, raw-mode restore on all exits — spec §12 risk 6) |
| `foundation/manager/terminal/console/UnixConsoleAdapter.py` | REBUILT | `term/console_unix.go` |
| `foundation/manager/terminal/console/WindowsConsoleAdapter.py` | REBUILT | `term/console_windows.go` (ConPTY rebuild; the NUL-per-chunk / 4096-boundary / no-initial-resize quirks are recorded divergences per OQ-19, not bug-compat requirements) |
| `manager/Kathara.py` | PORT\* | `kathara/client.go` (§0.2 #10/#11): singleton → constructed `*Client`; same method surface as the facade; manager-construction side effects preserved per OQ-16 (backends own them) |
| `foundation/cli/CliArgs.py` | COLLAPSED | cobra's bound flag values replace the parsed-args singleton; no Go artifact |
| `foundation/cli/command/Command.py` | PORT\* | `cmd/kathara/command.go` (shared helpers; `_load_custom_configuration` per-lab `kathara.conf` reload for connect/exec/lclean/lconfig/linfo/lstart) |
| `foundation/cli/command/CommandFactory.py` | REPLACED | §0.2 #7: cobra command tree in `cmd/kathara/root.go` |
| `foundation/cli/ui/setting/OptionsHandler.py` | REPLACED | §0.2 #1 (settings TUI rebuild) |
| `foundation/cli/ui/setting/OptionsHandlerFactory.py` | REPLACED | §0.2 #1 |

### 2.8 `manager/docker/` and `manager/kubernetes/` (24 non-init files)

| Python | Disposition | Go target |
|---|---|---|
| `manager/docker/DockerManager.py` | PORT\* | `backend/docker/manager.go` (implements `kathara.Manager`; §0.2 #11 contexts; errgroup + bounded semaphore per CONCURRENCY.tsv rows) |
| `manager/docker/DockerMachine.py` | PORT | `backend/docker/machine.go` (container naming incl. the dead-code hash quirk, labels, startup/shutdown command contract SYNTHESIS §1.10, `/tmp/EOS` wait, hosthome/shared mounts via `internal/util` user home) |
| `manager/docker/DockerLink.py` | PORT\* | `backend/docker/link.go` (network naming per `shared_cds`, DriverOpts `kathara.iface`/`kathara.link`/`kathara.mac_addr`; endpoint-sysctls join **canonically sorted** per ruling on OQ-8; engine-version forks). External-CD paths (lab.ext) DEFERRED → `FeatureNotAvailable` |
| `manager/docker/DockerImage.py` | PORT | `backend/docker/image.go` (pull/update policies, arch compatibility incl. macOS `amd64`-under-Rosetta acceptance) |
| `manager/docker/DockerPlugin.py` | PORT | `backend/docker/plugin.go` + `iptables_linux.go` (carve-out ruling; xtables lock mount Linux+bridge only) |
| `manager/docker/exec_stream/DockerExecStream.py` | PORT\* | `backend/docker/execstream.go` (demux `(stdout|nil, stderr|nil)` chunk parity, exit code after exhaustion, `MachineBinaryError` regex in non-stream mode only) |
| `manager/docker/stats/DockerMachineStats.py` | PORT\*/DEFERRED | `backend/docker/stats.go`: inventory fields ported (spec §0.3 partial exception, container-name keying preserved per OQ-22); resource sampling DEFERRED → `FeatureNotAvailable` |
| `manager/docker/stats/DockerLinkStats.py` | PORT\*/DEFERRED | `backend/docker/stats.go`: inventory only; sampling deferred (the shared-mode label KeyError latent bug goes to DIVERGENCES.md, unreachable in 1.0) |
| `manager/docker/terminal/DockerTTYTerminal.py` | REBUILT | transport → `backend/docker/tty_unix.go` (attach hijack implementing `kathara.TTYSession`); UI side → `term` (§0.2 #2) |
| `manager/docker/terminal/DockerNPipeTerminal.py` | REBUILT | `backend/docker/tty_windows.go` (npipe attach via docker SDK/go-winio) |
| `manager/docker/terminal/session/DockerTTYTerminalSession.py` | REBUILT | folded into `backend/docker/tty_unix.go` |
| `manager/docker/terminal/session/DockerNPipeTerminalSession.py` | REBUILT | folded into `backend/docker/tty_windows.go` |
| `manager/kubernetes/KubernetesManager.py` | PORT\* | `backend/kubernetes/manager.go` (lab-hash lowercasing at every entry point, incl. the shared-`lab.hash` mutation; 180 s watchdog → context deadline per OQ-10 ruling path, preserving the `kubectl -n {hash} get pods` message) |
| `manager/kubernetes/KubernetesMachine.py` | PORT | `backend/kubernetes/machine.go` (postStart lifecycle script incl. sysctl prologue quirks, `real_name` meta via `Extras`) |
| `manager/kubernetes/KubernetesLink.py` | PORT\* | `backend/kubernetes/link.go` (VXLAN VNI allocation: the `multiprocessing.Manager` process becomes a mutex-guarded map per CONCURRENCY.tsv) |
| `manager/kubernetes/KubernetesConfig.py` | PORT | `backend/kubernetes/config.go` (`get_cluster_user` seeds VNI derivation) |
| `manager/kubernetes/KubernetesConfigMap.py` | PORT | `backend/kubernetes/configmap.go` (3 MiB limit → `KubernetesConfigMap` code) |
| `manager/kubernetes/KubernetesNamespace.py` | PORT | `backend/kubernetes/namespace.go` |
| `manager/kubernetes/KubernetesSecret.py` | PORT | `backend/kubernetes/secret.go` (`docker_config_json` b64 verbatim into Secret data) |
| `manager/kubernetes/exec_stream/KubernetesExecStream.py` | PORT\* | `backend/kubernetes/execstream.go` (`remotecommand`; `wait` ignored with warning parity) |
| `manager/kubernetes/stats/KubernetesMachineStats.py` | PORT\*/DEFERRED | `backend/kubernetes/stats.go`: inventory only; sampling deferred |
| `manager/kubernetes/stats/KubernetesLinkStats.py` | PORT\*/DEFERRED | `backend/kubernetes/stats.go`: inventory only; sampling deferred |
| `manager/kubernetes/terminal/KubernetesWSTerminal.py` | REBUILT | `backend/kubernetes/tty.go` (exec websocket as `kathara.TTYSession`); UI side → `term` |
| `manager/kubernetes/terminal/session/KubernetesWSTerminalSession.py` | REBUILT | folded into `backend/kubernetes/tty.go` |

### 2.9 `cli/` (26 non-init files)

| Python | Disposition | Go target |
|---|---|---|
| `cli/command/LstartCommand.py` | PORT\* | `cmd/kathara/lstart.go` (+ `--format`, `--from-archive` per §5.4; lab.ext detection errors `FeatureNotAvailable`) |
| `cli/command/LcleanCommand.py` | PORT\* | `cmd/kathara/lclean.go` |
| `cli/command/LrestartCommand.py` | PORT\* | `cmd/kathara/lrestart.go` (flag drift vs lstart — `--xterm`, no `--print` — reproduced per OQ-11/OQ-18 unless CLI_SURFACE.md rules otherwise) |
| `cli/command/WipeCommand.py` | PORT\* | `cmd/kathara/wipe.go` |
| `cli/command/ListCommand.py` | PORT\* | `cmd/kathara/list.go` (inventory stats only) |
| `cli/command/ConnectCommand.py` | PORT\* | `cmd/kathara/connect.go` (attaches via `term`; human-only) |
| `cli/command/CheckCommand.py` | PORT\* | `cmd/kathara/check.go` (`kathara_test` magic lab) |
| `cli/command/ExecCommand.py` | PORT\* | `cmd/kathara/exec.go` (jsonl streaming §5.2; exit code = in-container command's; singleton-command list→string canonicalization per JSON contract OQ-7) |
| `cli/command/LconfigCommand.py` | PORT\* | `cmd/kathara/lconfig.go` |
| `cli/command/LinfoCommand.py` | DEFERRED | **Stub** `cmd/kathara/linfo.go`: registered command returning `FeatureNotAvailable` (ruling on OQ-13; keeps help-text and Ctrl-C-whitelist parity) |
| `cli/command/SettingsCommand.py` | REBUILT | §0.2 #1: `cmd/kathara/settings.go` + `settings_tui.go` (bubbletea form) + new `cmd/kathara/config.go` (`config get/set/list/reset`) |
| `cli/command/VstartCommand.py` | COLLAPSED | §0.2 #3: `cmd/kathara/vstart.go` — thin sugar building a one-device in-memory Lab (`kathara_vlab`), same code path as lstart; byte-identical goldens are the merge gate |
| `cli/command/VcleanCommand.py` | COLLAPSED | §0.2 #3: `cmd/kathara/vclean.go` |
| `cli/command/VconfigCommand.py` | COLLAPSED | §0.2 #3: `cmd/kathara/vconfig.go` (api-object plumbing pattern unified per OQ-11, goldens byte-identical) |
| `cli/ui/utils.py` | PORT\* (split) | three targets: rich panels/tables/`confirmation_prompt` → `internal/cliout/render.go`; argparse type-validators (`alphanumeric`, `interface_cd_mac`, `cd_mac`, `volume`) → `cmd/kathara/flags.go`; `open_machine_terminal` + per-OS connect closures → `term/external_{linux,darwin,windows}.go` |
| `cli/ui/event/register.py` | PORT\* | `cmd/kathara/events.go` — explicit handler registration, order preserved (progress bar advances before terminal opens, SYNTHESIS §1.4) |
| `cli/ui/event/HandleProgressBar.py` | PORT\* | `internal/cliout/progress.go` (stdout in human mode — Python parity, `JSON_CLI_CONTRACT.md` A1; not rendered at all in json/jsonl) |
| `cli/ui/event/HandleDockerImagePull.py` | PORT\* | `internal/cliout/imagepull.go` |
| `cli/ui/event/UpdateDockerImage.py` | PORT\* | `internal/cliout/imagepull.go` (update-policy prompt) |
| `cli/ui/event/MountDevicesVolumes.py` | PORT\* | `internal/cliout/prompts.go` |
| `cli/ui/event/HandleMachineTerminal.py` | PORT\* | wiring in `cmd/kathara/events.go` invoking `term` (kept out of `internal/cliout` so renderers do not import `term`) |
| `cli/ui/setting/SettingsMenuFactory.py` | REPLACED | §0.2 #1 |
| `cli/ui/setting/CommonOptionsHandler.py` | REPLACED | §0.2 #1 (image autocomplete dropped with webhooks deferral) |
| `cli/ui/setting/DockerOptionsHandler.py` | REPLACED | §0.2 #1 |
| `cli/ui/setting/KubernetesOptionsHandler.py` | REPLACED | §0.2 #1 |
| `cli/ui/setting/utils.py` | REPLACED | §0.2 #1 |

### 2.10 `__init__.py` package markers (50 files)

All 50 `__init__.py` files are **COLLAPSED** — Go packages need no marker files. Four are
non-empty: `Kathara/__init__.py` (a pyfilesystem2 deprecation-warning filter; obsolete — `vfs`
replaces pyfilesystem2 per §0.2 #9) and three `trdparty/consolemenu` re-export stubs (REPLACED
with consolemenu, §0.2 #1). The full list, for the 173-file audit:

`Kathara/`, `auth/`, `cli/`, `cli/command/`, `cli/ui/`, `cli/ui/event/`, `cli/ui/setting/`,
`event/`, `foundation/`, `foundation/cli/`, `foundation/cli/command/`, `foundation/cli/ui/`,
`foundation/cli/ui/setting/`, `foundation/factory/`, `foundation/manager/`,
`foundation/manager/exec_stream/`, `foundation/manager/stats/`, `foundation/manager/terminal/`,
`foundation/manager/terminal/console/`, `foundation/manager/terminal/core/`,
`foundation/model/`, `foundation/setting/`, `manager/`, `manager/docker/`,
`manager/docker/exec_stream/`, `manager/docker/stats/`, `manager/docker/terminal/`,
`manager/docker/terminal/session/`, `manager/kubernetes/`, `manager/kubernetes/exec_stream/`,
`manager/kubernetes/stats/`, `manager/kubernetes/terminal/`,
`manager/kubernetes/terminal/session/`, `model/`, `os/`, `parser/`, `parser/netkit/`,
`setting/`, `setting/addon/`, `trdparty/`, `trdparty/consolemenu/`,
`trdparty/consolemenu/format/`, `trdparty/consolemenu/items/`,
`trdparty/consolemenu/validators/`, `trdparty/depgen/`, `trdparty/libtmux/`,
`trdparty/nsenter/`, `trdparty/strtobool/`, `validator/`, `webhooks/`.

**Audit total: 123 modules (§2.1–2.9) + 50 markers = 173 files. ✓**

---

## 3. Where the 12 sanctioned §0.2 changes land

| # | Change | Landing package(s) | Design pointer |
|---|---|---|---|
| 1 | Settings rebuilt | `settings/` (load/save/validate/check, addons, `validate.go`) + `cmd/kathara/config.go` (scriptable interface) + `cmd/kathara/settings_tui.go` (bubbletea form) | spec §3.2; schema/path/serialization frozen; validation shared by both entry paths |
| 2 | Terminal rebuilt | `term/` (mux.go multiplexer default, tmux.go exec-driven backend, external_*.go opt-in adapters, runner.go, console_*.go, pty_*.go); transports in `backend/*/tty*.go` implementing `kathara.TTYSession` | spec §3.3; one session per scenario, attach-don't-clobber; tmux internals never parsed |
| 3 | vstart family as sugar | `cmd/kathara/vstart.go`, `vclean.go`, `vconfig.go` | spec §3.4; one-device in-memory `kathara_vlab` Lab through the lstart/lclean/lconfig path; byte-identical goldens gate merge |
| 4 | Ordered interface slice | `model/machine.go`, `model/interface.go` | spec §4.1; tombstone representation pinned by NILABILITY.tsv (OQ-2); reviewer rule: no map `range` reaching container/network/address |
| 5 | Typed `Meta` | `model/meta.go` (struct + pointer tri-states + `Extras` map, ruling) parsed once in `labfile/options.go` | spec §4.2 |
| 6 | Errors with stable codes | `kerrors/` (+ aliases in `kathara/errors.go`); frozen table `docs/port/ERROR_CODES.md` | spec §4.3; builtins ruling: user-reachable builtins get codes, rest `InternalError` |
| 7 | Reflection → registries | `kathara/registry.go` (managers, declared order), `settings/addon.go` (addons), cobra tree in `cmd/kathara/root.go` (commands) | deletes `Factory`, `class_for_name`, `CommandFactory`, `ManagerFactory`, `SettingsAddonFactory` |
| 8 | Separately importable backends | `backend/docker/`, `backend/kubernetes/`; selection in `cmd/kathara/backends_all.go` / `backends_dockeronly.go` (`//go:build nok8s`) | a Docker-only build excludes `client-go` |
| 9 | `FilesystemMixin` → `vfs` | `vfs/` (FS interface + free functions) | spec §6; `OSDir` for path labs, `Memory` for named labs |
| 10 | Singleton → `*Client` | `kathara/client.go` | spec §7; two backends constructible in one process; Setting singleton reads become injected `model.Defaults`/config params (OQ-4) |
| 11 | `context.Context` everywhere | `kathara/manager.go` signatures; honored in `backend/*` (errgroup, watch deadlines) and `cmd/kathara` (signal-to-context wiring, Ctrl-C = exit 0 with warning parity ruling) | spec §0.2 #11, OQ-10 |
| 12 | JSON CLI output mode | `internal/cliout/` (human/json/jsonl renderers, error envelope with `code`) + `--format` wiring in `cmd/kathara` | spec §5; stdout protocol-only in json/jsonl; contract doc: `docs/port/JSON_CLI_CONTRACT.md` |

---

## 4. Platform strategy (SYNTHESIS §1.11)

Convention: `_linux.go` / `_darwin.go` / `_windows.go` suffixes where the three OSes diverge;
`_unix.go` with `//go:build unix` where macOS == Linux; `_other.go` for no-op fallbacks.
`get_architecture` parity note: Python reads the **kernel's** arch (`platform.machine()`), not
the binary's — Go uses uname/`GetNativeSystemInfo`, **not** `runtime.GOARCH` (OQ-20).

| Package | File | Contents |
|---|---|---|
| `internal/util` | `user_linux.go` | SUDO-aware passwd identity (`SUDO_UID`), passwd-db home |
| | `user_darwin.go` | mac ≠ linux: home via `$HOME`/expanduser (not passwd), SUDO-aware uid/gid for the other getters |
| | `user_windows.go` | `getpass.getuser` equivalent; uid/gid `(nil, nil)` |
| | `admin_unix.go` / `admin_windows.go` | `getuid()==0` (real uid) / `IsUserAnAdmin` |
| | `perms_unix.go` / `perms_windows.go` | `unix.Access` r/w/x / `CreateFileW` + `FILE_FLAG_BACKUP_SEMANTICS` probe; fixed "read (r)"/"write (w)"/"execute (x)" order |
| | `input_unix.go` / `input_windows.go` | select-based non-consuming poll / kbhit+getch **Enter-consuming** poll — asymmetry preserved |
| | `arch_unix.go` / `arch_windows.go` | uname machine → Docker arch map (incl. armv7/armv6) / `GetNativeSystemInfo` |
| | `wsl_linux.go` / `wsl_other.go` | uname release contains "microsoft" / false |
| `settings` | `file_unix.go` / `file_windows.go` | chmod 600 + sudo-aware chown on save / no-op |
| `term` | `pty_unix.go` / `pty_windows.go` | `creack/pty` / ConPTY (`x/sys/windows`) |
| | `console_unix.go` / `console_windows.go` | raw mode + SIGWINCH + initial-resize emission / console modes + VT input; ConPTY rebuild retires the NUL/chunk-boundary/no-initial-resize quirks (recorded divergences, OQ-19) |
| | `external_linux.go` / `external_darwin.go` / `external_windows.go` | xterm/gnome-terminal/`$TERMINAL` spawn / `osascript` (replaces appscript) / `cmd start` — quoting convention from `util.GetExecutablePath` preserved |
| `backend/docker` | `iptables_linux.go` / `iptables_other.go` | carve-out `get_iptables_version` + xtables lock mount / no-op (never called) |
| | `tty_unix.go` / `tty_windows.go` | attach over hijacked conn / npipe (go-winio via docker SDK) |
| `backend/kubernetes` | — | none (client-go is platform-neutral) |
| `cmd/kathara` | `privileges_linux.go` / `privileges_other.go` | carve-out startup drop-effective-privileges (setuid/setgid-docker installs) / no-op (mac and Windows are no-ops in Python too) |
| | `backends_all.go` / `backends_dockeronly.go` | not OS tags: `//go:build !nok8s` / `nok8s` backend selection (§0.2 #8) |

Image-arch acceptance (mac additionally accepts `amd64` under Rosetta) is an inline
`runtime.GOOS` check in `backend/docker/image.go`, not a tagged file.

---

## 5. External dependencies per package (verified 2026-08-05 via Go module proxy)

Versions marked **pinned** were verified with `go list -m -versions`; TBD items carry the reason.

| Package | Module | Version | Note |
|---|---|---|---|
| `cmd/kathara` | `github.com/spf13/cobra` | **v1.10.2** pinned | + `spf13/pflag` v1.0.10 (transitive) |
| | `github.com/charmbracelet/bubbletea` | **v1.3.10** pinned | v1 line chosen; bubbletea/v2 (v2.0.8) exists but the v1 ecosystem is the mature one — do not mix v1/v2 Charm libs |
| | `github.com/charmbracelet/lipgloss` | **v1.1.0** pinned | `rich` replacement (panels/tables) |
| | `github.com/charmbracelet/bubbles` | **v0.21.1** pinned | v1-line companion widgets (form inputs, viewport). v1.0.0 exists but targets the v2 line — verify before any bump |
| `term` | `github.com/charmbracelet/bubbletea` | v1.3.10 | multiplexer UI |
| | `github.com/charmbracelet/bubbles` | v0.21.1 | tabs/viewport/scrollback |
| | `github.com/charmbracelet/lipgloss` | v1.1.0 | |
| | `github.com/creack/pty` | **v1.1.24** pinned | Unix PTY |
| | `golang.org/x/term` | **v0.45.0** pinned | raw mode, size |
| | `golang.org/x/sys` | **v0.47.0** pinned | ConPTY, SIGWINCH plumbing |
| `backend/docker` | `github.com/docker/docker` | **v28.5.2+incompatible** pinned | first-party client (`/client`, `/api/types`) |
| | `github.com/Microsoft/go-winio` | **v0.6.2** pinned | npipe (Windows daemon + tty); also transitive of docker |
| | `github.com/distribution/reference` | v0.6.0 | image-name parsing (transitive of docker; listed because used directly) |
| | `github.com/opencontainers/image-spec` | v1.1.1 | transitive of docker |
| | `golang.org/x/sync` | **v0.22.0** pinned | errgroup + semaphore (CONCURRENCY.tsv target) |
| `backend/kubernetes` | `k8s.io/client-go` | **v0.36.3** pinned | pin the triple together; v0.37 is still beta |
| | `k8s.io/api` | **v0.36.3** pinned | |
| | `k8s.io/apimachinery` | **v0.36.3** pinned | |
| | `golang.org/x/sync` | v0.22.0 | |
| `internal/util` | `golang.org/x/sys` | v0.47.0 | uname, Access, Win32 |
| | `golang.org/x/text` | **v0.40.0** pinned | NFKD for `slug` |
| | `github.com/saintfish/chardet` | **v0.0.0-20230101081208-5e3ef4b5456d** pinned | binaryornot-equivalent sniff (decodability part; NUL-in-first-1024 check is hand-rolled) |
| `internal/cliout` | `github.com/charmbracelet/lipgloss` | v1.1.0 | tables/panels/progress |
| `kerrors`, `vfs`, `model`, `labfile`, `settings`, `event`, `kathara` | — | — | **stdlib only** (this is load-bearing: the §7 client and lab-checker depend on these layers having no heavyweight deps; `regexp` is sufficient — no lookaheads/backrefs exist in scope except lab.conf's quote backreference, which is hand-rolled in `labfile`, spec §6) |
| `tools/goldenharness` | `github.com/docker/docker` | v28.5.2+incompatible | `docker inspect` snapshots; **separate `go.mod`** so oracle deps never enter the product module |

TBD (decide at Phase 2 spike, not load-bearing for the graph): a YAML dep for kubeconfig is
**not** needed (`client-go/tools/clientcmd` handles it); `github.com/moby/term` (v0.5.2) only if
the docker SDK's hijack helpers prove insufficient for `backend/docker/tty_unix.go`.

Post-1.0 (deferred with lab.ext, listed for completeness, **not** in 1.0 `go.mod`):
`vishvananda/netlink`, `containernetworking/plugins` (`pkg/ns`).

---

## 6. The §7 Python client: module layout and shared conformance surfaces

### 6.1 Layout (spec §7.1, unchanged; lives at `python/` in this repo)

```
python/kathara/
├── __init__.py
├── _bin.py              locates the bundled Go binary
├── _proc.py             subprocess runner, JSON decode, code→exception re-raise map
├── exceptions.py        the same exception classes, unchanged names/fields
│                        (incl. MachineBinaryError.binary/.machine_name,
│                        InvalidImageArchitectureError.image_name/.arch)
├── model/
│   ├── Lab.py           pure Python, memory FS preserved
│   ├── Machine.py
│   ├── Link.py
│   └── Interface.py
├── parser/netkit/
│   └── LabParser.py     stays Python; kathara-lab-checker imports it directly
├── manager/
│   └── Kathara.py       same facade method names, subprocess-backed
├── setting/Setting.py   reads/writes the same config file (frozen schema)
└── bin/kathara          the bundled Go binary (per-platform wheel)
```

Not a Go package; zero edges in §1. `deploy_lab` pipes a tar to
`kathara lstart --from-archive - --name <name> --format json`; `exec` consumes
`--format jsonl`. Setting-backed model defaults: the Python model reads the same
`kathara.conf` via its own `setting/Setting.py` (mirror of Go's injected `model.Defaults`);
the Layer B vectors are defined **settings-free** (below) so both resolutions are testable.

### 6.2 Conformance surface 1: Layer B parser vectors

- **Location:** `tools/goldenharness/vectors/labfile/` — one directory per vector: input files
  (`lab.conf`, `lab.dep`, folder layout, malformed variants) + `expected.json` (canonical model
  JSON: machines in insertion order, interfaces with numbers/CD names/explicit MACs, meta incl.
  `extras`, general options) or `expected_error.json` (stable error `code` + message template).
- **Canonical model JSON asserts raw parse output with defaults unresolved** (unset stays
  unset/null) so the vectors are independent of any settings file — this is what lets the same
  vectors bind the Go `labfile` package and the Python client's `LabParser` (OQ-4-safe).
- **Consumers, same CI job (spec §7.1/§9-B, risk 11):** `labfile/conformance_test.go` and
  `python/tests/test_labparser_vectors.py`. A vector change that greens one side and reds the
  other is a drift alarm, not a fixture update.

### 6.3 Conformance surface 2: the error-code table

- **Source of truth:** `docs/port/ERROR_CODES.md` (frozen Phase 0 contract; corrects and
  supersedes the draft `ERROR_CODES.tsv`): every Kathara exception class, the user-reachable
  builtins with stable codes, the `InternalError` bucket, `ConfirmationRequired`,
  `FeatureNotAvailable` (with `feature` field) for linfo / lab.ext / stats sampling / webhooks
  (ruling).
- **Consumers:** `kerrors/codes.go` (Go code ↔ error mapping) and `python/kathara/_proc.py`
  (code → exception class re-raise map, covering the four classes kathara-lab-checker catches
  by name). Both carry a verification test that walks the frozen table and fails on any missing
  or extra code, run in the same CI job.
- The JSON error envelope (`{"error":{"code":…,"message":…,…}}`) is specified in
  `docs/port/JSON_CLI_CONTRACT.md`; `message` carries the exact Python format-string output
  (typos included), with the ruling that nondeterministic set-order interpolations are emitted
  in canonical sorted order.

---

## 7. Deviations from spec §2 (each with justification)

| # | Deviation | Justification |
|---|---|---|
| D-1 | New leaf package `kerrors` holds the error taxonomy; `kathara/errors.go` re-exports aliases | spec §2 puts errors in `kathara`, but `kathara` must import `model` (Manager interface signatures) while `model` must raise taxonomy errors — a compile-blocking cycle; aliases keep the spec-§4.3 public surface byte-compatible |
| D-2 | `types.py` (`SharedCollisionDomains`) lands in `settings`, not `kathara/errors.go` (spec §3.1 row 1) | it is a frozen settings-schema enum consumed by `settings` and `backend/docker`; `kathara` sits above `settings` in the graph, so housing it there would invert an edge |
| D-3 | `internal/util` and `tools/goldenharness` added to the §2 tree | §3.1 row 2 already names `internal/util`; the harness is task-sanctioned and isolated as a separate Go module with zero production imports so the oracle cannot drift with the implementation |
| D-4 | Settings bubbletea form lives in `cmd/kathara/settings_tui.go`, not `settings/` | keeps `settings/` TTY-free and importable by the client-facing layers; §3.2's requirement stands — validation lives in `settings/` and runs on both paths |
| D-5 | Backend TTY transports live in `backend/*/tty*.go` implementing `kathara.TTYSession`; only UI lives in `term/` | the Python "terminal" classes conflate transport (attach socket / npipe / websocket) with rendering; the §0.2 #2 rebuild owns rendering, but transports are backend-SDK code and must live behind the same import boundary as the SDKs (§0.2 #8) |
| D-6 | `os/Networking.get_iptables_version` ported into `backend/docker/iptables_linux.go`, not an `os/`-shaped package | accepted carve-out ruling resolving SYNTHESIS C-3: the only 1.0 caller is `DockerPlugin`; a one-function package would outlive its purpose when lab.ext lands post-1.0 in `netns/` |
