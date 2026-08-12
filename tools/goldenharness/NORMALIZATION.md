# Layer A determinism rules

Every transformation the golden harness applies to a recording, and the source
of nondeterminism it exists to remove. Nothing may be normalized without an
entry here: a normalization with no named nondeterminism source is a
correctness assertion being silently deleted.

Sources referenced below:

- `docs/port/ORDERING.tsv` — the per-construct ordering audit. Rows whose
  `go_strategy` column starts with `!!` are the sites where the Python is
  *actually* nondeterministic (as opposed to merely order-sensitive).
- The MAC-derivation finding (`PROPOSED-DIVERGENCES.md`, "Deterministic MACs
  via kathara.machine driver opt"): `PORT_SPEC.md` §9 claims Kathara derives
  interface MACs as `md5("<machine>-<iface>")`. That is **empirically false**
  for Kathara 3.8.3 with the `kathara/katharanp_vde` plugin. The plugin
  (`NetworkPlugin` `vde/go-src/src/katharanp.go:123`) only applies the
  deterministic derivation when the driver opts `kathara.machine` **and**
  `kathara.iface` are both present; `DockerMachine._create_driver_opt` sends
  `kathara.iface` + `kathara.link` and never `kathara.machine`, so the plugin
  falls back to `md5(NetworkID-EndpointID)` — a fresh random MAC per deploy.
  Two deploys of the same lab were observed to produce different `eth0` MACs.

---

## 1. Process environment

| Rule | Why |
|---|---|
| Child processes inherit only `PATH`, `HOME`, `USER`, `LOGNAME`, `DOCKER_*`, `XDG_CONFIG_HOME`, `SSL_CERT_*`. | An inherited `COLUMNS`, `TERM`, locale or `PYTHONHASHSEED` would silently change the recording. Whitelisting makes the environment part of the harness, not of the operator's shell. |
| `COLUMNS=80`, `LINES=24` are forced. | `rich` lays its panels and tables out to the console width. Without a pin, the same command records differently in a 100-column terminal and in CI. 80 is `rich`'s own non-TTY default. |
| `TERM=dumb`, `NO_COLOR=1`. | Removes the TTY-dependent colour path entirely instead of relying on ANSI stripping to undo it. ANSI stripping still runs, as belt and braces, for a binary under test that colours unconditionally. |
| `LC_ALL=C.UTF-8`, `LANG=C.UTF-8`. | Locale changes collation and number/message formatting. |
| `PYTHONHASHSEED` is **not** set. | Deliberate. Setting it would hide the very set-iteration nondeterminism that rules 5.2 and 5.3 exist to normalize, and the Go build has no equivalent knob. Recordings must survive a randomly seeded oracle. |
| `HOME` is overridden to a harness-owned directory containing a pinned `.config/kathara.conf` (the stock 3.8.3 defaults, `image_update_policy` forced to `Never`, `last_checked` pinned far in the future). | Three sources at once. (1) The operator's real `kathara.conf` is mutable state: an edited `debug_level` or `image` would silently change every recording. (2) `Setting.check()` phones GitHub for a release check when `last_checked` is a week old, prints a three-line banner when a newer release exists, and rewrites the settings file — time-, network- and release-state-dependent. (3) `image_update_policy "Prompt"` makes every `lstart` fetch each image's registry digest; an upstream push turns the run into a confirmation prompt on stdout. `Never` suppresses the prompt and the pull, so recordings are independent of registry state. The binary under test still resolves `~` itself, so the Go build is subject to the same pinning. **Caveat, found 2026-08-12: on Linux this pinning does not reach the settings file at all.** `utils.get_current_user_home()` (`utils.py:212`) picks the `passwd_home` arm on Linux and returns `pw_dir` from the passwd database — `$HOME` is not consulted — and `Setting.DEFAULT_SETTINGS_PATH` (`Setting.py:36`) is computed from it. `internal/util/home_linux.go` ports that faithfully, so **both** implementations read the invoking account's real `~/.config/kathara.conf` and ignore the harness copy. The pinned file still matters on macOS/Windows (`expanduser('~')`, which honours `$HOME`), and the `HOME` override still governs `/hosthome`-adjacent paths and anything else that reads the environment. Until the harness can point Kathara's settings path directly, **the operator's real `~/.config/kathara.conf` is part of the harness contract on Linux** and must carry `image_update_policy: "Never"` and a far-future `last_checked`. This was not academic: an upstream push of `lscr.io/linuxserver/wireshark` turned the three labs that use it (`05-two-computers`, `06-basic-ipv6`, `11-capture-packets`) into an EOF-on-confirmation-prompt failure under the real file's stock `"Prompt"`. |
| stdin is an empty reader, never inherited. | A confirmation prompt (image update, volume mount) must see EOF and take its default path instead of blocking until the scenario timeout. |
| Working directory is the repository root; the lab is always passed with `-d <abs path>`. | `Machine.add_meta("volume", ...)` calls `os.path.abspath` against the process cwd, and `lstart` falls back to cwd when `-d` is absent. Pinning both makes the recording independent of where the harness was invoked. |

## 2. Path, identity and hash tokens

Recorded strings never contain a host-specific absolute path or identifier.
Literal substitutions are applied longest-first, so a container name is
tokenized through its components rather than as a whole.

| Token | Replaces | Why |
|---|---|---|
| `<LABDIR>` | The lab directory, both as given and as `realpath` resolved. | The golden must not encode where `Kathara-Labs` or this repository is checked out. `lstart` resolves `-d` through `os.path.realpath`, so both spellings can appear. |
| `<HOME>` | The invoking user's home directory. | `/hosthome` mounting and log messages embed it. |
| `<LABHASH>` | The lab hash, both the harness-derived value and every value observed in a `lab_hash` label. | `Lab.hash = generate_urlsafe_hash(realpath)`, or of `LAB_NAME` when the lab declares one. It is deterministic but path- and name-dependent, so it must not be frozen into a golden. What *is* asserted is `scenario.json.lab_hash_derived_ok`: the harness recomputes the hash itself (`GenerateURLSafeHash`, a direct port of `utils.generate_urlsafe_hash`) and records whether the binary under test agrees. |
| `<USER>` | The Kathara user slug. | `utils.get_current_user_name()` is `slug("<username>-<urlsafe md5 of platform.node()>")` — it depends on the machine's hostname. Re-derived and asserted the same way (`user_slug_derived_ok`). |
| `<CID>`, `<CID12>` | Container ids, full and 12-character. | Assigned by the daemon per run. They leak into `/etc/hosts` and into DNS names. |
| `<ANONVOL>`, `<ANONVOL_PATH>` | The `Name` and `Source` of a mount whose type is `volume` and whose name is a 64-hex id. | The `kathara/base` image declares `VOLUME /hosthome`, so every device gets a fresh anonymous volume with a random id. Recorded structurally (by mount type and name shape) rather than by a blanket hex-pattern rewrite, because a blanket rewrite would also destroy the sha256 digests in the file-tree probe. |
| `<KATHARA>` | The binary under test, in every recorded `argv`. | The whole point of `KATHARA_CMD` is that the same goldens replay against the Python oracle and the Go build. Recording `/root/kathara/pyvenv/bin/python -m kathara` in `commands.json` would guarantee a diff on the first Go run. |
| `<IFINDEX>` | The number in the `@if<n>` peer suffix that `ip link` renders for a veth whose peer sits in another network namespace, e.g. `24-pox-arp-handler`'s `controller` recording `eth1@if451`. **`ip -br link` output only.** | The peer's ifindex is a host-global counter over every interface ever created on the box, so two recordings on the same host differ (451 vs 492 was the observed pair). `ip -j addr`'s equivalent `link_index` key is dropped outright by rule 7; this is the same fact in `ip link`'s text rendering. The `@if` marker itself is **kept**: that a `bridged` device's second interface is one end of a cross-namespace veth — as opposed to a `katharanp_vde` interface, which has no peer suffix — is a real assertion. Scoped to the one probe that can produce the suffix so it can never touch a lab-configured string. |
| `<DOCKERIP>`, `<DOCKERIP6>` | The `IPAddress` and `GlobalIPv6Address` that `docker inspect` reports for an endpoint on a **non-Kathara** network, registered as literals before any text is rendered. Additionally, in `/etc/hosts` only, any address in `172.16.0.0/12` or `192.168.0.0/16`. | A `bridged` device is also attached to the default `bridge` network and receives an address from Docker's allocation pool. The address depends on daemon-wide allocation order across every container that ever ran, not on the lab: `05-two-computers`'s `wireshark` records `... scope link src 172.17.0.2` in `ip route`, and a host that already had a container on the bridge would record `.3`. Tokenizing the *observed* address rather than the whole private-address space is what keeps lab-configured addresses — including the labs that legitimately use `172.16.0.0/12` — byte-exact in `ip addr` and `ip route`. The gateway and subnet (`172.17.0.1`, `172.17.0.0/16`) are daemon configuration, not run state, and are deliberately left exact. The blanket `/etc/hosts` rule is kept as belt and braces. |

## 3. MAC addresses

Following the MAC ruling above, `PORT_SPEC` §9's "use MACs aggressively"
instruction is **inverted** for the default path:

1. Before any text is rendered, the harness collects the set of MACs that the
   lab pinned explicitly, i.e. the values of the `kathara.mac_addr` driver opt
   observed in `docker inspect`. That opt is only set when `lab.conf` uses the
   `machine[N]="cd/mac"` syntax.
2. Every MAC-shaped token in every recorded string is replaced with `<MAC>`
   **unless** it is in that set, or is one of the two constants
   `00:00:00:00:00:00` (loopback) and `ff:ff:ff:ff:ff:ff` (broadcast), which
   carry no identity. A MAC-shaped match directly adjacent to a `:` is the
   interior of a longer colon-hex run — an IPv6 address such as
   `2001:db8:aa:bb:cc:dd:ee:ff`, never a MAC — and is left byte-exact: a lab
   that configures such an address must stay a real assertion.
3. `containers.json` records `mac_address` **only** for endpoints that carry
   `kathara.mac_addr`, together with an assertion that the effective MAC equals
   the requested one.
4. Only **EUI-64-shaped** IPv6 link-local addresses
   (`fe80::X:Xff:feX:X`, the kernel's default derivation from the interface
   MAC) are replaced with `<LINKLOCAL6>`, and among those the ones derived
   from an explicitly pinned MAC are **kept**: they are as deterministic as
   the MAC itself. Statically configured link-locals — `basic-ipv6`'s
   `fe80::1` / `fe80::2`, used both as addresses and as route gateways — never
   match the pattern and stay byte-exact. (An earlier revision scrubbed all of
   `fe80::/10`, which deleted those assertions and would have let a port that
   swapped two static link-locals pass.) Residual risk, accepted: a lab that
   statically configures an address that happens to be EUI-64-shaped would be
   over-scrubbed; the corpus contains none.

What replaces the deleted assertion is the wiring itself, which *is*
deterministic. For every endpoint the harness records and asserts:

- `kathara.iface` is present and numeric (the interface number),
- `kathara.link` is present (the collision-domain name),
- `kathara.link` equals the `name` label of the Docker network the endpoint is
  attached to.

A violation is a hard scenario failure, not a diff.

The `bridged` option attaches a device to Docker's **default bridge network**
as well. That endpoint is created by plain Docker and carries none of the
`kathara.*` driver opts, so the three assertions above do not apply to it —
`05-two-computers`'s `wireshark` device has no collision domain at all and is
bridged only. Endpoints are therefore tagged `kathara_network` and the wiring
assertions are scoped to the ones that are. The bridge attachment is not
thereby unasserted; what is asserted instead is:

- an endpoint on a non-Kathara network exists **iff** the container carries the
  `bridged_iface` label, which `DockerMachine.py:355` sets exactly when the
  device declared `bridged`,
- that endpoint carries **no** `kathara.iface` or `kathara.link`.

## 4. Container and network naming

The harness re-derives the expected names
(`<device_prefix>_<user>_<device>_<lab_hash>` and
`<net_prefix>_<user>_<cd>_<lab_hash>`, the `NOT_SHARED` forms selected by the
default `shared_cds` setting) and records `container_names_ok` /
`network_names_ok`. The names themselves appear only in tokenized form, so the
snapshot survives a repository move while the derivation stays asserted.

## 5. Ordering

### 5.1 Structures whose Python order *is* deterministic

These are recorded in observed order, never sorted, because their order is part
of the contract and a reordering by the Go port is a real defect:

| Recorded field | ORDERING.tsv row |
|---|---|
| `containers[].env` | `Machine.py:200` — `meta['envs']` dict insertion feeds the Docker `Env` list. |
| ~~`containers[].cap_add`~~ | `DockerMachine.py:347` region — a fixed `MACHINE_CAPABILITIES` list literal. **Withdrawn**: the Go Docker SDK reorders the list client-side, so its stored order is not observable from Kathara code on both sides. Now canonicalized (set assertion) — section 10. |
| `containers[].cmd`, `containers[].entrypoint` | image/`args` metadata, source order. |
| `commands[].stdout` / `stderr` line order | Whatever plain lines survive rule 6 keep their order (panels, log records, `✓` check lines). Note this does **not** assert deploy submission order: the per-device deploy lines live inside the progress bar rows that rule 6.6 drops, and completion order under the deploy pool is nondeterministic anyway (`DockerMachine.py:178`). Submission order is asserted only where a scenario makes it observable as a side effect — `syn-lab-dep`'s `shared/boot-order.txt`. |
| `probes[].startup_logs` line order | `DockerMachine.py:546` — `"; ".join(STARTUP_COMMANDS)` plus the `exec_commands` interleave; `/var/log/startup.log` is the observable of that order. |

### 5.2 Structures sorted because Python's order is genuinely random

| Recorded field | Normalization | ORDERING.tsv row |
|---|---|---|
| `containers[].endpoints[].endpoint_sysctls` | The `com.docker.network.endpoint.sysctls` driver-opt string is split on `,` and sorted. | `DockerMachine.py:470` (`!!`): built by `",".join(sysctl_opts)` over a Python **set** of strings, whose iteration order is hash-randomized per process. `DockerMachine.py:441` (`!!`) feeds it from another set. This is the single normalization `ORDERING.tsv` explicitly instructs Layer A to perform. |

### 5.3 Structures sorted because the *observation* has no defined order

Sorting here does not delete an assertion; the underlying API never promised an
order in the first place.

| Recorded field | Sort key | Why |
|---|---|---|
| `containers[]` | device name | `docker inspect` returns containers in the order the ids were passed, which comes from `docker ps -aq` (creation order, descending). Creation order is thread-completion order under Kathara's deploy pool — inherently nondeterministic (`DockerMachine.py:178`) and equally so in Go. Deploy *submission* order is asserted through `commands[].stdout`, not through this array. |
| `networks[]` | collision-domain name | Same, via `DockerLink.py:66`. |
| `containers[].sysctls` | `"k=v"` string | The daemon returns `HostConfig.Sysctls` as a JSON object; object key order is not meaningful. Kathara's own merge precedence (`DockerMachine.py:295`) is a semantic, not an ordering, property and survives sorting. |
| `containers[].ulimits` | ulimit name | Recorded from a JSON array the daemon rebuilds; the Python insertion order (`Machine.py:236`) is asserted by *membership and values*, which sorting preserves. |
| `containers[].mounts` | destination, then source | `HostConfig.Binds` order (`DockerMachine.py:305`) is asserted by the set of mounts and their modes; the `Mounts` array the daemon reports is a rebuilt view, not the input list. |
| `containers[].endpoints[]` | `kathara.iface`, then network name | `NetworkSettings.Networks` is a JSON object. Sorting by the interface number recovers the ordering that actually matters (`Machine.py:67`, `DockerMachine.py:525`) instead of relying on map order. |
| `probes[].ip_br_link` | whole line | Kernel dump order follows `ifindex`, which is host-global and depends on every interface ever created on the box. |
| `probes[].ip_addr` | `ifname`; `addr_info` entries by canonical JSON | Same reason; `addr_info` is additionally sorted because SLAAC addresses can appear asynchronously. Other nested arrays, notably `flags`, keep kernel order — that order is deterministic and worth asserting. |
| `probes[].ip_route` | whole line | Kernel route dump order is not specified. Only enabled for scenarios whose routing table is fully static (see the manifest); labs running FRR/Quagga converge nondeterministically and record static facets only. |
| `probes[].fs_trees[]` | path | `find` output is `readdir` order. It is already sorted inside the container under `LC_ALL=C` and re-sorted here. Matches `ORDERING.tsv` `Machine.py:392`: "walk in sorted order; extracted tree identical; do NOT golden-compare raw tar bytes" — which is exactly why the file tree is recorded as paths plus sha256 of contents rather than as tar bytes. |
| `teardown.kathara_networks` | name | Must be empty; sorted so a failure diff is stable. |

## 6. Text stream normalization

Applied in this order to every captured stdout/stderr:

1. **OSC, CSI and single-character ANSI escapes are stripped.** `rich`
   suppresses colour on a non-TTY, but the binary under test may not, and the
   escape bytes carry no behavioural information.
2. **CRLF folded to LF.** Windows recordings must be comparable with Linux ones.
3. **Log records unwrapped to logical lines.** `RichHandler` renders a log
   record as a level column (8 characters plus a space) and the message
   word-wrapped into the remaining width, continuation rows indented 9 spaces,
   every row padded to the console width. The wrap points depend on the
   rendered length of everything in the message **before** tokenization — a
   host path such as the lab directory wraps at a host-specific column, so the
   wrapped form can neither match its literal token nor compare across hosts
   (`err-nonexistent-dir` recorded `<HOME>/kathara/kathara-go/...` split
   mid-path before this rule existed). A line matching the exact padded level
   column (`CRITICAL `, `ERROR    `, …) absorbs its continuation rows. The
   separator at each break follows rich's own wrap rules: rich folds at spaces
   and hard-chops only a word that cannot fit within the fold width (console
   width minus the 9-column gutter) on a row of its own, so a break is
   rejoined with no separator only when the previous row is filled to the
   console width exactly **and** the fragments on either side of the break
   form a single word longer than the fold width; every other break — word
   folds, including those landing exactly on the console width (syn-env's
   WARNING folds at column 80 after "meta"), and embedded newlines — is
   rejoined with a single space. The assertion this
   deletes is rich's wrap layout (and the space-vs-newline distinction inside
   a message); the assertion it preserves — and makes renderer- and
   host-independent — is the full message text. Panels are untouched: their
   content never embeds host paths, so their 80-column layout stays byte-exact.
4. **Literal token substitution** (section 2), longest source first.
5. **MAC and link-local scrubbing** (section 3).
6. **Progress-bar lines dropped.** Any line containing one of the `rich` bar
   glyphs `━ ╸ ╹ ╺ ╻` (U+2501, U+2578–U+257B) is removed. These lines are
   `[Deploying devices] ━━━━━ 3/3`: the bar width is a function of console
   width and of how many redraws the run happened to emit. The panel borders
   use a different block (U+2500, U+2502 and corners) and are **kept**, so the
   lab metadata panel remains a byte-exact assertion. Deploy counts are not
   lost: `containers.json` and `networks.json` record what was actually
   created.
   The same rule drops any line containing a glyph from the braille block
   (U+2800–U+28FF), which is `rich`'s **spinner** column. Which spinner frame
   is on screen is a function of elapsed time, so it differs between two runs
   of the same scenario. The spinner survives the bar rule only when the
   progress display is torn down before a bar is ever drawn, i.e. on a fast
   failure: `syn-volume`'s pre-fix recording ended with
   `[Deploying devices] ⠋ … 0/1`.
7. **Image-pull progress dropped.** Lines matching `^[0-9a-f]{12}: ` (per-layer
   progress) and the `Downloading` / `Extracting` / `Pull complete` /
   `Verifying Checksum` / `Waiting` / `Already exists` / `Digest:` / `Status:`
   families. These depend on layer count, network speed and what the local
   image cache already had. The `Pulling image \`X\`...` log line is
   deliberately **kept**: it means the image was missing, which is a real
   signal about the run and not progress noise.
8. **Trailing whitespace trimmed per line; trailing blank lines removed.**
9. The result is stored as a JSON array of lines, so a diff points at a line
   rather than at a byte offset.

## 7. `ip -j addr` field filtering

Dropped keys, all host-global or time-derived:
`ifindex`, `link_index`, `link_netnsid`, `altnames` / `alt_names`,
`valid_life_time`, `preferred_life_time`, `parentbus`, `parentdev`,
`tentative`, `optimistic`, `dadfailed`.

The last three are the Duplicate Address Detection state machine. An address is
`tentative` from assignment until DAD completes — one solicitation and a
timeout, of the order of a second — and `lstart` returns as soon as the
containers are up (`DockerMachine.py:555` dispatches the startup commands with
`detach=True`), so the probe races DAD. `06-basic-ipv6` recorded `pc1` and
`pc3` tentative and `pc2` not, within one run. DAD's *outcome* is still
asserted: a duplicate address would leave `dadfailed` behind **and** be missing
from the traffic path, and the address itself stays byte-exact.

Dropped `addr_info` **entries**: any whose `protocol` is `kernel_ra`.

That is SLAAC — an address that exists only once a Router Advertisement has
arrived. `06-basic-ipv6`'s `pc1`..`pc3` ship no `.startup` at all and are
addressed entirely by `radvd` on `r1`/`r2` (`MinRtrAdvInterval 3`,
`MaxRtrAdvInterval 9`), and the kernel delays its own Router Solicitation by a
random interval of up to one second (RFC 4861 §6.3.7). Two recordings of the
same lab disagree on whether `2001::3:200:ff:fe00:3` is there yet — observed,
not hypothesised. This is the same class as a BGP- or OSPF-learned route
(`GOLDEN_CANDIDATES.md`'s determinism caveat) and is dropped for the same
reason: daemon-learned state is not a golden. It is dropped **by protocol**,
not by address shape, so a statically configured address in the same prefix
would still be compared exactly.

What that lab still asserts, and what a port cannot break without failing:
`containers.json` carries the `sysctl net.ipv6.conf.eth0.accept_ra=2` entry and
the endpoint's `kathara.mac_addr`, and `ip -br link` carries the pinned MAC
byte-exact. Every statically configured address survives, including
`basic-ipv6`'s `fe80::1` / `fe80::2` and the EUI-64 link-locals derived from
pinned MACs (section 3.4).

Everything else — `ifname`, `flags`, `mtu`, `qdisc`, `operstate`, `group`,
`txqlen`, `link_type`, `broadcast`, and every remaining `addr_info` entry's
`family`, `local`, `prefixlen`, `scope`, `protocol`, `label` — is kept and
compared exactly.

## 8. Host-state hygiene

Not normalization, but part of what makes a recording reproducible.

- **Pre-flight.** A scenario refuses to start while any `app=kathara`
  container or network exists. State left by a previous run would be
  attributed to this one. The harness force-removes what it finds and re-checks.
- **Serialization.** Scenarios run strictly one at a time. They share one
  Docker daemon, one lab-hash namespace and one set of network names.
- **Timeout.** Each scenario has a wall-clock limit (240 s default, per
  scenario override in the manifest). `lclean` and the post-teardown check run
  on a *separate* budget so that a hung `lstart` is still torn down.
- **Unconditional cleanup.** On every exit path — success, assertion failure,
  error, timeout, SIGINT — the harness force-removes every `app=kathara`
  container and network.
- **`shared/` hygiene.** Kathara creates `<lab>/shared` and bind-mounts it
  read-write into every device. Some corpus labs ship a populated `shared/`
  in git (`main-labs/p4/05-*`, `application-level/dns`), so it cannot be
  deleted blindly. The harness records whether the directory existed before
  the run and removes it afterwards only if it created it. Scenarios that use
  `/shared` as an observation channel additionally declare `reset_files` so
  that append-style markers cannot accumulate across recordings.
- **`volume` sources.** `utils.check_directory_permissions` raises
  `FileExistsError` when a `volume` host path is missing, so a lab that mounts
  one records either the mount or that error depending on host state the
  harness does not own. Scenarios declaring `volume` list their sources under
  `host_dirs`; the harness creates them before the run and removes the ones it
  created, exactly as it does for `shared/`. Without this, `syn-volume`
  recorded a `CRITICAL (FileExistsError)` and deployed nothing.
- **Settling.** `lstart` returning does **not** mean the lab has stopped
  moving: Kathara dispatches the startup commands with `detach=True`
  (`DockerMachine.py:555`), and even once they have run, a kernel state
  machine they started can still be converging. `settle_seconds` in the
  manifest delays *all* observation — `docker inspect` and every in-container
  probe — so that a scenario which needs it records the converged state rather
  than a random point on the way there. It is opt-in per scenario and defaults
  to 0; a global delay would change what every existing golden records, for the
  benefit of the one or two that need it.

  The case that motivated it: `10-one-bridge`'s `b1.startup` creates a Linux
  bridge and enslaves `eth0`..`eth3` to it. A bridge has no carrier until a
  port does, and the propagation is asynchronous, so `mainbridge` passes
  through `DOWN <NO-CARRIER,…>` on its way to `UP <…>`. The window is short
  enough that a single probe pass straddled it: `ip -br link` recorded
  DOWN/NO-CARRIER and `ip -j addr`, a moment later, recorded `operstate: UP`.
  Note that this is *not* a normalization — no assertion is deleted. The
  bridge's converged link state stays byte-exact, and a port that failed to
  enslave, or a bridge that never came up, still fails the golden.

- **`teardown.json` asserts emptiness.** A non-empty `kathara_containers` or
  `kathara_networks` after `lclean` is a scenario failure, not a diff.

## 9. Deliberately *not* normalized

| Kept exact | Rationale |
|---|---|
| Exit codes. | The whole error taxonomy (`PORT_SPEC` §4.3) is observable here. |
| The `rich` metadata panel and its box-drawing borders. | Deterministic once `COLUMNS` is pinned. A padding change in the Go port's `lipgloss` rendering is a real user-visible difference and should fail. |
| sha256 of every file in `/hostlab` and `/shared`. | This is the assertion that `pack_data` shipped the right bytes. |
| `containers[].labels`, minus tokenized values. | `name`, `lab_hash`, `user`, `app`, `shell`, `bridged_iface` are the contract the manager uses to find its own objects. |
| `networks[].external`. | `lab.ext` is deferred, and `DockerLink.py:145` requires the label to be the empty string in 1.0. Recording it pins that. |
| `port_bindings` and `exposed_ports`. | Daemon-normalized maps; key order is handled by canonical JSON. |

## 10. Container capabilities (`cap_add` / `cap_drop`)

Both lists are recorded in **canonical form**: each entry upper-cased and given
a `CAP_` prefix unless it already has one or is the `ALL` magic value, then
de-duplicated and sorted (`NormalizeCapabilities`, `normalize.go`). The
assertion this leaves is the *set* of capabilities, not their spelling or their
order.

**Why.** The Go Docker SDK rewrites both lists in the client, before the
request leaves the process:

```go
// github.com/docker/docker@v28.5.2/client/container_create.go:72
hostConfig.CapAdd = normalizeCapabilities(hostConfig.CapAdd)
hostConfig.CapDrop = normalizeCapabilities(hostConfig.CapDrop)
```

`normalizeCapabilities` (`:139`) de-duplicates and `sort.Strings`-es;
`normalizeCap` (`:159`) upper-cases and prefixes, special-casing the constant
`allCapabilities = "ALL"` (`:132`). The rewrite is unconditional — no API
version gate, no opt-out, and the SDK exposes no hook to suppress it.

`docker-py` does no such thing: it puts `MACHINE_CAPABILITIES` on the wire
exactly as the Python literal spells it — bare names, source order. The daemon
stores whichever form it was sent and `docker inspect` echoes that form back.
So the same Kathara lab yields:

| | `HostConfig.CapAdd` as `docker inspect` reports it |
|---|---|
| Python (docker-py) | `["NET_ADMIN","NET_RAW","NET_BROADCAST","NET_BIND_SERVICE","SYS_ADMIN"]` |
| Go (docker SDK) | `["CAP_NET_ADMIN","CAP_NET_BIND_SERVICE","CAP_NET_BROADCAST","CAP_NET_RAW","CAP_SYS_ADMIN"]` |

The resulting container is identical. The daemon resolves both spellings to the
same kernel capability and the bounding set is order-independent, so nothing an
operator can observe *inside* the container differs; only the daemon's echo of
what it was told differs.

**Ruling.** Canonicalize in the harness rather than chase byte-equality. A
byte-exact golden here would assert a property of the client library, not of
the port, and could only be satisfied by forking or bypassing the SDK — a real
cost for zero semantic gain. The divergence is recorded in `DIVERGENCES.md`.

**What is still asserted.** Membership and cardinality: a missing
`NET_ADMIN`, a stray `SYS_PTRACE`, an empty list where five capabilities were
due, or the `privileged` path's empty `cap_add` (Kathara passes `cap_add=None`
when `privileged` is set — `syn-privileged`) all still fail the golden. Only
letter-case, the `CAP_` prefix and list order are conceded. Note that the
canonical form is a fixed point, so re-recording a migrated snapshot does not
move it, and the `cap_drop` field stays `omitempty` — an absent list normalizes
to `null`, never to `[]`.

The 47 stored goldens were migrated mechanically to canonical form (a pure
transform of the recorded arrays, no re-recording) and re-verified against the
Python oracle at 47/47.
