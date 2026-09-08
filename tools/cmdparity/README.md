# cmdparity — command-flow parity harness

The Layer A golden suite (`tools/goldenharness`) exercises `lstart`, in-container
probes and `lclean`. `PORT_SPEC.md` §11's merge gate additionally demands that
`vstart` / `vconfig` / `vclean` be byte-identical to the Python implementation,
and `lconfig`, `lrestart`, `wipe`, `list` and `exec` have no behaviour golden at
all. This tool closes that gap.

For each **flow** — a fixed argv sequence over a fixed scratch scenario — it
drives **both** implementations through the identical steps and records, after
every step:

- the exit code,
- normalized stdout and stderr,
- a normalized snapshot of the Kathara-owned Docker state (containers,
  networks, mounts, driver opts, dangling volume count).

Each flow is run twice per implementation. Comparing run 1 against run 2 of one
implementation is the **determinism** check; comparing Python against Go
run-for-run is the **parity** check.

## Running

```sh
go build -o /tmp/kgo ./cmd/kathara            # from the repo root
cd tools/cmdparity
go run . -record -compare                     # all flows, both impls, 2 runs
go run . -record -flow exec                   # one flow
go run . -compare                             # diff what is already recorded
go run . -record -compare -columns 80         # golden-harness geometry
```

Recordings land in `recordings/<flow>.<impl>.run<N>.json` and are committed as
the expected behaviour. `-compare` prints one block per difference and a count;
the count is the assertion.

The tool owns Docker while it runs: it force-cleans every Kathara container and
network before and after each flow, on every path including failure.

## Two environment traps this harness had to solve

1. **`$HOME` does not move the settings file.** `utils.get_current_user_home`
   (`utils.py:212`) picks the `passwd_home` arm on Linux, so both
   implementations read `<pw_dir>/.config/kathara.conf` and ignore `$HOME`
   entirely. Verified: `HOME=/tmp/x python -c "from Kathara.setting.Setting
   import DEFAULT_SETTINGS_PATH; print(DEFAULT_SETTINGS_PATH)"` prints
   `/root/.config/kathara.conf`, and `HOME=/tmp/x kathara config get image`
   returns the value from the real file. cmdparity therefore installs its
   pinned `kathara.conf` **at the real default path** and restores the previous
   content on exit. (`tools/goldenharness/runner.go:437` sets `HOME` only, which
   on Linux pins nothing — see the findings note below.)

2. **Console width.** Both implementations honour `COLUMNS`. The harness pins
   200 rather than the golden harness's 80, because `create_lab_table` builds an
   eleven-column `expand=True` table and at 80 every cell renders as a single
   ellipsis. The cost is that the `usage:` block width then differs between the
   two (the port renders help at a fixed 80 — `DIVERGENCES.md` #103); use
   `-columns 80` to remove that axis.

## Flows

| Flow | What it covers |
|---|---|
| `vsugar` | `vstart` two devices on one collision domain, `list`, `vconfig --add`/`--add CD/MAC`/`--rm`, re-`--rm` of a removed CD, `vconfig` on an unknown device, `vclean` each, `vclean` of an already-stopped name |
| `vsugar-args` | The whole `§6/§7/§8` flag surface without Docker: `-h`, `--print`/`--dry-run`, every mutually-exclusive group, missing required, `--eth`/`--volume`/`--rm` type errors, unknown flag |
| `vstart-full` | One device deployed with the full device-configuration surface (`--eth` with MAC, `--mem`, `--cpus`, `-i`, `--bridged`, `--port` ×2, `--sysctl`, `--env` ×2, `--ulimit`, `--volume`, `--shell`, `-e`, `--entrypoint` + `--` args), plus `--hosthome`, `--privileged`, and a duplicate `vstart` |
| `lab` | `lstart` → `lconfig --add`/`--rm` → unknown-CD and unknown-device errors → `lrestart` (whole scenario and one device) → `lclean` |
| `listwipe` | `list` with a lab **and** a vlab up, `list -n`, `list -n <unknown>`, `list -a`, `wipe -f`, `wipe -f -a`, `wipe` without `--force` (EOF on stdin) |
| `exec` | Exit-code propagation (`true`/`false`/`exit 7`/missing binary → 127), stdout and stderr routing, `--no-stdout`, `--no-stderr`, both, `--` multiword, unknown device, `-v` with nothing running, argparse errors |
| `edge` | `lclean` with nothing up, `lstart` twice, `vconfig`/`vclean` aimed at a lab device through the vlab, the `lrestart --xterm` latent bug (`DIVERGENCES.md` on `LrestartCommand`), `--wait`, `lclean` with a device list / `--exclude`, `lclean -d <missing>` |

## What is normalized, and why

Every rule deletes a named source of nondeterminism. Nothing else is touched.

| Rule | Source |
|---|---|
| ANSI CSI/OSC/single-char escapes stripped, CR dropped | colour and cursor control carry no behaviour |
| Log records unfolded (a `CRITICAL `-class line absorbs its 9-space continuations) | `RichHandler` folds to the console width; the port emits records unfolded on purpose (`DIVERGENCES.md` #91) |
| Progress-bar glyphs (`━╸╹╺╻`), braille spinner frames, `docker pull` layer chatter dropped | frame count is a function of elapsed time |
| Literal tokens: scratch dir → `<LABDIR>`, lab hash → `<LABHASH>`, `kathara_vlab` hash → `<VLABHASH>`, user slug → `<USER>`, harness home → `<HOME>` | host identity |
| MACs → `<MAC>`, except `00:…:00`, `ff:…:ff` and any MAC the flow itself spells out (those reach the daemon as `kathara.mac_addr` and are an assertion) | the plugin derives a random MAC unless `kathara.machine` is sent — see `goldenharness/NORMALIZATION.md` |
| 64-hex ids → `<CID>`, Docker default-pool IPv4 → `<DOCKERIP>` | container/volume ids and bridge allocation |
| `TIMESTAMP: <datetime.now()>` → `TIMESTAMP: <TS>`, and the line is left-trimmed | `create_lab_table` titles the table with the wall clock; the title is centred, so its indent follows its own length |
| `list` table rewritten to a cell grid: horizontal runs collapsed on junction-bearing border rows, `PIDS`/`CPU USAGE`/`MEM USAGE`/`MEM PERCENT`/`NET USAGE` cells → `<STAT>` | rich sizes columns from content, and those five hold live counters, so two runs of the *same* binary lay the table out differently. Box style, header set and order, row order and every other cell stay asserted. Panels (two verticals) are untouched and stay byte-exact |
| `CapAdd` loses its `CAP_` prefix | the Go Docker SDK rewrites the list client-side inside `ContainerCreate`; unreachable from port code (`DIVERGENCES.md` #106) |
| `com.docker.network.endpoint.sysctls` comma list sorted | `DockerLink` joins a Python set; the order varies between two runs of the oracle itself |

## Expected differences

`-compare` is **not** expected to print zero blocks. As of the run recorded
here it prints 63, and every one of them falls into a class below. A block that
does not is a regression.

| Class | Blocks | Status |
|---|---|---|
| `list` table columns: Python has eleven, the port five (`PIDS`, `CPU USAGE`, `MEM USAGE`, `MEM PERCENT`, `NET USAGE`, `INTERFACES` absent) | 15 PARITY | Sanctioned: `PORT_SPEC.md` §0.3 defers resource sampling and enumerates the six inventory fields that survive |
| `list` row order differs between two Python runs | 7 DETERMINISM (py only) | Sanctioned: `ORDERING.tsv` row `DockerMachine.py:1044` marks the source `!!` nondeterministic and prescribes sorting by name, which the port does |
| `lstart` on an already-running scenario names whichever device the pool reached first | 2 | Inherent: `ORDERING.tsv` row `DockerMachine.py:597` — completion order under the deploy pool is nondeterministic |
| exit-2 stderr carries the port's **full help** where argparse prints the `usage:` block alone | 18 PARITY | **Finding — not sanctioned.** See below |
| `--format` and the port's own `--lab-hash`/`--lab-name`/`--from-archive`/`--name` rows appear in help and usage | included above | Sanctioned: `PORT_SPEC.md` §5.1, `DIVERGENCES.md` #102(a) |
| `usage:` block wrapped at 80 by the port, at `COLUMNS` by argparse | included above | Recorded: `DIVERGENCES.md` #103. Disappears with `-columns 80` |
| pflag's message text for a malformed flag (`unknown flag:`, `flag needs an argument:`, `invalid argument %q for %q flag:`) | included above | `DIVERGENCES.md` #102(b) covers the first two verbatim; the third is the `ArgumentTypeError` class and is **not** listed there |

**The exit-2 finding.** `app.usageError` (`cmd/kathara/root.go:288`) prints
`spec.Cmd.UsageString()`, which `parser.SetUsageFunc` (`cmd/kathara/parser.go:56`)
wires to `parser.usage()` → `usageAt(80)` — i.e. argparse's `format_help()`, not
its `format_usage()`. `argparse.ArgumentParser.error` calls `print_usage`, so
Python emits the `usage:` block and one message. `kathara vclean` with no
arguments is 2 lines of stderr from Python and 13 from the port; `lstart --bogus`
is 4 and 51. `CLI_SURFACE.md` §0.6 and `JSON_CLI_CONTRACT.md` §5.5 both pin
"usage + error on stderr" as observable, and `root.go:287`'s own comment says
"usage text plus one message". Smallest fix: add
`func (p *parser) usageBlock() string { return p.formatUsage(helpWidth(80)) }`
next to `usage()` in `cmd/kathara/usage.go` and call it from `usageError`. The
`--help` path (`root.go:228`) must keep `UsageString()`.

## Note on the golden harness's HOME pin

`tools/goldenharness/runner.go:71-89` writes a pinned `kathara.conf` into a
temporary HOME and `runKathara` exports `HOME=` it, and `NORMALIZATION.md` §1
claims the recording therefore "can depend neither on the operator's mutable
settings nor on the release / image update checks those settings would allow to
fire". On Linux that pin has no effect (trap 1 above): both binaries read
`<pw_dir>/.config/kathara.conf`. Whatever that file happens to contain — this
host's held `"image": "scenario/image"` and `"image_update_policy": "Prompt"`
before cmdparity replaced it — is what the goldens were recorded against.
