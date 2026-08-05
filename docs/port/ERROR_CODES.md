# ERROR_CODES.md — Frozen Error Taxonomy (Kathara Go Port, Phase 0)

**STATUS: FROZEN.** This document is the Phase 0 contract for spec §4.3 (Go error
taxonomy) and §5.3 (JSON error envelope). Later phases implement against it without
relitigating. Every claim below was verified against `kathara-python/src/Kathara`
(v3.8.3) source at the cited `file:line` sites, against
`/root/kathara/kathara-lab-checker/src`, and against `analysis/ERROR_CODES.tsv`,
`analysis/SYNTHESIS.md` §1.9 and `analysis/CONCURRENCY.tsv`. Paths below are relative
to `kathara-python/src/Kathara/` unless noted.

Accepted rulings honored here: user-reachable builtin exceptions get stable codes and
everything else buckets to `InternalError`; `linfo`, `lab.ext`, stats **sampling**, and
webhooks are `FeatureNotAvailable` stubs; `get_iptables_version` is carved out into
`backend/docker` (so its `FileNotFoundError` is live in 1.0); nondeterministic Python
orders are emitted in canonical sorted order; Ctrl-C exits 0 with warning parity.

---

## 0. Contract rules

### 0.1 The three renderings of one error

Every user-visible error has exactly three renderings, all frozen by this table:

1. **Human mode** (stdout — Python's `RichHandler` logging writes to stdout, see
   `JSON_CLI_CONTRACT.md` A1): `CRITICAL ({human_label}) {message}` — byte-for-byte what
   Python's `src/kathara.py` catch-all prints (`logging.critical(f"({type(e).__name__})
   {str(e)}")`, exit 1). `human_label` is the **Python class name**, not the code.
   For most rows `human_label = code + "Error"`; exceptions are listed per row.
   With debug level `EXCEPTION`, the same line is emitted with a stack trace appended
   (Go: the wrapped error chain), still exit 1.
2. **JSON mode** (stdout): `{"error":{"code":"<Code>","message":"<message>",...fields}}`,
   exit 1 (§6, §7). `code` values are the stable identifiers below and are the
   public contract.
3. **Go API**: a sentinel `var Err<Code>` matched by `errors.Is`, or a struct type
   matched by `errors.As` when the error carries data. Struct types implement
   `Unwrap() error` returning their class sentinel, so **both** `errors.Is(err,
   kerr.ErrMachineBinary)` and `errors.As(err, &binErr)` work on the same value.

### 0.2 Message templates are exact

The `message` field and human-mode message are the **exact** Python format strings,
typos, missing periods, missing backticks and trailing spaces included. Known quirks
frozen verbatim:

- `LinkAlreadyExistsError`: `` Collision domain {name} is already the network scenario. ``
  (missing "in" — `model/Lab.py:378`).
- `InvalidImageArchitectureError`: no trailing period (`exceptions.py:160`).
- Volume-mode `MachineOptionError` ends with a **trailing space**: `` Invalid volume mode
  `{mode}` on `{host_path}` mount. Allowed values are ro, rw, rx. `` (with final space —
  `model/Machine.py:279`, `ALLOWED_VOLUME_MODES = ["ro", "rw", "rx"]` at `model/Machine.py:28`).
- All three ulimit messages interpolate `{name}` = the **literal option name string
  `ulimit`**, not the machine name and not the ulimit key (raised inside
  `update_meta` where `name == "ulimit"`, `model/Machine.py:221,224,238`), e.g.
  `` Invalid ulimit value (`nofile=-2`) on `ulimit`. Values must be >= -1. ``
- `MachineOptionError` volume-format variant has no trailing period
  (`model/Machine.py:274`).
- `FileExistsError` is semantically inverted in Python (raised when the path does
  **not** exist). We keep class label, code name and message for parity, but the Go
  error wraps `fs.ErrNotExist`, never `fs.ErrExist` (§3).
- Python set interpolation (`The following devices are not in the network scenario:
  {'a', 'b'}.`) is nondeterministic in Python. **Frozen ruling:** Go renders the
  Python set literal syntax with elements single-quoted, comma-space separated, braces,
  **sorted bytewise ascending**: `{'pc1', 'pc2'}`.

### 0.3 Go package shape (frozen)

In `kerrors/errors.go` + `kerrors/codes.go` (leaf package `kerrors`, referred to as
`kerr`; `kathara/errors.go` re-exports every name below as aliases so the spec-§4.3
`kathara.*` surface holds — see `PACKAGE_GRAPH.md` D-1):

```go
// One class sentinel per Python exception class (exhaustive, §1).
// Sentinel text is identity only, never user-facing.
var (
    ErrMachineNotFound = errors.New("kathara: device not found")
    ErrMachineNotRunning = errors.New("kathara: device not running")
    // ... Err<Code> for every code in §1 ...
)

// Context wrappers: attach structured JSON fields to sentinel-class errors.
type MachineError struct{ Machine, Op string; Err error } // JSON field "machine"
type LinkError    struct{ Link, Op string; Err error }    // JSON field "link"
type ImageError   struct{ Image string; Err error }       // JSON field "image"
// Each implements Unwrap() error.

// Data-bearing class types (Unwrap() returns the class sentinel):
type BinaryError          struct{ Binary, Machine string }            // MachineBinary
type ImageArchError       struct{ Image, Arch string }                // InvalidImageArchitecture
type NonSeqInterfaceError struct{ Iface int; Machine string }         // NonSequentialMachineInterface
type MacAddressError      struct{ MAC string; Iface int; Machine string } // InterfaceMacAddress
type CollisionDomainError struct{ Machine, Link string; Iface int; Variant int } // MachineCollisionDomain
type OptionError          struct{ Machine, Option string; Message string }       // MachineOption
type SettingsInvalidError struct{ Reason string }                     // Settings
type SettingsNotFoundError struct{ Path string }                      // SettingsNotFound
type HostArchError        struct{ Arch string }                       // HostArchitecture
type FeatureNotAvailableError struct{ Feature string }                // FeatureNotAvailable
```

In `labfile` (parser package):

```go
type ParseError struct {
    File string // "lab.conf", "lab.dep", "lab.ext"; "" for non-file sites
    Line int    // 1-based; 0 when not applicable
    Msg  string // rendered exactly per §2 template
    Code string // "Syntax" (default) or "Value" (reserved-name variant only)
}
```

`Error()` on every type renders the exact §2 template. The JSON encoder derives
`code` from the class sentinel (exhaustive switch), `message` from `Error()`, and
extra fields via `errors.As` over the wrapper and data-bearing types.

### 0.4 Choosing struct vs sentinel

Frozen rule applied throughout §1: **struct type** when the error carries data that
Python exposes as attributes or that a JSON field needs *and* no generic wrapper fits
(`.binary`, `.machine_name`, `.image_name`, `.arch`, iface numbers, MAC, file/line,
feature); **sentinel + wrapper** (`MachineError`/`LinkError`/`ImageError` or
`fmt.Errorf("...: %w", ...)`) for message-only classes.

---

## 1. The complete code registry

46 codes total: 33 Python-class codes (1:1, exhaustive per spec §4.3) + 8 builtin
codes + 2 passthrough codes + 3 port-new codes. 40 are emittable by Go 1.0; 6 are
RESERVED (never emitted, kept so the mapping stays 1:1 and forward-compatible).

### 1.1 Kathara exception classes (33 — `exceptions.py`)

| Python class | Code | Go representation | JSON fields | 1.0 status |
|---|---|---|---|---|
| `ClassNotFoundError` | `ClassNotFound` | none (cobra handles unknown command; §7) | — | RESERVED — Python converts it to `Unrecognized command \`{cmd}\`.` + help, exit 1 (`src/kathara.py`), never `({type}) {msg}` |
| `HTTPConnectionError` | `HTTPConnection` | sentinel `ErrHTTPConnection` | — | RESERVED — only raisers are `webhooks/` (deferred; `DockerHubApi.py:34,36,40,70,72,79`, `GitHubApi.py:19,21,25`), and both 3.8.3 consumers swallow it (`setting/Setting.py:207`, `cli/ui/setting/CommonOptionsHandler.py:69`) |
| `InstantiationError` | `Instantiation` | none (no singletons in Go) | — | RESERVED (`setting/Setting.py:65`, `manager/Kathara.py:41`, `event/EventDispatcher.py:31`, `foundation/cli/CliArgs.py:22`, `auth/PrivilegeHandler.py:23` — all programmer-error guards) |
| `InvocationError` | `Invocation` | sentinel `ErrInvocation`, wrapped with site message | — | live |
| `SettingsError` | `Settings` | struct `SettingsInvalidError{Reason}` | — | live |
| `SettingsNotFoundError` | `SettingsNotFound` | struct `SettingsNotFoundError{Path}`; wraps `fs.ErrNotExist` | `path` | live (auto-healed at startup like Python `__main__`; surfaces via API with explicit path) |
| `DockerDaemonConnectionError` | `DockerDaemonConnection` | sentinel `ErrDaemonConnection` (spec §4.3 name), wraps underlying client error | — | live |
| `NotSupportedError` | `NotSupported` | sentinel `ErrNotSupported`, wrapped with operation message | — | live (k8s update paths) |
| `PrivilegeError` | `Privilege` | sentinel `ErrPrivilege`, wrapped with site message | — | live (root checks are ported even though `@privileged` is a no-op; the lab.ext / external-CD sites are superseded by `FeatureNotAvailable`, §5) |
| `InterfaceNotFoundError` | `InterfaceNotFound` | sentinel `ErrInterfaceNotFound` | — | RESERVED — sole raiser `os/Networking.py:38` is deferred (the carve-out ports only `get_iptables_version`) |
| `HostArchitectureError` | `HostArchitecture` | struct `HostArchError{Arch}` | `arch` | live |
| `LabAlreadyExistsError` | `LabAlreadyExists` | sentinel `ErrLabAlreadyExists` | — | live (k8s namespace 403 conflict) |
| `LabNotFoundError` | `LabNotFound` | sentinel `ErrLabNotFound` (spec §4.3), wrapped `MachineError`/`LinkError` for the referenced object | `machine` or `link` when known | live |
| `EmptyLabError` | `EmptyLab` | sentinel `ErrEmptyLab` | — | live |
| `MachineDependencyError` | `MachineDependency` | sentinel `ErrDependencyLoop` (spec §4.3 Go name; **code stays `MachineDependency`** for 1:1 class mapping) | — | live |
| `MountDeniedError` | `MountDenied` | sentinel `ErrMountDenied` | `machine` (device variant only) | live (host-drive variant escapes; device variant is always caught → warning, §2) |
| `MachineAlreadyExistsError` | `MachineAlreadyExists` | sentinel `ErrMachineAlreadyExists` via `MachineError` wrap | `machine` | live |
| `NonSequentialMachineInterfaceError` | `NonSequentialMachineInterface` | struct `NonSeqInterfaceError{Iface, Machine}` | `iface`, `machine` | live |
| `MachineOptionError` | `MachineOption` | struct `OptionError{Machine, Option, Message}` | `machine`, `option` | live |
| `MachineCollisionDomainError` | `MachineCollisionDomain` | struct `CollisionDomainError{Machine, Link, Iface, Variant}` — **lab-checker contract, §4** | `machine`, `link` (variant 1: `machine`, `iface`) | live |
| `MachineNotFoundError` | `MachineNotFound` | sentinel `ErrMachineNotFound` via `MachineError` wrap — **lab-checker contract, §4** | `machine` (singular) / `machines` sorted array (set variant) | live |
| `MachineNotRunningError` | `MachineNotRunning` | sentinel `ErrMachineNotRunning` via `MachineError` wrap — **lab-checker contract, §4** | `machine` | live |
| `MachineNotReadyError` | `MachineNotReady` | sentinel `ErrMachineNotReady` via `MachineError` wrap | `machine` | live (k8s pod phase) |
| `MachineBinaryError` | `MachineBinary` | struct `BinaryError{Binary, Machine}` — **lab-checker contract, §4** | `binary`, `machine` | live |
| `InterfaceMacAddressError` | `InterfaceMacAddress` | struct `MacAddressError{MAC, Iface, Machine}` | `mac`, `iface`, `machine` | live |
| `LinkNotFoundError` | `LinkNotFound` | sentinel `ErrLinkNotFound` via `LinkError` wrap | `link` | live (lab.ext message variant unreachable in 1.0) |
| `LinkAlreadyExistsError` | `LinkAlreadyExists` | sentinel `ErrLinkAlreadyExists` via `LinkError` wrap | `link` | live |
| `TestError` | `Test` | none | — | RESERVED-DEAD — never instantiated in 3.8.3 (leftover of removed `ltest`); code reserved, never emitted, do not port |
| `MachineSignatureNotFoundError` | `MachineSignatureNotFound` | none | — | RESERVED-DEAD — never raised; code reserved, never emitted, do not port |
| `InvalidImageArchitectureError` | `InvalidImageArchitecture` | struct `ImageArchError{Image, Arch}` | `image`, `arch` | live. NOTE: Python class is-a `ValueError` (`exceptions.py:152`) — any Python-client generic `except ValueError` must still catch it |
| `DockerImageNotFoundError` | `DockerImageNotFound` | sentinel `ErrDockerImageNotFound` via `ImageError` wrap | `image` | live |
| `DockerPluginError` | `DockerPlugin` | sentinel `ErrDockerPlugin`, wrapped with site message | — | live |
| `KubernetesConfigMapError` | `KubernetesConfigMap` | sentinel `ErrKubernetesConfigMap` | — | live |

Human labels for all rows above: `code + "Error"` (e.g. `MachineNotFound` →
`MachineNotFoundError`).

### 1.2 Builtin exceptions that reach users (8 codes, mapped by call-site semantics)

| Python class (human label) | Code | Go representation | JSON fields | 1.0 status |
|---|---|---|---|---|
| `SyntaxError` | `Syntax` | `labfile.ParseError{File, Line, Msg, Code:"Syntax"}` (file sites) or plain rendered error wrapping `ErrSyntax` (device-name / `--eth` / raw `parse_cd_mac_address` sites) | `file`, `line` (file variants only) | live |
| `ValueError` | `Value` | per call-site: reserved-name → `labfile.ParseError{Code:"Value"}`; others wrap `ErrValue` with site message | `file`, `line` (reserved-name variant only) | live |
| `OSError` (also every Python `raise IOError(...)` — **`IOError` is an alias of `OSError` in Python 3; `type(e).__name__` is `OSError`**, so the observable label is `OSError` for both) | `OS` | wrap `ErrOS` with site message; lab-file open failures additionally wrap the underlying fs error with `%w` | — | live (lab-file open/empty variants); the lab.ext / external-CD platform variants are superseded by `FeatureNotAvailable` (§5); terminal-internal sites → `InternalError` |
| `FileNotFoundError` | `FileNotFound` | wrap `fs.ErrNotExist` + exact message | — | live (`Cannot find \`iptables\` in the host.` is live via the `get_iptables_version` carve-out; `Unable to find Kathara.` via terminal spawn; `Unable to find \`{HOSTTMP_KEY}\` in plugin mounts.` via DockerPlugin) |
| `FileExistsError` | `FileExists` | wrap `fs.ErrNotExist` (semantics; §0.2) + exact message | `path` | live (volume/directory validation) |
| `NotADirectoryError` | `NotADirectory` | wrap `ErrNotADirectory` + exact message | `path` | live |
| `PermissionError` | `Permission` | wrap `fs.ErrPermission` + exact message | — | live (volume mount permissions) |
| `ConnectionError` | `Connection` | sentinel `ErrConnection`, wrapped with site message | `image` (image-pull variant) | live |

Builtins that do **not** get codes (bucket to `InternalError`, §1.4, per accepted
ruling): `NotImplementedError` (abstract stubs — unrepresentable with Go interfaces),
`ImportError` (static linking), `AttributeError`/`KeyError`/`TypeError`/`RuntimeError`
(latent-bug and internal-signal paths), `StopIteration` (control flow),
`io.UnsupportedOperation` (`foundation/model/FilesystemMixin.py:178` — maps to
`Invocation`, see §2), bare `Exception` signals (k8s WS terminal).
`argparse.ArgumentTypeError` is a usage error, not part of the JSON contract (§7).
`KeyboardInterrupt` is not an error (§7).

### 1.3 Third-party passthrough (2 codes)

Where Python **translates** a third-party error, Go maps the same trigger to the same
Kathara code (translation table in §3). Where Python **re-raises it untranslated**
(`raise e`), the user observes `(APIError) ...` / `(ApiException) ...`; Go assigns:

| Source | Code | Human label | Go representation | 1.0 status |
|---|---|---|---|---|
| Docker daemon API error not matched by any translation rule (`DockerMachine.py:386,427,520,876`; `DockerImage.py:152`) | `DockerAPI` | `APIError` | wrap the `github.com/docker/docker` client error; message = daemon error text | live |
| Kubernetes API error not matched by any translation rule (`KubernetesMachine.py:370,837`; `KubernetesManager.py:145`) | `KubernetesAPI` | `ApiException` | wrap the k8s client error | live |

### 1.4 Port-new codes (3)

| Code | Human label | Go representation | JSON fields | Purpose |
|---|---|---|---|---|
| `FeatureNotAvailable` | `FeatureNotAvailable` | struct `FeatureNotAvailableError{Feature}` wrapping `ErrNotSupported` | `feature` | deferred features (§5); Python client raises `Kathara.exceptions.NotSupportedError` for it |
| `InternalError` | `InternalError` | any error not mapped above reaching the CLI boundary | — | the exhaustive fallback; human line `CRITICAL (InternalError) {go error text}` (accepted divergence: these paths printed raw Python class names and were latent bugs) |
| `ConfirmationRequired` | `ConfirmationRequired` | sentinel `ErrConfirmationRequired` | — | JSON-mode-only code contributed by `JSON_CLI_CONTRACT.md` §1.5: `wipe` without `-f/--force` in `json`/`jsonl` mode emits `` Confirmation required: re-run with `--force` to wipe Kathara. `` and exits 1, nothing wiped; can never be raised in human mode (the human prompt is unchanged) |

---

## 2. Message template catalog (exact, with verified raise sites)

Templates are Python f-string/%-format contracts; `{x}` marks interpolation. Multiple
templates under one code are variants; all must be reproduced byte-exact.

### Invocation
- `You can either specify \`selected_machines\` or \`excluded_machines\`.` — `manager/docker/DockerMachine.py:137,594`; `manager/kubernetes/KubernetesMachine.py:161,587`
- `You can either specify \`selected_links\` or \`excluded_links\`.` — `manager/docker/DockerLink.py:48`; `manager/kubernetes/KubernetesLink.py:57`
- `You can either select or exclude devices.` — `manager/docker/DockerManager.py:147,342`; `manager/kubernetes/KubernetesManager.py:107,288`
- `You must specify a running network scenario hash or name.` — `manager/docker/DockerManager.py:693`; `manager/kubernetes/KubernetesManager.py:685`
- `You must specify a device name or object.` — `model/Lab.py:326`
- `You must specify only a parameter among {', '.join(kwargs.keys())}` (no trailing period) — `utils.py:114,123`
- `You must specify a parameter among {', '.join(kwargs.keys())}` (no trailing period) — `utils.py:121`
- `There is no filesystem associated to this object.` — `foundation/model/FilesystemMixin.py:79,99,122,142,166,194,218,256,293`
- `Cannot create a file if the filesystem is not set.` — `foundation/model/FilesystemMixin.py:56`
- `To create a file from stream, you must open it with read permissions.` — `foundation/model/FilesystemMixin.py:178` (Python raises `io.UnsupportedOperation`; frozen mapping → `Invocation`)
- `\`add\` called without a MenuFactory set.` — `foundation/cli/ui/setting/OptionsHandler.py:17` (internal menu machinery; unreachable in Go — bubbletea rebuild)

### Settings (struct; wrapper + reasons)
Wrapper (`exceptions.py:21`): `Settings file is not valid: {reason} Fix it or delete it before launching.`
Reasons (`setting/Setting.py`): `Not a valid JSON.` (:111) · `Networks Prefix must only contain lowercase letters and underscore.` (:218) · `Device Prefix must only contain lowercase letters and underscore.` (:223) · `Debug Level must be one of the following: CRITICAL, ERROR, WARNING, INFO, DEBUG, EXCEPTION.` (:226, from `AVAILABLE_DEBUG_LEVELS` at `setting/Setting.py:17`) · `Manager Type not allowed.` (:241) · `Terminal Emulator \`{terminal}\` not valid! Install it before using it.` (:294)

### SettingsNotFound
- `Settings file not found in path \`{path}\`.` — `setting/Setting.py:104`

### DockerDaemonConnection
- `Cannot connect to Docker Daemon, this may indicate that it is not running. {message}` — `manager/docker/DockerManager.py:50,52,73`; `{message}` = `str()` of the underlying client/connection error

### NotSupported
Wrapper (`exceptions.py:36`): `Not Supported: {message}`
- `Unable to update a running device.` — `manager/kubernetes/KubernetesManager.py:161,177`
- `Unable to update a running network scenario.` — `manager/kubernetes/KubernetesManager.py:747`

### Privilege
- `You must be root in order to show all Kathara devices of all users.` — `cli/command/ListCommand.py:58`
- `You must be root in order to wipe all Kathara devices of all users.` — `cli/command/WipeCommand.py:66`
- `You must be root in order to start Kathara devices in privileged mode.` — `cli/command/LstartCommand.py:222`
- `You must be root in order to start this Kathara device in privileged mode.` — `cli/command/VstartCommand.py:219`
- `You must be root in order to start device \`{machine.name}\` in privileged mode.` — `manager/docker/DockerMachine.py:329`
- `You must be root to get networks statistics of all users.` — `manager/docker/DockerLink.py:268`
- `You must be root to get devices statistics of all users.` — `manager/docker/DockerMachine.py:1038`
- superseded by `FeatureNotAvailable` in 1.0: `You must be root in order to use lab.ext file.` (`cli/command/LstartCommand.py:203`) · `You must be root in order to use external collision domains.` (`manager/docker/DockerLink.py:337,372`)

### HostArchitecture
- `Not implemented for host architecture \`{architecture}\`.` — `utils.py:412`

### LabAlreadyExists
- `Previous network scenario execution is still terminating. Please wait.` — `manager/kubernetes/KubernetesManager.py:143` (k8s namespace create, 403)

### LabNotFound
- `Device \`{machine.name}\` is not associated to a network scenario.` (both `%s` and f-string sites) — `manager/docker/DockerManager.py:99,196,246,284,433,504,930`; `manager/kubernetes/KubernetesManager.py:431,502,836`
- `Machine \`{machine.name}\` is not associated to a network scenario.` — `manager/kubernetes/KubernetesManager.py:54,193`
- `Collision domain \`{link.name}\` is not associated to a network scenario.` — `manager/docker/DockerManager.py:120,206,256,306`; `manager/kubernetes/KubernetesManager.py:78,237`
- `Link \`{link.name}\` is not associated to a network scenario.` — `manager/docker/DockerManager.py:1022`; `manager/kubernetes/KubernetesManager.py:926`

### EmptyLab
- `No devices in the current network scenario.` — `exceptions.py:64`, raised `cli/command/LstartCommand.py:185`

### MachineDependency
- `Machines' dependency loop in lab.dep file.` — `parser/netkit/DepParser.py:73`

### MountDenied
- `Host drive is not shared with Docker.` — `manager/docker/DockerMachine.py:515` (escapes to user)
- `Device \`{self.name}\` cannot mount volumes.` — `model/Machine.py:597` (always caught: `manager/docker/DockerMachine.py:324` and `manager/kubernetes/KubernetesMachine.py:406` convert it to warning `Volumes of device \`{machine.name}\` will not be mounted.` and continue — Go must preserve the catch-and-warn, not surface the error)

### MachineAlreadyExists
- `Device with name \`{machine_name}\` already exists.` — `exceptions.py:78`; raised `model/Lab.py:287`, `manager/docker/DockerMachine.py:224`, `manager/kubernetes/KubernetesMachine.py:368` (k8s 409 translation)

### NonSequentialMachineInterface
- `Interface \`{iface_num}\` missing on device \`{machine_name}\`.` — `exceptions.py:83`; raised `model/Machine.py:375` (pre-deploy `check()`)

### MachineOption
All raised in `model/Machine.py` (`update_meta`/accessors). `option` JSON field = the option name being parsed. Note: unknown option **names** do not error — they land in the frozen `Meta.Extras` map (accepted ruling; golden `syn-unknown-opt`).
- `Invalid sysctl value (\`{value}\`) on \`{self.name}\`, missing \`=\` or value not in \`net.\` namespace.` — :184
- `Invalid env value (\`{value}\`) on \`{self.name}\`.` — :202
- `Invalid ulimit value (\`{value}\`) on \`ulimit\`. Values must be >= -1.` — :221 (`{name}` = literal `ulimit`, §0.2)
- `Invalid ulimit value (\`{value}\`) on \`ulimit\`. Soft limit (-1) cannot be greater than hard limit ({hard}).` — :224
- `Invalid ulimit value (\`{value}\`) on \`ulimit\`.` — :238
- `Port protocol value not valid on \`{self.name}\`.` — :254
- `Port value not valid on \`{self.name}\`.` — :263
- `The volume specified \`{value}\` is not in a valid format: <host_path>|<guest_path>|[<mode>]` (no trailing period) — :274
- `Invalid volume mode \`{mode}\` on \`{host_path}\` mount. Allowed values are ro, rw, rx. ` (trailing space, §0.2) — :279
- `Memory value not valid on \`{self.name}\`.` — :504,509
- `CPU value not valid on \`{self.name}\`.` — :532,537
- `Terminals Number value on \`{self.name}\` must be a positive value or zero.` — :569
- `Terminals Number value not valid on \`{self.name}\`.` — :571
- `IPv6 value not valid on \`{self.name}\`.` — :618

### MachineCollisionDomain (variants numbered for `CollisionDomainError.Variant`)
1. `Interface {number} already set on device \`{self.name}\`.` (no backticks on number) — `model/Machine.py:106`
2. `Device \`{self.name}\` is already connected to collision domain \`{link.name}\`.` — `model/Machine.py:109`
3. `Device \`{self.name}\` is not connected to collision domain \`{link.name}\`.` — `model/Machine.py:132`
4. `Device \`{machine.name}\` is already connected to collision domain \`{link.name}\`.` — `manager/docker/DockerManager.py:209`
5. `Device \`{machine.name}\` is not connected to collision domain \`{link.name}\`.` — `manager/docker/DockerManager.py:259`

### MachineNotFound
- `Device {name} not in the network scenario.` (no backticks) — `model/Lab.py:268,332`
- `The following devices are not in the network scenario: {machines_not_in_lab}.` — `manager/docker/DockerManager.py:151,155`; `manager/kubernetes/KubernetesManager.py:111,115` — set repr rendered sorted per §0.2; JSON field `machines` (sorted array)
- `Device \`{machine_name}\` not found.` — `manager/docker/DockerManager.py:577`
- `Device {machine_name} not found.` (no backticks) — `manager/kubernetes/KubernetesManager.py:573`

### MachineNotRunning
- `Device \`{machine_name}\` is not running.` — `exceptions.py:100`; raised `manager/docker/DockerMachine.py:669,781,801`, `manager/docker/DockerManager.py:199,203,249,253`, `manager/kubernetes/KubernetesMachine.py:715,823`. Swallowed during k8s undeploy shutdown-commands (`manager/kubernetes/KubernetesMachine.py:689` — preserve the swallow)

### MachineNotReady
- `Device \`{machine_name}\` is not ready.` — `exceptions.py:105`; raised `manager/kubernetes/KubernetesMachine.py:719`

### MachineBinary
- `Binary \`{binary}\` not found in device \`{machine_name}\`.` — `exceptions.py:116` (`__str__`). Raised: `manager/docker/DockerMachine.py:874` (binary = `OCI_RUNTIME_RE` group 3 or 4 of docker `APIError.explanation`), `:890` (same regex over exec stdout), `manager/kubernetes/KubernetesMachine.py:917` (binary = `shlex.join(command)`). `OCI_RUNTIME_RE` (`manager/docker/DockerMachine.py:35`): `OCI runtime exec failed(.*?)(stat (.*): no such file or directory|exec: \"(.*)\": executable file not found)` — port verbatim. Caught-and-warned at `manager/docker/DockerMachine.py:562` (startup) and `:1110` (shutdown) — preserve

### InterfaceMacAddress
- `MAC address {mac_address} on interface \`{interface_num}\` of device \`{machine_name}\` is invalid.` (no backticks on MAC) — `exceptions.py:123`; raised `model/Interface.py:31`

### LinkNotFound
- `Collision domain {name} not found in the network scenario.` (no backticks) — `model/Lab.py:361`
- `Collision Domain \`{link_name}\` not found.` (capital D) — `manager/docker/DockerManager.py:644`
- `Collision Domain {link_name} not found.` (capital D, no backticks) — `manager/kubernetes/KubernetesManager.py:640`
- unreachable in 1.0 (lab.ext): `Collision domain \`{link_name}\` (declared in lab.ext) not found in network scenario collision domains.` — `model/Lab.py:175`

### LinkAlreadyExists
- `Collision domain {name} is already the network scenario.` (typo preserved, §0.2) — `model/Lab.py:378`

### InvalidImageArchitecture
- `Docker Image \`{image_name}\` is not compatible with your host architecture \`{arch}\`` (no trailing period) — `exceptions.py:160`; raised `manager/docker/DockerImage.py:207`

### DockerImageNotFound
- `Docker Image \`{image_name}\` is not available neither on Docker Hub nor in local repository!` — `exceptions.py:165`; raised `manager/docker/DockerImage.py:168`

### DockerPlugin
- `Kathara Network Plugin not found on remote Docker connection.` — `manager/docker/DockerPlugin.py:54`
- `Kathara Network Plugin not enabled on remote Docker connection.` — `manager/docker/DockerPlugin.py:79`
- `Kathara has been left in an inconsistent state! Please run \`kathara wipe\`.` — `manager/docker/DockerMachine.py:424,518` (translated from docker 500 "network does not exist"/"endpoint does not exist")

### KubernetesConfigMap
- `Unable to upload device folder. Maximum supported size: {human_readable_bytes(MAX_FILE_SIZE)}. Current: {human_readable_bytes(tar_data_size)}.` — `manager/kubernetes/KubernetesConfigMap.py:88`

### Syntax
- `Invalid device name \`{name}\`.` — `model/Machine.py:62` (name regex fail; also fires for API-created machines)
- `In {conf_name} - Line {line_number}: {inner}` — `parser/netkit/LabParser.py:65`, where `{inner}` = the re-wrapped `parse_cd_mac_address` message `Invalid interface definition: \`{value}\`.`
- `In {conf_name} - Line {line_number}: Collision domain \`{value}\` contains non-alphanumeric characters.` — `parser/netkit/LabParser.py:71`
- `In {conf_name} - Line {line_number}: \`{line}\`.` — `parser/netkit/LabParser.py:83` (unparseable line)
- `In lab.dep - Line {line_number}.` — `parser/netkit/DepParser.py:67`
- `Invalid interface definition: \`{value}\`.` — `utils.py:468` (raw; reachable via API `connect_machine_to_link`)
- `Interface number in \`--eth {iface_number}:{s}\` is not a number.` — `cli/command/VstartCommand.py:243` (where `s` = `{cd}/{mac}` or `{cd}`)
- unreachable in 1.0 (lab.ext): `In file lab.ext - Line {line_number}.` — `parser/netkit/ExtParser.py:71`

All file variants are golden-tested via spec §9 Layer B parser vectors. Line numbers
are 1-based.

### Value
- `In {conf_name} - Line {line_number}: \`{key}\` is a reserved name, you can not use it for a device.` — `parser/netkit/LabParser.py:55` (`RESERVED_MACHINE_NAMES = ['shared', '_test']`, `utils.py:38`); Go: `labfile.ParseError{Code:"Value"}`
- `Option parameter not valid: {inner}.` — `parser/netkit/OptionParser.py:29` (bad `--option`/`-o` value)
- `\`shared\` folder is a symlink, delete it.` — `model/Lab.py:414` (escapes: the enclosing `except OSError` at :415 does not catch `ValueError`)
- `Invalid \`wait\` value.` — `manager/docker/DockerMachine.py:681,690,786,795`
- `File type {type(file_obj)} not supported` (no trailing period) — `utils.py:441`
- internal-only, no code: bare `ValueError()` at `utils.py:89` (`re_search_fail` signal — always converted at call sites)

### OS (human label `OSError` — covers Python `raise IOError(...)` too, §1.2)
- `No {conf_name} in given directory.` — `parser/netkit/LabParser.py:27` (the `-F`/`--force-lab` fallback trigger — golden-test; re-raised at `cli/command/LstartCommand.py:170` unless `--force-lab`; caught as `(Exception, IOError)` with fallback `Lab(None, path)` in `cli/command/ExecCommand.py:91`, `ConnectCommand.py:74`, `LcleanCommand.py:63`, `LinfoCommand.py:85`)
- `{conf_name} file is empty.` — `parser/netkit/LabParser.py:30`
- `Cannot open {conf_name} file.` — `parser/netkit/LabParser.py:37`
- `Cannot open lab.dep file.` — `parser/netkit/DepParser.py:47`
- unreachable in 1.0: `Cannot open lab.ext file.` (`parser/netkit/ExtParser.py:44`), `lab.ext is only available on Linux systems.` (`cli/command/LstartCommand.py:205`), `External collision domains available only on Linux systems.` (`manager/docker/DockerLink.py:334,369`) — all superseded by `FeatureNotAvailable`

### FileNotFound
- `Unable to find Kathara.` — `cli/ui/utils.py:130` (terminal spawn)
- `Unable to find \`{HOSTTMP_KEY}\` in plugin mounts.` — `manager/docker/DockerPlugin.py:140`
- `Cannot find \`iptables\` in the host.` — `os/Networking.py:209`, **live in 1.0** via the `get_iptables_version` carve-out into `backend/docker`

### FileExists (semantics: path does NOT exist, §0.2)
- `Path \`{path}\` does not exist.` — `utils.py:282,303` (`check_directory_permissions`, unix + windows; volume mount validation)

### NotADirectory
- `Path \`{path}\` must be a directory.` — `utils.py:285,306`

### Permission
- `To mount volume \`{host_path}\` in \`{guest_path}\` you miss the following permissions: \`{', '.join(missing_permissions)}\`.` — `manager/docker/DockerMachine.py:320`; `manager/kubernetes/KubernetesMachine.py:402`

### Connection
- `Docker Image \`{image_name}\` is not available in local repository and no Internet connection is available to pull it from Docker Hub.` — `manager/docker/DockerImage.py:163` (docker 500 with `dial tcp` in explanation)
- `Cannot read Kubernetes configuration.` — `manager/kubernetes/KubernetesConfig.py:41`

---

## 3. Third-party translation table (Go backend must reproduce)

The Go docker/k8s clients surface failures differently than the Python SDKs; these
translations are the contract, keyed by trigger, not by SDK type:

| Trigger (Python site) | Kathara result |
|---|---|
| docker ping/connect failure (`DockerManager.py:49-52,72-73`; incl. `pywintypes.error` on Windows) | `DockerDaemonConnection`, `{message}` = underlying error text |
| docker 500, explanation starts `Mounts denied` (`DockerMachine.py:513-515`) | `MountDenied` (host-drive variant) |
| docker 500, explanation contains `network does not exist` or `endpoint does not exist` (`DockerMachine.py:421-424,516-518`) | `DockerPlugin` (inconsistent-state variant) |
| docker exec failure matching `OCI_RUNTIME_RE` (`DockerMachine.py:871-874,888-890`) | `MachineBinary` |
| docker 500 with `dial tcp` during pull (`DockerImage.py:161-163`) | `Connection` (image variant) |
| docker image-pull failure otherwise (`DockerImage.py:168`) | `DockerImageNotFound` |
| docker plugin not found / not enabled (`DockerPlugin.py:48-54,79`) | `DockerPlugin` |
| docker APIError otherwise re-raised (`DockerMachine.py:386,427,520,876`; `DockerImage.py:152`) | `DockerAPI` passthrough |
| docker APIError during `check_for_updates` (`DockerImage.py:149`) and stream close (`DockerMachine.py:951`) | swallowed (debug log) — preserve |
| k8s ApiException 409 on deployment create (`KubernetesMachine.py:366-368`) | `MachineAlreadyExists` |
| k8s ApiException 403 on namespace create (`KubernetesManager.py:141-143`) | `LabAlreadyExists` |
| k8s ApiException otherwise re-raised (`KubernetesMachine.py:370,837`; `KubernetesManager.py:145`) | `KubernetesAPI` passthrough |
| k8s ApiException swallowed sites (`KubernetesConfigMap.py:52`, `KubernetesLink.py:193`, `KubernetesMachine.py:687`, `KubernetesNamespace.py:37,52`, `KubernetesSecret.py:65`) | swallowed — preserve |
| kube config unreadable (`KubernetesConfig.py:41`) | `Connection` |
| macOS terminal app not found (`setting/Setting.py:288-294`) | `Settings` (terminal-emulator reason) |

---

## 4. Compatibility-critical set (kathara-lab-checker contract)

`kathara-lab-checker` imports and catches **by class name** exactly these, verified by
grep over `/root/kathara/kathara-lab-checker/src/kathara_lab_checker`:

| Exception | Catch sites |
|---|---|
| `MachineNotRunningError` | `checks/BridgeCheck.py:184`, `checks/InterfaceIPCheck.py:58`, `checks/KernelRouteCheck.py:108,118,129`, `checks/applications/dns/DNSAuthorityCheck.py:26`, `checks/applications/http/HTTPCheck.py:53`, `checks/protocols/bgp/BGPNeighborCheck.py:29`, `checks/protocols/evpn/EVPNSessionCheck.py:24`, `checks/protocols/evpn/VTEPCheck.py:23`, `checks/protocols/ospf/OSPFInterfaceCheck.py:28`, `checks/protocols/ospf/OSPFNeighborCheck.py:28`, `checks/protocols/ospf/OSPFRoutesCheck.py:28`, `checks/protocols/scion/SCIONAddressCheck.py:29`, `checks/protocols/scion/SCIONPathsCheck.py:76` |
| `MachineNotFoundError` | `checks/DeviceExistenceCheck.py:20`, `checks/DaemonCheck.py:36`, `checks/IPv6EnabledCheck.py:25`, `checks/CollisionDomainCheck.py:35`, `checks/CustomCommandCheck.py:66`, `checks/SysctlCheck.py:37` |
| `MachineBinaryError` | `checks/protocols/bgp/BGPNeighborCheck.py:32` |
| `MachineCollisionDomainError` | `__main__.py:83` (lab.conf load) |
| `IOError` (= `OSError`) | `__main__.py:78` (lab.conf open) — catches the parser's `OS`-code errors |

**Required attribute/behavior surface** (what the Python client must reproduce):

- **Class identity**: the four Kathara classes above must be importable from
  `Kathara.exceptions` and be distinct classes (the client maps JSON `code` →
  exception class 1:1 using §1's table; the mapping is injective).
- **`str(e)` parity**: every catch site above uses only `str(e)` (verified — no other
  attribute access anywhere in lab-checker), and embeds it in `FailedCheck` reasons;
  the §2 templates are therefore load-bearing beyond the CLI.
- **Attributes, for API parity even though lab-checker does not read them**:
  `MachineBinaryError.binary`, `MachineBinaryError.machine_name` (public `__slots__`,
  `exceptions.py:109-116`) — populated from JSON fields `binary`, `machine`;
  `InvalidImageArchitectureError.image_name`, `.arch` (`exceptions.py:153-157`) — from
  JSON `image`, `arch`; `InvalidImageArchitectureError` must subclass `ValueError`.
- The Go side must therefore always populate the `machine`, `binary`, `link`, `image`,
  `arch` JSON fields per §1 whenever a code in this set is emitted.

---

## 5. Deferred features: `FeatureNotAvailable`

Code `FeatureNotAvailable`, JSON field `feature`, exit 1. Python 3.8.3 never emits it
(it supports these features), so there is no parity constraint on the message; the
messages below are frozen now. The Python client raises
`Kathara.exceptions.NotSupportedError` with the message verbatim.

| `feature` value | Trigger in Go 1.0 | Frozen message |
|---|---|---|
| `lab.ext` | `lab.ext` file present in the scenario directory at lstart/lclean (checked before any root/platform checks, superseding `cli/command/LstartCommand.py:203,205`); external collision-domain attach via API (superseding `manager/docker/DockerLink.py:334-372`) | `lab.ext external links are not supported in this release. Use Kathará 3.8.x.` |
| `linfo` | `kathara linfo` invocation | `The linfo command is not supported in this release. Use Kathará 3.8.x.` |
| `stats-sampling` | resource-sampling fields of `get_machines_stats`/`get_links_stats` (streaming samples). Inventory fields (`name`, `container_name`, `status`, `image`, `user`, `network_scenario_id`) still work per spec §0.3 and are NOT an error | `Resource statistics sampling is not supported in this release. Use Kathará 3.8.x.` |
| `webhooks` | Docker Hub image listing / tag lookup paths (`webhooks/`); the settings-menu image list falls back to the static default list exactly as the Python swallow paths do, without erroring — this code fires only on direct API invocation | `Docker Hub image listing is not supported in this release. Use Kathará 3.8.x.` |

Note "Kathará" carries the accent, per spec §5.3's example.

---

## 6. Partial failures: `errors.Join` and the JSON envelope

**Python observable behavior (the parity target)** — verified in
`analysis/CONCURRENCY.tsv` rows for `DockerMachine.py:178/608/628`,
`DockerLink.py:66/178/202`, `KubernetesMachine.py:197/605`, `KubernetesLink.py:81/152`:
parallel deploy/undeploy runs in chunked thread pools; a chunk completes fully, then
`Pool.map` raises the **first-arriving** exception; sibling exceptions are swallowed;
remaining chunks are never submitted. On the `lab.dep` sequential path the first error
aborts immediately. Either way the CLI prints **exactly one** `CRITICAL ({type}) {msg}`
line and exits 1. Which sibling error surfaced was nondeterministic (completion order).

**Frozen Go semantics:**

1. Workers in a parallel batch run to completion (no context cancellation of siblings
   already started — matches complete-then-raise); no further batches start after a
   failure.
2. All errors collected from the failed batch are combined with `errors.Join`, ordered
   canonically by **machine (or link) name, bytewise ascending** (accepted ruling:
   nondeterministic Python order → canonical sorted order). `errors.Is`/`errors.As`
   traverse the join per stdlib semantics.
3. The **primary error** is the first error in that canonical order.
4. **Human mode**: print the primary error only, as `CRITICAL ({human_label}) {message}`,
   exit 1 — one line, matching Python's observable single-error output.
5. **JSON mode**: the envelope's `error` object is the **primary error**. When more
   than one error was collected, a sibling key `errors` is added: an array of error
   objects (same shape as `error`: `code`, `message`, plus per-code fields) listing
   **all** collected errors in canonical order, primary included as element 0:

   ```json
   {"error":{"code":"MachineBinary","message":"Binary `frr` not found in device `pc1`.","binary":"frr","machine":"pc1"},
    "errors":[
      {"code":"MachineBinary","message":"Binary `frr` not found in device `pc1`.","binary":"frr","machine":"pc1"},
      {"code":"DockerAPI","message":"...","..."  : "..."}
    ]}
   ```

   `errors` is **absent** for single-error failures (the common case and the only case
   Python could express). Consumers that only read `error` observe exactly the Python
   behavior, made deterministic. The Python client raises the exception mapped from
   `error` (primary) and ignores `errors` in 1.0.
6. Exit code 1 in both modes.

---

## 7. Exit-code mapping (frozen)

Verified against `src/kathara.py`, `cli/command/ExecCommand.py:116`,
`cli/command/CheckCommand.py:77-79`.

| Condition | Exit code | Notes |
|---|---|---|
| Success | 0 | |
| `--version` | 0 | prints `Current version: {version}` |
| Any error rendered through the taxonomy (human or JSON mode) | 1 | Python catch-all: `CRITICAL ({type}) {msg}`; JSON: `{"error":{...}}` on stdout |
| Startup settings check failure (`Settings`, `DockerDaemonConnection`) | 1 | same rendering; emitted before command dispatch (skipped when the command is `settings`) |
| Unknown command | 1 | `Unrecognized command \`{cmd}\`.` (logging.error) + help text; **no JSON envelope** (usage-level, matches Python); `ClassNotFound` code stays reserved |
| No command / `-h` paths | 1 / 0 | help printed; argparse `-h` exits 0, bare invocation exits 1 |
| Flag/argument usage errors (argparse → cobra) | 2 | usage + error on stderr; includes the `argparse.ArgumentTypeError` validators (`cli/ui/utils.py:210,226,229,242`); not part of the JSON error contract |
| `kathara exec` | remote command's exit code | passthrough (`ExecCommand.py:116`); in `jsonl` mode also carried in the `{"type":"exit","code":N}` event |
| `kathara check` self-test failure | 1 | `CheckCommand.py:77` |
| Ctrl-C / SIGINT | **0** | accepted ruling, warning parity: print warning `You interrupted Kathara during a command. The system may be in an inconsistent state! If you encounter any problem please run \`kathara wipe\`.` **unless** the command is one of `exec`, `linfo`, `list`, `settings` (Python's list, preserved verbatim even though `linfo` is a stub) or the port-new `config` (pinned in `JSON_CLI_CONTRACT.md` §6.2). Human mode: warning via logging on stdout (Python parity); `json`/`jsonl`: warning on stderr. `json` mode stdout: the interrupt envelope `{"interrupted":true}` — no error envelope; `jsonl`: terminal event `{"type":"interrupted"}`, no `exit` event (`JSON_CLI_CONTRACT.md` §6.2); exit 0 |

`InternalError`-coded failures exit 1 like everything else.

---

## 8. Corrections applied to the draft `analysis/ERROR_CODES.tsv`

All draft rows were re-verified against source; raise-site line numbers, catch sites
and templates checked above are confirmed accurate except as noted:

1. **Rows 56/57 — `IO` and `OS` merged into one code `OS`** (draft assigned separate
   `IO` code). In Python 3, `IOError` **is** `OSError`; `type(IOError("x")).__name__`
   is `OSError`, so the observable human label for every parser `raise IOError(...)`
   is `(OSError)`, and two codes cannot both round-trip through human-label parity.
   Registry has a single `OS` code, human label `OSError` (§1.2, §2). The draft's
   separate `IO` code does not exist in this registry.
2. **TSV header rendering rule corrected**: header said Go human mode reproduces
   `"({Code}) {msg}"`. The Python line prints the **class name** (`MachineNotFoundError`),
   not the code (`MachineNotFound`). Frozen rule: `({human_label}) {message}` with
   `human_label` = Python class name (§0.1). The header also called it "the observable
   stderr line": Python's `RichHandler` logging writes to **stdout**; corrected in §0.1.
3. **Rows 47/48 (`TestError`, `MachineSignatureNotFoundError`)** — draft said "reserve
   no code". Spec §4.3 requires the class→code mapping to be 1:1 and exhaustive;
   codes `Test` and `MachineSignatureNotFound` are RESERVED-DEAD (never emitted,
   never ported) instead of unassigned (§1.1).
4. **Row 58 (`FileNotFound`)** — draft marked `os/Networking.py:209`
   (`Cannot find \`iptables\` in the host.`) DEFERRED. The accepted
   `get_iptables_version` carve-out into `backend/docker` makes it **live in 1.0** (§2).
5. **Rows 73/78 (docker APIError / k8s ApiException passthrough)** — draft left
   untranslated `raise e` passthroughs codeless ("wrapped"). They are user-reachable
   (`(APIError) ...` in today's human output), so they get stable codes `DockerAPI` and
   `KubernetesAPI` with human labels `APIError`/`ApiException` instead of falling
   into `InternalError` (§1.3).
6. **Row 38 (`MachineOption` ulimit templates) clarified**: `{name}` in all three
   ulimit messages is the literal string `ulimit` (option name), not the machine or
   ulimit key — verified at `model/Machine.py:205-238`; the frozen templates spell it
   out (§0.2, §2). Also pinned from source: the volume-mode message's trailing space
   and `ALLOWED_VOLUME_MODES = ["ro", "rw", "rx"]`.
7. **Rows 28/45/54/57 deferred-site messages** marked "superseded by
   `FeatureNotAvailable`" (draft kept them merely as raiser variants): the lab.ext /
   external-CD `PrivilegeError`, `LinkNotFoundError`, `SyntaxError` and `OSError`
   variants are unreachable in 1.0 because the feature stub errors first (§5).
8. **Row 68 (`io.UnsupportedOperation`)** — draft mapping "→ Invocation" confirmed and
   frozen with the exact message now listed under `Invocation` (§2); note
   `io.UnsupportedOperation` subclasses both `OSError` and `ValueError` in Python, but
   the call site is API misuse, hence `Invocation` by call-site semantics.

Everything else in the draft TSV (raise sites, catch sites, message variants,
lab-checker sites, exit-code legend) was verified correct and is incorporated above
unchanged.

---

*Frozen for Phase 0. Changes require a new accepted ruling recorded in the port log.*
