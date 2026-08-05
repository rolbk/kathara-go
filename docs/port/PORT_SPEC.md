# Kathará: Python to Go Port Specification

**Status:** decisions locked, ready for execution
**Target:** feature parity in Go with a better architecture, plus a Python client package over a JSON CLI
**Audience:** the porting workflow (implementer, reviewer and fixer agents) and the human owner

---

## 0. Decisions

These are settled. Do not relitigate them mid-port.

| Decision | Value |
|---|---|
| Target language | Go |
| Scope | Feature parity, including Kubernetes/Megalos and all three OS platforms. Four items deferred to post-1.0, see §0.3 |
| Python API | **Not** an FFI binding. A pure-Python model layer plus a subprocess client over a JSON CLI. See §7 |
| Fidelity | **This is not a 1:1 transpile.** See §0.1 |

### 0.1 Fidelity policy: parity of behaviour, not of structure

The goal is a Go implementation that **behaves** like Kathará and is **built better** than Kathará. Several parts of the current codebase are bad in ways that a faithful port would preserve forever: a vendored curses library to draw one settings screen, terminal integration that shells out to external emulators, a single-device command path duplicating the lab path, dynamic-import reflection standing in for a registry. Reproducing those in Go would be a wasted opportunity.

**But the license to redesign is granted by this document, not taken by agents.**

- **§0.2 is a closed list** of sanctioned architecture changes. Implement those as specified.
- **Outside that list, port faithfully.** Same order of operations, same observable behaviour, same error taxonomy.
- If you believe something else should be redesigned, **stop and write it to `PROPOSED-DIVERGENCES.md`** with a one-paragraph rationale. Do not implement it. A human decides, possibly after 1.0.
- If you believe the Python has a bug, do not fix it. File it in `DIVERGENCES.md` and port the behaviour as-is. Bug fixes are separate commits with their own tests, after the port is green.

The reason for the boundary: unbounded redesign destroys reviewability, because reviewers can no longer diff the Go against the Python to check it. Bounded redesign gets the architectural benefit while keeping every unsanctioned file reviewable line by line.

**Consequence for reviewers.** For files touched by a §0.2 change, the review question is *"does this produce the same observable behaviour and conform to the sanctioned design?"*, and the golden tests are the arbiter. For every other file, the review question stays *"does this match the Python?"*, and the diff is the arbiter.

### 0.2 Sanctioned architecture changes (the closed list)

| # | Change | Detail |
|---|---|---|
| 1 | Settings subsystem rebuilt | §3.2. Deletes ~1,500 SLOC |
| 2 | Terminal integration rebuilt | §3.3. Built-in multiplexer by default, first-class tmux, external emulators as opt-in |
| 3 | `vstart` / `vclean` / `vconfig` reimplemented as sugar | §3.4. One device-creation path instead of two |
| 4 | `Machine.interfaces` becomes an ordered slice | §4.1. Required for correctness, not taste |
| 5 | `Meta` becomes a typed struct | §4.2. Parse once at load instead of on every accessor |
| 6 | Exceptions become Go errors with stable codes | §4.3 |
| 7 | `Factory` + `class_for_name` reflection becomes an explicit registry | Deletes reflection entirely |
| 8 | Backends become separately importable packages | §2. A Docker-only build excludes `client-go` |
| 9 | `FilesystemMixin` becomes a `vfs` package with free functions | §6. Instead of 15 methods hung on two types |
| 10 | Singleton manager becomes a constructed `*Client` | §7. Testable, supports two backends in one process |
| 11 | `context.Context` on every operation | New capability. Ctrl-C during a 50-device deploy currently orphans containers |
| 12 | JSON CLI output mode | §5. New public interface |

### 0.3 Deferred to post-1.0

Not cancelled. Ship 1.0 without them, add them afterwards against a proven core.

| Item | SLOC | Why deferred |
|---|---|---|
| Live resource stats (cpu, mem, net, pids sampling) | 255 | Streaming, time-sampled data whose golden tests are inherently flaky. Worst verification cost per line in the repo. **Partial exception below** |
| `linfo` command | 150 | Exists to display the above. `kathara list` covers inventory |
| Docker Hub / GitHub version-check webhooks | 79 | A startup network call announcing releases users get from apt or brew anyway. One less thing to hang behind a university proxy |
| `lab.ext` external links (`os/Networking`, `nsenter`, `auth/PrivilegeHandler`, `ExtParser`, `ExternalLink`) | 328 | Highest risk-per-line code in the repo: `setns`, `LockOSThread`, root escalation, intermittent failure. Cleanly separable behind a Linux build tag, which almost nothing else is |

**Partial exception on stats.** `get_machines_stats` must still return the **inventory** fields (`name`, `container_name`, `status`, `image`, `user`, `network_scenario_id`). Those come from the container listing that `kathara list` already needs, so they are nearly free, they are deterministic and cheap to test, and the official `getting-started` Python API tutorial ends with `print(next(get_machines_stats(...)))` as a liveness check. The deferred part is only the resource-sampling fields.

**Consequences to accept openly:**

- `lab.conf` files using `lab.ext` will error with a clear "not supported in this release, use 3.8.x" message rather than silently ignoring the file.
- **IXP Digital Twin cannot run against 1.0**, because it uses `ExternalLink`. It moves from a release gate to a post-1.0 gate. See §9 Layer D.

### 0.4 Non-goals

Performance optimization. New user-facing features. Changing the `lab.conf` format, the Docker label scheme, the config file path or schema, or the MAC derivation formula. Any of these silently breaks compatibility with running scenarios, the network plugin, or existing installs.

---

## 1. Ground truth: measured inventory

Measured from `KatharaFramework/Kathara` at v3.8.3 by AST analysis, excluding docstrings, comments and blank lines.

**`src/` is 19,487 raw lines but only 10,357 lines of code.** 4,683 lines (24%) are docstrings, 4,032 (21%) are blank. Any estimate quoting the 19k figure is wrong by roughly half.

| Module | Code | Docstrings | Raw | Files |
|---|---|---|---|---|
| `manager/docker` | 1,768 | 952 | 3,518 | 17 |
| `manager/kubernetes` | 1,521 | 810 | 3,105 | 17 |
| `cli/command` | 1,343 | 0 | 1,582 | 15 |
| `trdparty/consolemenu` | 1,295 | 851 | 2,752 | 24 |
| `cli/ui` | 1,288 | 84 | 1,704 | 15 |
| `model` | 628 | 470 | 1,471 | 6 |
| `foundation/manager` | 558 | 639 | 1,536 | 16 |
| root (`utils`, `exceptions`, `types`) | 494 | 20 | 767 | 7 |
| `setting` | 229 | 69 | 392 | 5 |
| `parser` | 176 | 48 | 311 | 7 |
| `foundation/model` | 140 | 179 | 420 | 3 |
| `manager` (facade) | 122 | 375 | 624 | 2 |
| `os` | 107 | 47 | 213 | 2 |
| `kathara.py` (entrypoint) | 106 | 0 | 136 | 1 |
| `foundation/cli` | 86 | 4 | 122 | 9 |
| `trdparty/nsenter` | 82 | 50 | 176 | 2 |
| `webhooks` | 79 | 15 | 120 | 3 |
| `auth` | 79 | 0 | 106 | 2 |
| `trdparty/depgen` | 63 | 24 | 113 | 2 |
| `trdparty/libtmux` | 60 | 0 | 83 | 2 |
| `event` | 40 | 37 | 104 | 2 |
| `validator` | 38 | 0 | 48 | 4 |
| `foundation/setting` | 26 | 0 | 35 | 3 |
| `foundation/factory` | 21 | 0 | 29 | 2 |
| `trdparty/strtobool` | 8 | 9 | 20 | 2 |
| **Total** | **10,357** | **4,683** | **19,487** | **173** |

**1.0 port surface:**

| | SLOC |
|---|---|
| Total source | 10,357 |
| less deferred (§0.3) | -812 |
| less `consolemenu`, replaced not ported | -1,295 |
| less `libtmux`, replaced by exec | -60 |
| less `vstart` family collapse (§3.4) | -250 |
| **To port** | **~7,940** |

Tests: 40 files, 13,965 raw lines, 10,980 code, **1,315 `mock.patch` calls**. See §9 for why these are not the oracle.

Code is concentrated: 12 files account for 4,308 SLOC, 42% of the total. `DockerMachine.py` (629), `KubernetesMachine.py` (621), `DockerManager.py` (458), `Machine.py` (399), `KubernetesManager.py` (375).

**Expected Go output:** ~17,500 SLOC ported, plus ~600 for settings and ~1,600 for terminals, so **~19,000-20,000 SLOC** (roughly 30k raw lines with doc comments and blanks), plus tests.

---

## 2. Target repository layout

```
github.com/KatharaFramework/kathara-go
├── cmd/kathara/             main, cobra root
├── kathara/                 Client, Manager interface, options, errors
├── model/                   Lab, Machine, Link, Interface, Meta
├── labfile/                 lab.conf, lab.dep, folder, meta parsers
├── vfs/                     filesystem abstraction (replaces pyfilesystem2)
├── settings/                config load/save/validate
├── event/                   typed event dispatcher
├── term/                    built-in multiplexer, tmux backend, external adapters
├── backend/
│   ├── docker/
│   └── kubernetes/
├── internal/cliout/         human / json / jsonl renderers
└── python/                  the Python client package (see §7)
```

Post-1.0 adds `netns/` (setns helpers, Linux build tag) and `labfile/ext.go` for external links.

**Declare the package graph acyclic before writing code.** Python hides cycles behind runtime imports and the `Factory` + `class_for_name` reflection; Go will not compile them. `model` must not import `backend`. `backend/*` imports `model` and `kathara`. `kathara` defines the `Manager` interface that `backend/*` implements, and holds the error taxonomy.

Backends are separate packages so a build can exclude `client-go`. In Python both SDKs are imported unconditionally and cost roughly a second of startup.

---

## 3. Component port map

### 3.1 Faithful ports (~6,000 SLOC)

Port mechanically, in this dependency order. Sanctioned changes from §0.2 apply where noted; otherwise match the Python.

| Order | Python | Go | Code | Notes |
|---|---|---|---|---|
| 1 | `exceptions.py`, `types.py` | `kathara/errors.go` | ~200 | §0.2 #6. Do this first, everything depends on it |
| 2 | `utils.py` | `internal/util` | 338 | Audit each function. Several are Python-specific and disappear (`class_for_name`, `chunk_list`) |
| 3 | `setting/` | `settings/` | 229 | Plus `foundation/setting` 26. Same file path and JSON schema |
| 4 | `parser/netkit/` | `labfile/` | 134 | LabParser, DepParser, FolderParser, OptionParser. ExtParser deferred. **Highest regression cost per line.** See §9 Layer B |
| 5 | `trdparty/depgen` | `labfile/depgen.go` | 63 | Topological sort with cycle detection |
| 6 | `model/` | `model/` | 610 | §0.2 #4 and #5 apply. `ExternalLink.py` deferred |
| 7 | `foundation/model/FilesystemMixin` | `vfs/` | 140 | §0.2 #9 |
| 8 | `foundation/manager/` | `kathara/manager.go` | 540 | Interface definitions, exec streams, terminal interfaces. Stats interfaces reduced to inventory fields |
| 9 | `manager/Kathara.py` | `kathara/client.go` | 122 | §0.2 #10 and #11 |
| 10 | `manager/docker/` | `backend/docker/` | 1,618 | The core. `DockerMachine` 629, `DockerManager` 458, `DockerLink` 208. Stats sampling deferred |
| 11 | `manager/kubernetes/` | `backend/kubernetes/` | 1,434 | `KubernetesMachine` 621, `KubernetesManager` 375, `KubernetesLink` 194. Stats sampling deferred |
| 12 | `foundation/factory/` | registry maps | 21 | §0.2 #7 |
| 13 | `event/` | `event/` | 40 | Typed handlers instead of `getattr` dispatch |
| 14 | `cli/command/` | `cmd/kathara/` | ~940 | §3.4 |
| 15 | `cli/ui/utils.py`, `cli/ui/event/` | `internal/cliout/`, `event/` | ~390 | Progress bars, image pull handling |

### 3.2 Rebuilt: settings

**Current cost: 2,092 SLOC** (`trdparty/consolemenu` 1,295 plus `cli/ui/setting/*` 797). That is 20% of the codebase to render one settings screen. It vendors a curses library, it drags in `windows-curses`, and it is the root cause of several known user-facing bugs.

**Build instead:**

1. The config file at the existing path (`~/.config/kathara.conf`), **same JSON schema, same path**. Existing installs must keep working. This is not a sanctioned place to innovate.
2. `kathara config get <key>`, `set <key> <value>`, `list`, `reset`. Scriptable, testable, works over SSH, works with no TTY. This becomes the primary interface.
3. `kathara settings` opens a `bubbletea` form over the same keys. Keep the command name for muscle memory.
4. Validation lives in `settings/` and runs on both paths, so `config set` and the TUI cannot disagree. The current `validator/` package (38 SLOC) folds into this.

**Target: under 600 SLOC.** Net deletion of roughly 1,500 lines.

### 3.3 Rebuilt: terminal integration

**Current design:** spawn an external terminal emulator per device (xterm, gnome-terminal, Terminal.app via `appscript`, cmd on Windows), or tmux via a vendored `libtmux`. This is the largest single source of user-facing failures: only two emulators are officially supported, xterm must be on `PATH` with correct font configuration, and the tmux path has shipped broken.

**Build instead, in priority order:**

1. **Built-in multiplexer, the default.** One window, one pane or tab per device, driven by `bubbletea` plus `creack/pty` on Unix and ConPTY on Windows. No external dependency, identical behaviour on all three platforms. Must support: switching devices, scrollback, resize, copy, and clean detach that leaves containers running.
2. **tmux as a first-class backend, done properly.** Not a vendored client library. Shell out to the `tmux` binary and drive it through its documented CLI. Requirements: one session per network scenario named from the lab, one window per device, correct handling of an existing session (attach, do not clobber), detach and reattach across `kathara` invocations, and `kathara connect --tmux <device>` attaching to the right window. Explicitly fixes the `decode()` class of bug by never parsing tmux internals.
3. **External emulators as opt-in** (`terminal_mode: external`). Port the existing adapters for users who want separate OS windows. This path is faithful-port territory, not redesign.
4. `kathara connect <device>` attaches to a single device, unchanged behaviour.

**Target: ~1,200 SLOC** multiplexer, ~250 tmux backend, ~400 ported external adapters.

### 3.4 CLI commands

Cobra-based. Cobra generates shell completions natively, deleting the current argparse-introspecting generator script.

| Command | Python SLOC | 1.0 |
|---|---|---|
| `lstart` | 211 | port |
| `lrestart` | 130 | port |
| `lclean` | 60 | port |
| `wipe` | 56 | port |
| `list` | 70 | port |
| `connect` | 68 | port |
| `check` | 66 | port |
| `exec` | 99 | port |
| `lconfig` | 83 | port |
| `settings` | 9 | rebuilt (§3.2), plus new `config` |
| `vstart` | 230 | **reimplement as sugar** |
| `vconfig` | 74 | **reimplement as sugar** |
| `vclean` | 37 | **reimplement as sugar** |
| `linfo` | 150 | deferred (§0.3) |

**The `vstart` family (§0.2 #3).** `VstartCommand` is 230 SLOC, larger than `lstart` at 211, because it re-implements the whole device-configuration flag surface for one device. Reimplement all three as thin wrappers that construct a one-device in-memory `Lab` and call the same code path as `lstart` / `lconfig` / `lclean`. **User-facing behaviour and every flag must be identical**, verified by golden tests. Expected result: ~90 SLOC instead of 341, and one device-creation path instead of two.

Every non-interactive command gains `--format {human,json,jsonl}`. See §5.

---

## 4. Required model shape changes

Sanctioned, and not optional. These are the difference between a correct port and a subtly broken one.

### 4.1 Interfaces: ordered slice, not a map

`Machine.interfaces` is an `OrderedDict[int, Interface]` in Python. Iteration order determines `eth0` / `eth1` numbering and, through the network plugin's derivation of MAC addresses from `md5("<machine>-<iface>")`, the addresses themselves. **Go map iteration is randomized. An unsorted `range` here produces a wrong but running network, not a crash.**

```go
type Machine struct {
    Lab  *Lab
    Name string

    // interfaces is kept sorted by Number. Numbers may be sparse: a lab.conf
    // declaring only eth0 and eth5 yields entries with Number 0 and 5.
    interfaces []Interface

    Meta      Meta
    FS        vfs.FS
    APIObject any
}

type Interface struct {
    Number int
    Link   *Link
    MAC    string
}

func (m *Machine) Interfaces() []Interface       // sorted, safe to range
func (m *Machine) Interface(n int) (Interface, bool)
func (m *Machine) AddInterface(l *Link, opts AddInterfaceOptions) (Interface, error)
```

**Reviewer rule: any `for ... range` over a map whose result reaches a container, network, or address must be rejected.** Sort the keys or use a slice.

### 4.2 Meta: typed struct with pointers for tri-state

Python's `add_meta(name, value: Any)` plus fifteen re-parsing accessors becomes one parse in `labfile` and one struct:

```go
type Meta struct {
    Image    string
    Memory   string   // "64m", validated at parse time
    CPUs     *float64 // nil means unset, which is not 0
    Bridged  *bool
    IPv6     *bool
    Shell    string
    NumTerms *int
    Exec     []string
    Sysctls  map[string]string
    Env      map[string]string
    Ports    []PortMapping
    Ulimits  map[string]Ulimit
    Volumes  []Volume
}
```

Pointers for the tri-state fields. Go's zero values would otherwise erase the `False` versus `None` distinction the Python code relies on.

### 4.3 Errors

```go
var (
    ErrLabNotFound          = errors.New("kathara: network scenario not found")
    ErrMachineNotFound      = errors.New("kathara: device not found")
    ErrMachineNotRunning    = errors.New("kathara: device not running")
    ErrMachineAlreadyExists = errors.New("kathara: device already exists")
    ErrLinkNotFound         = errors.New("kathara: collision domain not found")
    ErrDaemonConnection     = errors.New("kathara: cannot connect to the container daemon")
    ErrDependencyLoop       = errors.New("kathara: dependency loop in lab.dep")
    // ... one per Python exception class
)

type MachineError struct {
    Machine string
    Op      string
    Err     error
}
func (e *MachineError) Unwrap() error { return e.Err }
```

**Every error carries a stable string code** for the JSON CLI (§5.3). The mapping from Python exception class name to code is 1:1 and must be exhaustive, including the deferred features' errors: the Python client re-raises the identical exception classes, and `kathara-lab-checker` catches `MachineNotRunningError`, `MachineNotFoundError`, `MachineBinaryError` and `MachineCollisionDomainError` by name.

Partial failures across a lab operation use `errors.Join`, which `errors.Is` still matches through.

### 4.4 Constraint registers

Three machine-generated, human-reviewed tables, produced in Phase 2 before any porting:

- **`ORDERING.tsv`** - every site where iteration order is semantically meaningful. Highest-value document in the port.
- **`NILABILITY.tsv`** - every `Optional[...]` field, whether it becomes a pointer or a zero value, and what the zero value means.
- **`CONCURRENCY.tsv`** - every `multiprocessing.dummy.Pool` site (12 or more) and `threading.Thread` use. For each, decide explicitly: first error cancels, or complete-then-aggregate. The Python answer differs per call site. Target is `errgroup` with a bounded semaphore.

---

## 5. The JSON CLI contract

A new public interface and the foundation of the Python API. Design it before porting the CLI, and version it.

### 5.1 Global flag

`--format {human,json,jsonl}`, default `human`.

- `human`: current output, unchanged.
- `json`: exactly one JSON object on stdout. Nothing else on stdout, ever.
- `jsonl`: newline-delimited JSON events on stdout, for streaming commands.

Logs, progress bars and prompts go to **stderr** in all modes. In `json` and `jsonl` modes, stdout carries only protocol.

### 5.2 Streaming events

```
$ kathara exec --format jsonl --lab-hash abc123 web1 -- ping -c 2 8.8.8.8
{"type":"stdout","data":"PING 8.8.8.8 ..."}
{"type":"stderr","data":""}
{"type":"exit","code":0}
```

`exec` is the only streaming consumer in 1.0. When resource stats land post-1.0 they use the same envelope, one event per sample.

### 5.3 Errors

```json
{"error":{"code":"MachineAlreadyExists","message":"Device pc1 already exists.","machine":"pc1"}}
```

Emitted on stdout, exit code non-zero. `code` values are the stable identifiers from §4.3 and are part of the public contract.

Deferred features get their own codes so the Python client can raise something useful:

```json
{"error":{"code":"FeatureNotAvailable","message":"lab.ext external links are not supported in this release. Use Kathará 3.8.x.","feature":"lab.ext"}}
```

### 5.4 Deploying an in-memory scenario

The Python client builds scenarios in memory (this is how the official tutorials and IXP Digital Twin both work). The wire format is a **tar of a normal Kathará scenario directory**, so no new schema is needed:

```
kathara lstart --from-archive - --name "BGP Announcement" --format json
```

Reads a tar from stdin containing `lab.conf`, `lab.dep`, `*.startup` and the per-device folders. `--name` sets the hash, since there is no path to derive it from.

`kathara vstart` gains `--format json` for the incremental deploy pattern.

### 5.5 Command coverage

`lstart`, `lclean`, `lrestart`, `wipe`, `list`, `exec`, `check`, `vstart`, `vclean`, `lconfig`, `vconfig`, `config` support `json`. `exec` supports `jsonl`. `connect` and `settings` are interactive and human-only.

---

## 6. Dependency map

| Python | Go | Difficulty |
|---|---|---|
| `docker` SDK | `github.com/docker/docker/client` | Easy. First-party, better typed, different call shape |
| `kubernetes` | `k8s.io/client-go` | Medium. Different idioms (typed clients, `remotecommand` for exec) |
| `fs` (pyfilesystem2) | custom `vfs` package on `io/fs` | **Hard, must be designed.** See below |
| `rich` | `charmbracelet/lipgloss` | Easy |
| vendored `consolemenu` | `charmbracelet/bubbletea` | Rebuild (§3.2) |
| vendored `libtmux` | exec the `tmux` binary | Rebuild (§3.3) |
| `windows-curses`, npipe terminal | `golang.org/x/sys/windows`, `Microsoft/go-winio`, ConPTY | Medium |
| `appscript` (macOS) | exec `osascript` | Easy |
| `binaryornot`, `chardet` | `saintfish/chardet`, `golang.org/x/text` | Medium |
| `argparse` | `spf13/cobra` | Easy. Native completions replace the generator script |
| terminal raw mode | `golang.org/x/term`, `creack/pty` | Medium |
| `pyroute2`, `nsenter` | `vishvananda/netlink`, `containernetworking/plugins/pkg/ns` | Deferred with `lab.ext` |

**The `vfs` package**, replacing pyfilesystem2:

```go
package vfs

type FS interface {
    fs.FS
    Create(name string) (io.WriteCloser, error)
    MkdirAll(name string, perm os.FileMode) error
    Remove(name string) error
    SysPath(name string) (string, bool) // false for in-memory scenarios
}

func OSDir(path string) FS
func Memory() FS
```

`Lab(path)` uses `OSDir`, `Lab(name)` with no path uses `Memory()`, matching `open_fs("osfs://...")` versus `open_fs("mem://")`. The `FilesystemMixin` convenience methods (`create_file_from_string`, `update_file_from_string`, `write_line_before`, `delete_line`) become free functions in `vfs` rather than 15 methods hung on both `Lab` and `Machine`.

**Positive finding from the audit:** there are no regex lookaheads or backreferences anywhere in `src/`. Every pattern is RE2-compatible, so Go's `regexp` is a drop-in.

---

## 7. The Python client package

### 7.1 Design

Measured usage across the two real consumers and the official tutorials:

- **kathara-lab-checker** (2,933 lines): imports `Lab` in 29 files, the manager in 2. Calls `exec` at **26 sites**, plus `deploy_lab`, `undeploy_lab`, and one `get_machines_api_objects` used only as a liveness check. Model surface is `lab.hash`, `lab.get_machine`, `lab.fs`, `machine.interfaces`, `device.meta`, `device.get_sysctls`. Catches four exception classes by name.
- **Official tutorials**: `Lab("name")` with no path (memory FS), `new_machine`, `connect_machine_to_link`, `create_startup_file_from_list`, `create_file_from_path`, `create_file_from_string`, `deploy_lab`, then `get_machines_stats` as a liveness check.
- **IXP Digital Twin** (3,092 lines): `exec_obj` (16), `deploy_machine`, `undeploy_machine`, `deploy_link`, `copy_files`, `connect_tty`, `get_lab_from_api`, plus `ExternalLink`, plus incremental `build_diff` / `update_interconnection`.

**Key structural fact: the model layer never touches Docker.** `Lab`, `Machine`, `Link`, `Interface` and `FilesystemMixin` are ~770 SLOC of pure data manipulation. Scenario construction, which is most of the observed usage, involves no manager at all.

**Therefore the model stays in Python.** Do not put it behind the process boundary.

```
python/kathara/
├── __init__.py
├── _bin.py              locates the bundled Go binary
├── _proc.py             subprocess, JSON decode, error-code to exception mapping
├── exceptions.py        the same 40 classes, unchanged names
├── model/
│   ├── Lab.py           pure Python, memory FS preserved
│   ├── Machine.py
│   ├── Link.py
│   └── Interface.py
├── parser/netkit/
│   └── LabParser.py     stays Python; lab-checker imports it directly
├── manager/
│   └── Kathara.py       same method names, subprocess-backed
├── setting/Setting.py   reads and writes the same config file
└── bin/kathara          the bundled binary
```

`deploy_lab(lab)` serializes the in-memory `Lab` to a tar and pipes it to `kathara lstart --from-archive - --name <name>`. `exec(...)` shells out to `kathara exec --format jsonl` and yields parsed events. Errors decoded from the `code` field re-raise the original exception classes.

Duplicating the model and parser in both languages is a deliberate cost: roughly 800 lines of Python that must stay in sync with `model/` and `labfile/`. It is cheaper and far more robust than any FFI layer. The Layer B conformance vectors (§9) run against both implementations in the same CI job and are what keeps them honest.

### 7.2 What is lost

- `get_machine_api_object()` cannot return a live docker-py `Container` across a process boundary. Return the container ID and labels instead; callers needing more pair that with their own docker-py client. Observed real-world usage is one liveness check, so impact is minimal.
- `get_machines_stats` returns inventory fields only in 1.0 (§0.3).
- `ExternalLink` raises `FeatureNotAvailable` in 1.0.
- IXP's incremental deploy loop pays one process spawn per operation. Acceptable at 5-15ms against Docker round-trips of 10-100ms. If it proves painful, add a daemon mode later. **Do not build one now.**

### 7.3 Packaging

`goreleaser` builds five binaries. A build script emits five platform-tagged wheels with the binary in `.data/scripts/` so pip puts it on `PATH` directly. Backend is `hatchling`. No `cibuildwheel`, no `auditwheel`, no C toolchain, because there is no C extension. This is the `ruff` and `uv` distribution pattern.

`pip install kathara` keeps working and keeps giving users a working `kathara` command.

---

## 8. Distribution

Go's payoff. Out: PyInstaller (three platform specs), Nuitka (which blocked Arch releases for nearly a year), Python version drift on user machines, the `setuptools<81` and `chardet<6` pins, the `pyuv`-from-git-master install step.

In: `CGO_ENABLED=0` static binaries, cross-compilation, `goreleaser`.

Keep and repoint: Inno Setup for the Windows installer, deb and rpm packaging (now much simpler), the Launchpad PPA, the Homebrew cask, and the `.ronn` man pages, which are language-independent and need no work.

**Budget for code signing.** An unsigned Go binary is quarantined by Gatekeeper and warned about by SmartScreen exactly like a PyInstaller one. This needs an Apple Developer account and a Windows signing certificate, and the language change does not fix it.

Replace: the argparse-introspecting completion generator (cobra does this natively) and `scripts/pydoc/generate_doc.py` (pkgsite).

---

## 9. Verification

**This is the critical path.** The Bun Zig-to-Rust port completed in 11 days because its test suite was written in TypeScript and ran unchanged against the new binary. Kathará has no such asset: its 40 test files contain **1,315 `mock.patch` calls** asserting exact call signatures into the Python Docker SDK. They are worth zero as a portable oracle, and the Go Docker SDK has a different call shape.

Build the oracle **before** Phase 3. Four layers.

### Layer A: CLI golden tests

A harness that does not import Kathará, driving the current Python binary against real scenarios from `KatharaFramework/Kathara-Labs` and snapshotting observable state:

- exit codes and normalized stdout/stderr
- `docker inspect` JSON for every container and network (labels, mounts, capabilities, sysctls, ulimits, memory, NanoCPUs)
- inside each device: `ip link`, `ip addr`, `ip route`, `/etc/hosts`, the mounted file tree
- **MAC addresses per interface**, derived deterministically as the first 6 bytes of `md5("<machine>-<iface>")`. This makes topology wiring an exact assertion rather than a fuzzy one, and is the single best verification asset available. Use it aggressively
- teardown state after `lclean` and `wipe`

Record goldens from the Python build, replay against the Go build. Target 20 or more real scenarios plus one synthetic scenario per CLI flag.

**Because §0.2 permits structural divergence, Layer A is the primary arbiter of correctness for every rebuilt subsystem.** The `vstart` reimplementation in particular is only acceptable if its goldens are byte-identical to the current implementation's.

### Layer B: parser conformance vectors

A table of (input files, expected model JSON) for `lab.conf`, `lab.dep`, `*.startup`, folder layout and machine metadata, including malformed input. Generate from `parser/netkit/*.py` plus the `docs/*.ronn` man pages. Only 134 SLOC of in-scope parser, but this is where Netkit cross-compatibility lives and where silent regressions do the most damage.

**These vectors are shared by the Go `labfile` package and the Python client's `LabParser`.** They are what keeps the two implementations from drifting.

### Layer C: mine the existing pytest suite for intent

The 1,315 mock assertions are an excellent specification even though they are a useless oracle. Each states what call should be made with what arguments. Agent task: *for each mock assertion in the Python test suite, express the same expectation either as a Go table test or as a Layer A golden case.* **Do not translate the mocks into Go mocks**; that reproduces the coupling to a specific SDK call shape.

### Layer D: downstream acceptance

1. **`kathara-lab-checker` runs unmodified** against the Go build via the Python client, producing identical results on a known set of exercise scenarios. **Release gate.**
2. **Both official Python API tutorial scripts run unmodified** (`getting-started`, `managing-filesystem`). **Release gate.** Note `getting-started` ends with `get_machines_stats`, which is why the inventory fields are exempt from the stats deferral (§0.3).
3. **IXP Digital Twin runs.** **Post-1.0 gate**, since it requires `ExternalLink`. Track it, do not block on it.

Gate 1 is the highest-value single test in the project. It covers exec, deploy, undeploy, the parser, the model, the exception taxonomy and the Python client in one command.

### Test infrastructure

Go tooling that catches this port's characteristic bugs, all non-advisory in CI from the first commit: `go vet`, `staticcheck`, **`errcheck`** (the number one Python-to-Go porting bug is a dropped error where Python had an implicitly propagating exception), `-race` on every test run, `goleak`.

**There is no CI in the repo today.** Build it: GitHub Actions across Linux x64/arm64, macOS x64/arm64, Windows x64. Agents run against disposable VMs, never a workstation; the tests start dozens of privileged containers with `NET_ADMIN`, create bridges and VDE switches, and can leave host network state dirty on failure. Cleanup is part of every test, not a courtesy.

---

## 10. Workflow rules for agents

### Loop shape

**1 implementer, 2 adversarial reviewers, 1 fixer.** Reviewers work in separate context windows, see only the diff and the original `.py`, are told to assume the code is wrong, and never implement. The fixer applies review feedback.

Implementer context: the original Python file, this spec, `ORDERING.tsv`, `NILABILITY.tsv`, `CONCURRENCY.tsv`, and the target package's existing Go files.

The whole Python source is ~250k tokens and fits in one context window. Retrieval is unnecessary; give agents the files directly.

### Hard rules

- **Redesign only what §0.2 sanctions.** Everything else is a faithful port. Ideas go in `PROPOSED-DIVERGENCES.md`, not into the code.
- If the Python raised, the Go returns an error. **Never `panic` on a path the Python could reach.**
- **No stubs.** A `TODO` that compiles is a failure, not progress. This is the single most common way an agent misreads "get it to compile".
- No silently discarded errors. `errcheck` is a merge gate.
- **If you need a paragraph-long comment to justify a workaround, the code is wrong. Fix the code.**
- No `git stash`, no `git reset`, no `git` command that does not commit specific named files.
- Do not fix Python bugs you find. Record them in `DIVERGENCES.md` and port the behaviour as-is.
- Reject any `for ... range` over a map whose iteration order can reach a container, network, or address.
- Deferred features (§0.3) must return `FeatureNotAvailable` with a clear message, never a silent no-op and never a partial implementation.

### Parallelism

8-16 agents across 2 worktrees. The codebase is small enough that heavier parallelism mostly produces merge contention.

Use `go build ./... && go vet ./...` as the work queue, grouped by package, in the dependency order of §3.1.

---

## 11. Phases and gates

| Phase | Work | Days |
|---|---|---|
| 0 | Package graph, JSON CLI contract, error-code table, sanctioned-change designs | 4-6 |
| 1 | **Build the oracle** (Layers A, B, C) and CI | 11-16 |
| 2 | Constraint registers, dependency spikes (`vfs`, ConPTY, tmux driving) | 3-5 |
| 3 | Faithful ports, packages 1-15 | 4-6 |
| 4 | Compile, vet, smoke ladder | 3-5 |
| 5 | Behavioural equivalence against goldens, all platforms | 10-18 |
| 6 | Settings and terminal rebuilds | 5-8 |
| 7 | Python client package and wheels | 4-6 |
| 8 | Packaging, signing, installers (parallel with 5-7) | 5-10 |

**Total: 36-52 working days, so 7-11 weeks with one dedicated engineer driving agents.** Part-time, multiply by 2 to 2.5.

Note the shape: the phases that write the port are 7-11 days out of 36-52. The schedule is dominated by verification, exactly as it should be.

### Gate: do not start Phase 3 until

- The package graph is written down and acyclic.
- The JSON CLI contract and the complete error-code table exist.
- Layer A reproduces goldens for 20 or more real scenarios against the **current Python build**, twice, deterministically. An oracle that is flaky against the implementation it was recorded from cannot adjudicate the port.
- `ORDERING.tsv` and `NILABILITY.tsv` exist and a human has read them.
- Disposable VMs with Docker plus the network plugin are available, and macOS and Windows runners exist.

### Gate: do not merge until

- Golden suite green on all five platform/arch combinations.
- **Zero tests skipped or deleted, verified by hand.**
- `-race`, `goleak` and `errcheck` clean.
- Layer D gates 1 and 2 pass.
- `vstart` / `vconfig` / `vclean` goldens are byte-identical to the current implementation's.
- A human has read the diff for `model/`, `labfile/` and the wiring path in `backend/docker/link.go`. Roughly 1,000 SLOC of Python worth of Go, and where a silent semantic change does the most damage.

### Gate: do not release until

- Shipped as a canary alongside 3.8.x and run by at least one real course for a full semester.
- Cutover happens **between** semesters, never during one. A bad release lands in a lab session with 200 students in it.
- The Python 3.8.x line stays maintained for one full academic year, security fixes only, and is the documented answer for `lab.ext` users until the deferred work lands.

### Post-1.0 backlog

In recommended order: resource stats and `linfo`, `lab.ext` external links plus `netns/` (then Layer D gate 3), version-check webhooks if anyone actually wants them.

---

## 12. Risk register

Ranked by expected damage. Each needs a targeted Layer A or B test written **before** the corresponding package is ported.

1. **Map iteration order destroying topologies.** Interface numbering is load-bearing and feeds MAC derivation. Go randomizes map iteration. Produces a wrong but running network. Highest-probability serious bug in the port. Mitigation: §4.1 plus the reviewer rule.
2. **Thread pool to errgroup semantics.** 12+ `multiprocessing.dummy.Pool` sites plus `threading.Thread` for Kubernetes shutdown waits. Partial-failure behaviour during a 50-device deploy will differ unless specified per call site. Mitigation: `CONCURRENCY.tsv`.
3. **Scope creep through §0.2.** A sanctioned-redesign list is an invitation to redesign adjacent things. The `vstart` collapse and the terminal rebuild are the likeliest places for an agent to quietly change behaviour while "improving" it. Mitigation: byte-identical goldens as an explicit merge gate, and `PROPOSED-DIVERGENCES.md` as the only outlet.
4. **Dropped errors.** Python propagates implicitly; Go requires a decision at every call site. Default failure mode of a mechanical port. Mitigation: `errcheck` as a merge gate from commit one.
5. **`defer` versus `finally`.** Go defers run LIFO at function exit; Python `finally` is lexically scoped. Cleanup ordering in `undeploy_lab` and terminal restore will shift.
6. **Terminal state restoration.** A botched termios or console-mode restore leaves the user's shell unusable after `kathara connect`. High visibility, low natural coverage, and now sitting inside a rebuilt subsystem. Needs an explicit test on all three platforms.
7. **Numeric conversion.** Memory strings (`64m`) to bytes, `cpus` float to Docker's int64 NanoCPUs, Python `//` versus Go integer division.
8. **Encoding and binary detection.** Python `str` is Unicode, Go `string` is bytes. `utils.py` does `unicodedata` normalization, `chardet` detection and `binaryornot` checks before tarring startup files into containers. BOMs, latin-1 configs and symlinks in lab directories will behave differently. Tar header permissions and symlink handling need their own tests.
9. **Windows paths.** `os.path` versus `filepath`, drive letters, UNC, the `hosthome` mount translation for Docker Desktop. Expect Windows to go green last.
10. **Truthiness.** Python `if not x` covers empty list, empty dict, `0` and `""`. Go distinguishes nil slice, empty slice and zero value. Mitigation: `NILABILITY.tsv`.
11. **Model drift between Go and Python.** The client duplicates `model/` and `LabParser`. Mitigation: Layer B vectors run against both in the same CI job.

**Deferred with their features:** `setns` from Go (goroutine thread migration, `runtime.LockOSThread`, intermittent failure) moves out of 1.0 with `lab.ext`. When it returns, copy the `containernetworking/plugins/pkg/ns` pattern exactly rather than inventing one, and confine it to `netns/` behind a Linux build tag.

---

## 13. Capacity planning

**This project runs on a Max 20x subscription, not API billing. The constraint is rate-limit windows, not money.** Plan against windows and pace to roughly 95% utilisation of each one.

### 13.1 The constraints that actually bind

- **A rolling 5-hour session window.** Starts with the first message, resets 5 hours later regardless of activity in between.
- **Weekly caps.** Max plans carry two: one across all models, and a second that applies to Sonnet models only. Sonnet work counts against both.
- **One shared pool.** Claude Code, Claude Desktop and claude.ai all draw from the same account budget. The human's own chat usage competes with the agents.
- Anthropic no longer publishes fixed prompts-per-window figures, only relative capacity (Max 20x is 20x Pro per session). Third-party message-count numbers circulating online are unofficial and should not be planned against.
- Check with **`/usage`** in Claude Code, or Settings → Usage in the apps. Do this at the start and midpoint of every work block.

Since May 6, 2026, the 5-hour limits were doubled and peak-hour reductions removed, so any planning intuition formed before that is stale in your favour.

### 13.2 Consequences for this plan

**Work must be decomposable into window-sized units with a clean commit at the end.** A lockout mid-loop that leaves a package half-ported is far more expensive than the lost time, because the next session has to reconstruct state. Never start an unbounded loop past ~70% window consumption; spend the tail on review, documentation and the constraint registers, which are cheap and interruptible.

**Parallelism is now limited by the shared pool, not by merge contention.** The earlier guidance of 8-16 agents (§10) was about merge conflicts. On a subscription, 16 concurrent agents drain the window roughly 16x faster in wall-clock terms. Total consumption is the same either way, so the question is pacing rather than headcount: size the fan-out so a batch completes inside one window rather than getting cut in half.

**Model routing is a scheduling decision, not just a quality one.** Because Sonnet usage counts against both weekly caps, an all-Sonnet strategy can exhaust the Sonnet-only cap while leaving all-model headroom unused. Mix deliberately:

- **Sonnet** for the mechanical port (§3.1), high volume and low judgment.
- **Opus** for adjudicating golden-test diffs, the constraint registers, and the model, parser and wiring reviews called out in the merge gate. Low volume, high judgment.

**Phase 5 is the capacity risk**, for the same reason it was the cost risk: unbounded iteration where agents wait on Docker. Structure it as batch-then-fix rather than one-failure-at-a-time. Run the full golden suite, dump every failure to a file, then have agents work the file offline. A loop that spends window time waiting for containers to start is burning capacity to produce nothing.

### 13.3 The process alarm

Dollars were a bad alarm anyway. The right one is convergence:

**Track golden-test pass rate at the end of every window.** If it is not improving window over window during Phase 5, stop porting and fix the oracle. Agents looping without converging is the failure mode, and it looks identical to progress from the inside.

Secondary signals: the same test failing across three consecutive windows, and `PROPOSED-DIVERGENCES.md` growing faster than `DIVERGENCES.md` (agents wanting to redesign usually means the spec is underspecified for that area).

### 13.4 If it turns out to be rate-limit bound

The 36-52 day estimate in §11 is an effort estimate. If Phases 3 and 5 consistently run out of window before running out of work, the schedule becomes rate-limit bound, and the lever is **more seats, not a bigger plan** since Max 20x is the top individual tier. A second account or Team seats for whoever else is on the project buys parallel windows.

For scale reference: the Bun Zig-to-Rust port consumed 5.9B uncached input tokens, 690M output tokens and 72B cache reads over 11 days to move 535,496 lines. Kathará's 1.0 surface is roughly 1/70th of that line count, though with a worse ratio because verification must be built rather than inherited.

