# goldenharness — Layer A CLI golden tests

The harness described in `docs/port/PORT_SPEC.md` §9, Layer A. It does not
import Kathara. It drives whatever binary `KATHARA_CMD` names over the
scenarios in `scenarios.yaml` and snapshots the observable state of each run as
a tree of canonical JSON under `test/goldens/<scenario>/`.

Record from the Python 3.8.3 oracle, replay against the Go build:

```sh
# record every scenario from the oracle (the default KATHARA_CMD)
go run ./tools/goldenharness record -v

# replay the same scenarios against the Go build and fail on any difference
KATHARA_CMD=./bin/kathara go run ./tools/goldenharness verify -v

# one scenario, or a glob
go run ./tools/goldenharness record -scenario 01-simple-configuration
go run ./tools/goldenharness verify -scenario 'syn-*'

# compare two snapshot trees that already exist
go run ./tools/goldenharness diff test/goldens /tmp/other-recording

# what the manifest resolves to, and whether every lab directory is present
go run ./tools/goldenharness list
```

| Environment | Default | Meaning |
|---|---|---|
| `KATHARA_CMD` | `/root/kathara/pyvenv/bin/python -m kathara` | Binary under test. Split on whitespace, so `python -m kathara` works. |
| `DOCKER_BIN` | `docker` | Docker CLI used for inspection and cleanup. |
| `KATHARA_LABS_ROOT` | `/root/kathara/Kathara-Labs` | Also settable per run with `-labs-root`, and in the manifest under `defaults.labs_root`. |

The binary under test does **not** see the operator's `~/.config/kathara.conf`:
each `record`/`verify` run creates a scratch `HOME` holding a pinned settings
file (stock 3.8.3 defaults, `image_update_policy: Never`, release check
disabled), so recordings cannot depend on operator settings, GitHub release
state or Docker Hub digest drift. See NORMALIZATION.md section 1.

## What a snapshot contains

```
test/goldens/<scenario>/
  scenario.json     what ran, plus the derived-identifier assertions
                    (lab hash, user slug, container and network naming)
  commands.json     exit code and normalized stdout/stderr of lstart and lclean
  containers.json   docker inspect subset per device: labels, hostname, image,
                    user, caps, privileged, sysctls, ulimits, memory, NanoCPUs,
                    mounts, env, cmd/entrypoint, port bindings, and one entry
                    per network endpoint with its kathara.iface / kathara.link
                    wiring assertions and split-and-sorted endpoint sysctls
  networks.json     docker network inspect subset: name, driver, labels,
                    external flag, IPAM driver
  probes/<dev>.json in-container probes: ip -br link, ip -j addr, ip route,
                    /etc/hosts, hostname, the /hostlab and /shared file trees
                    with a sha256 per file, and any per-scenario file probes
  teardown.json     post-lclean state; the container and network lists must be
                    empty, plus any host-side files the scenario declares
```

Every normalization applied on the way in, and the nondeterminism source that
justifies it, is documented in `NORMALIZATION.md`. Read it before adding a
scenario or changing a probe.

## Operating rules

- Scenarios run **strictly one at a time**. They share a Docker daemon, a
  lab-hash namespace and a set of network names.
- Each scenario is time-limited (240 s default, per-scenario override in the
  manifest). `lclean` and the post-teardown check run on a separate budget so a
  hung `lstart` is still torn down.
- Cleanup is unconditional: on success, assertion failure, error, timeout or
  SIGINT the harness force-removes every `app=kathara` container and network.
  A scenario also refuses to start while any Kathara state is present.
- Wiring assertions (`kathara.iface`, `kathara.link`, explicit-MAC agreement,
  derived container/network names, empty teardown) are **failures**, not
  diffs — `record` exits non-zero and names them.

## Adding a scenario

1. Put the lab under `test/scenarios/synthetic/<name>/` (or point at a
   Kathara-Labs path with `dir: labs:<rel>`).
2. Startup scripts must be deterministic: no clocks, no network, no
   randomness. Use `/shared` as the observation channel for anything that has
   to survive the container, and declare `reset_files` so markers cannot
   accumulate between recordings. A lab that uses the `volume` option must
   declare its host paths under `host_dirs`, or the recording depends on
   whether those paths happen to exist on the host.
3. Add an entry to `scenarios.yaml` with a `note` saying what it covers. If the
   lab's startup leaves a kernel state machine converging (an in-device bridge
   coming up, for instance), set `settle_seconds` and say so in the note —
   `lstart` returns before the startup commands have even run.
4. `go run ./tools/goldenharness record -scenario <name>`, read the snapshot,
   then run `verify` at least twice to prove the recording is stable.
